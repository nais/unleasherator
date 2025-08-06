package controller

import (
	"context"
	"fmt"
	"net/http"
	"time"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/go-logr/logr"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/federation"
	"github.com/nais/unleasherator/internal/o11y"
	"github.com/nais/unleasherator/internal/resources"
	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/nais/unleasherator/internal/utils"
)

const (
	unleashFinalizer                  = "unleash.nais.io/finalizer"
	unleashPublishMetricStatusSending = "sending"
	unleashPublishMetricStatusSuccess = "success"
	unleashPublishMetricStatusFailed  = "failed"
)

var (
	deploymentTimeout = 5 * time.Minute
	requeueAfter      = 1 * time.Hour

	// unleashStatus is a Prometheus metric which will be used to expose the status of the Unleash instances
	unleashStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_unleash_status",
			Help: "Status of Unleash instances",
		},
		[]string{"namespace", "name", "status"},
	)

	unleashPublished = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_federation_published_total",
			Help: "Number of Unleash federation messages published with status",
		},
		[]string{"state", "status"},
	)
)

func init() {
	metrics.Registry.MustRegister(unleashStatus, unleashPublished)
}

// UnleashReconciler reconciles a Unleash object
type UnleashReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string
	Federation        UnleashFederation
	Tracer            trace.Tracer
}

type UnleashFederation struct {
	Enabled   bool
	Publisher federation.Publisher
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=services/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/finalizers,verbs=update
//+kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

func (r *UnleashReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	spanOpts := o11y.ReconcilerAttributes(ctx, req)
	ctx, span := r.Tracer.Start(ctx, "Reconcile Unleash", spanOpts...)
	defer span.End()

	log := log.FromContext(ctx).WithName("unleash").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Starting reconciliation of Unleash")

	unleash := &unleashv1.Unleash{}
	err := r.Get(ctx, req.NamespacedName, unleash)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Unleash resource not found. Ignoring since object must be deleted")
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "Failed to get Unleash")
		return ctrl.Result{}, err
	}

	// Check if marked for deletion
	if unleash.GetDeletionTimestamp() != nil {
		span.AddEvent("Unleash marked for deletion")
		log.Info("Unleash marked for deletion")
		if controllerutil.ContainsFinalizer(unleash, unleashFinalizer) {
			log.Info("Performing Finalizer Operations for Unleash before delete CR")

			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeDegraded,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer options",
			})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return ctrl.Result{}, err
			}

			r.doFinalizerOperationsForUnleash(unleash, ctx, log)

			if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
				log.Error(err, "Failed to get Unleash")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for Unleash %s name were successfully accomplished", unleash.Name),
			})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return ctrl.Result{}, err
			}

			if ok := controllerutil.RemoveFinalizer(unleash, unleashFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer for Unleash")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to remove finalizer for Unleash")
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{Requeue: false}, nil
	}

	// Set status to unknown if no status is set
	if len(unleash.Status.Conditions) == 0 {
		span.AddEvent("Setting status as unknown for Unleash")
		log.Info("Setting status as unknown for Unleash")
		meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{
			Type:    unleashv1.UnleashStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})

		if err = r.Status().Update(ctx, unleash); err != nil {
			log.Error(err, "Failed to update Unleash status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
			log.Error(err, "Failed to get Unleash")
			return ctrl.Result{}, err
		}
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(unleash, unleashFinalizer) {
		span.AddEvent("Adding finalizer for Unleash")
		log.Info("Adding finalizer for Unleash")

		if ok := controllerutil.AddFinalizer(unleash, unleashFinalizer); !ok {
			log.Info("Finalizer already present for Unleash")
			return ctrl.Result{}, nil
		}

		// Retry update on conflicts (common when multiple controllers are operating)
		err = wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 2*time.Second, false, func(ctx context.Context) (done bool, err error) {
			if updateErr := r.Update(ctx, unleash); updateErr != nil {
				if apierrors.IsConflict(updateErr) {
					// Refetch and retry
					if fetchErr := r.Get(ctx, req.NamespacedName, unleash); fetchErr != nil {
						return false, fetchErr
					}
					if !controllerutil.ContainsFinalizer(unleash, unleashFinalizer) {
						controllerutil.AddFinalizer(unleash, unleashFinalizer)
					}
					return false, nil // retry
				}
				return false, updateErr
			}
			return true, nil
		})

		if err != nil {
			log.Error(err, "Failed to update Unleash to add finalizer")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
			log.Error(err, "Failed to get Unleash")
			return ctrl.Result{}, err
		}
	}

	var res ctrl.Result

	span.AddEvent("Reconciling Secrets")
	res, err = r.reconcileSecrets(ctx, unleash)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to reconcile Secrets")
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Secrets"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.RequeueAfter > 0 {
		return res, nil
	}

	span.AddEvent("Reconciling NetworkPolicy")
	res, err = r.reconcileNetworkPolicy(ctx, unleash)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to reconcile NetworkPolicy")
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile NetworkPolicy"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.RequeueAfter > 0 {
		return res, nil
	}

	span.AddEvent("Reconciling Deployment")
	res, err = r.reconcileDeployment(ctx, unleash)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to reconcile Deployment")
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Deployment"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.RequeueAfter > 0 {
		return res, nil
	}

	span.AddEvent("Reconciling Service")
	res, err = r.reconcileService(ctx, unleash)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to reconcile Service")
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Service"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.RequeueAfter > 0 {
		return res, nil
	}

	span.AddEvent("Reconciling Ingresses")
	res, err = r.reconcileIngresses(ctx, unleash)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to reconcile Ingresses")
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Ingresses"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.RequeueAfter > 0 {
		return res, nil
	}

	span.AddEvent("Reconciling ServiceMonitor")
	res, err = r.reconcileServiceMonitor(ctx, unleash)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "Failed to reconcile ServiceMonitor")
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile ServiceMonitor"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.RequeueAfter > 0 {
		return res, nil
	}

	// Re-fetch Unleash instance before update the status
	err = r.Get(ctx, req.NamespacedName, unleash)
	if err != nil {
		log.Error(err, "Failed to get Unleash")
		return ctrl.Result{}, err
	}

	// Wait for Deployment rollout to finish before testing connection This is to
	// avoid testing connection to the previous instance if the Deployment is not
	// ready yet. Delay requeue to avoid tying up the reconciler since waiting is
	// done in the same reconcile loop.
	log.WithValues("timeout", deploymentTimeout).Info("Waiting for Deployment rollout to finish")
	if err = r.waitForDeployment(ctx, deploymentTimeout, req.NamespacedName); err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, fmt.Sprintf("Deployment rollout timed out after %s", deploymentTimeout)); err != nil {
			return ctrl.Result{RequeueAfter: deploymentTimeout}, err
		}
		return ctrl.Result{RequeueAfter: deploymentTimeout}, err
	}

	// Set the reconcile status of the Unleash instance to available
	log.Info("Successfully reconciled Unleash resources")
	if err = r.updateStatusReconcileSuccess(ctx, unleash); err != nil {
		return ctrl.Result{}, err
	}

	span.AddEvent("Testing connection to Unleash instance")
	stats, err := r.testConnection(unleash, ctx, log)
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, unleash, err, "Failed to connect to Unleash instance"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	span.SetAttributes(attribute.String("unleash.version", stats.VersionOSS))

	// Set the connection status of the Unleash instance to available
	if err = r.updateStatusConnectionSuccess(ctx, unleash, stats); err != nil {
		return ctrl.Result{}, err
	}

	// Publish the Unleash instance to federation if enabled
	if err = r.publish(ctx, unleash); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciliation of Unleash finished")
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// publish the Unleash instance to pubsub if federation is enabled.
// It fetches the API token and publishes the instance using the federation publisher.
// If the API token cannot be fetched, it returns an error.
func (r *UnleashReconciler) publish(ctx context.Context, unleash *unleashv1.Unleash) error {
	log := log.FromContext(ctx).WithName("publish")

	if !r.Federation.Enabled || !unleash.Spec.Federation.Enabled {
		log.Info("Federation is disabled, skipping publishing")
		return nil
	}

	ctx, span := r.Tracer.Start(ctx, "Publish Federation")
	defer span.End()

	log.Info("Publishing Unleash instance to federation")
	// Count the number of Unleash instances published
	unleashPublished.WithLabelValues("provisioned", unleashPublishMetricStatusSending).Inc()

	token, err := unleash.AdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		unleashPublished.WithLabelValues("provisioned", unleashPublishMetricStatusFailed).Inc()
		log.Error(err, "Failed to fetch API token")
		return fmt.Errorf("publish could not fetch API token: %w", err)
	}

	err = r.Federation.Publisher.Publish(ctx, unleash, string(token))
	if err != nil {
		unleashPublished.WithLabelValues("provisioned", unleashPublishMetricStatusFailed).Inc()
		return fmt.Errorf("publish could not publish Unleash instance: %w", err)
	}

	unleashPublished.WithLabelValues("provisioned", unleashPublishMetricStatusSuccess).Inc()
	return nil
}

// finalizeUnleash will perform the required operations before delete the CR.
func (r *UnleashReconciler) doFinalizerOperationsForUnleash(cr *unleashv1.Unleash, ctx context.Context, log logr.Logger) {
	// @TODO make it optional to delete the operator secret
	operatorSecret := cr.NamespacedOperatorSecretName(r.OperatorNamespace)

	err := r.Delete(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      operatorSecret.Name,
			Namespace: operatorSecret.Namespace,
		},
	})
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to delete the secret %s from the namespace %s",
			operatorSecret.Name,
			operatorSecret.Namespace))

		r.Recorder.Event(cr, "Warning", "Deleting",
			fmt.Sprintf("Failed to delete the secret %s from the namespace %s",
				operatorSecret.Name,
				operatorSecret.Namespace))
	}

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Unleash %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
}

// reconcileServiceMonitor will ensure that the required ServiceMonitor is created
func (r *UnleashReconciler) reconcileServiceMonitor(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("unleash")

	newServiceMonitor, err := resources.ServiceMonitorForUnleash(unleash, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingServiceMonitor := &monitoringv1.ServiceMonitor{}
	err = r.Get(ctx, unleash.NamespacedName(), existingServiceMonitor)

	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to get ServiceMonitor for Unleash", "ServiceMonitor.Namespace", existingServiceMonitor.Namespace, "ServiceMonitor.Name", existingServiceMonitor.Name)
		return ctrl.Result{}, err
	}

	if !unleash.Spec.Prometheus.Enabled {
		if !apierrors.IsNotFound(err) {
			log.Info("Deleting ServiceMonitor", "ServiceMonitor.Namespace", existingServiceMonitor.Namespace, "ServiceMonitor.Name", existingServiceMonitor.Name)
			if err := r.Delete(ctx, existingServiceMonitor); err != nil {
				log.Error(err, "Failed to delete ServiceMonitor for Unleash", "ServiceMonitor.Namespace", existingServiceMonitor.Namespace, "ServiceMonitor.Name", existingServiceMonitor.Name)
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// If the ServiceMonitor does not exist, we create it
	if apierrors.IsNotFound(err) {
		log.Info("Creating ServiceMonitor", "ServiceMonitor.Namespace", newServiceMonitor.Namespace, "ServiceMonitor.Name", newServiceMonitor.Name)
		if err := r.Create(ctx, newServiceMonitor); err != nil {
			log.Error(err, "Failed to create new ServiceMonitor", "ServiceMonitor.Namespace", newServiceMonitor.Namespace, "ServiceMonitor.Name", newServiceMonitor.Name)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// If the ServiceMonitor exists, we check if it needs to be updated
	if !equality.Semantic.DeepDerivative(newServiceMonitor.Spec, existingServiceMonitor.Spec) || !equality.Semantic.DeepDerivative(newServiceMonitor.ObjectMeta.Labels, existingServiceMonitor.ObjectMeta.Labels) {
		log.Info("Updating ServiceMonitor", "ServiceMonitor.Namespace", existingServiceMonitor.Namespace, "ServiceMonitor.Name", existingServiceMonitor.Name)

		existingServiceMonitor.Spec = newServiceMonitor.Spec
		existingServiceMonitor.ObjectMeta.Labels = newServiceMonitor.ObjectMeta.Labels

		if err := r.Update(ctx, existingServiceMonitor); err != nil {
			log.Error(err, "Failed to update ServiceMonitor", "ServiceMonitor.Namespace", existingServiceMonitor.Namespace, "ServiceMonitor.Name", existingServiceMonitor.Name)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// reconcileNetworkPolicy will ensure that the required network policy is created
func (r *UnleashReconciler) reconcileNetworkPolicy(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("unleash")

	newNetPol, err := resources.NetworkPolicyForUnleash(unleash, r.Scheme, r.OperatorNamespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingNetPol := &networkingv1.NetworkPolicy{}
	err = r.Get(ctx, unleash.NamespacedName(), existingNetPol)

	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to get NetworkPolicy for Unleash", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
		return ctrl.Result{}, err
	}

	// If the NetworkPolicy is not enabled, we delete it if it exists.
	if !unleash.Spec.NetworkPolicy.Enabled {
		if !apierrors.IsNotFound(err) {
			log.Info("Deleting NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
			if err := r.Delete(ctx, existingNetPol); err != nil {
				log.Error(err, "Failed to delete NetworkPolicy for Unleash", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
				return ctrl.Result{}, err
			}
		}

		return ctrl.Result{}, nil
	}

	// If the NetworkPolicy is enabled and does not exist, we create it.
	if err != nil && apierrors.IsNotFound(err) {
		log.Info("Creating NetworkPolicy", "NetworkPolicy.Namespace", newNetPol.Namespace, "NetworkPolicy.Name", newNetPol.Name)
		err = r.Create(ctx, newNetPol)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// If the NetworkPolicy is enabled and exists, we update it if it is not up to date.
	if !equality.Semantic.DeepDerivative(newNetPol.Spec, existingNetPol.Spec) || !equality.Semantic.DeepDerivative(newNetPol.ObjectMeta.Labels, existingNetPol.ObjectMeta.Labels) {
		log.Info("Updating NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)

		existingNetPol.Spec = newNetPol.Spec
		existingNetPol.ObjectMeta.Labels = newNetPol.ObjectMeta.Labels

		if err := r.Update(ctx, existingNetPol); err != nil {
			log.Error(err, "Failed to update NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileIngress will ensure that the required ingress is created
func (r *UnleashReconciler) reconcileIngress(ctx context.Context, unleash *unleashv1.Unleash, ingress *unleashv1.UnleashIngressConfig, nameSuffix string) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("unleash")

	newIngress, err := resources.IngressForUnleash(unleash, ingress, nameSuffix, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingIngress := &networkingv1.Ingress{}
	err = r.Get(ctx, unleash.NamespacedNameWithSuffix(nameSuffix), existingIngress)

	if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to get Ingress for Unleash", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
		return ctrl.Result{}, err
	}

	// If the ingress is not enabled, we delete the existing one if it exists.
	if !ingress.Enabled {
		if !apierrors.IsNotFound(err) {
			log.Info("Deleting Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
			if err := r.Delete(ctx, existingIngress); err != nil {
				log.Error(err, "Failed to delete Ingress for Unleash", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// If the ingress is enabled and does not exist, we create it.
	if err != nil && apierrors.IsNotFound(err) {
		log.Info("Creating Ingress", "Ingress.Namespace", newIngress.Namespace, "Ingress.Name", newIngress.Name)
		err = r.Create(ctx, newIngress)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// If the ingress is enabled and exists, we update it if it has changed.
	if !equality.Semantic.DeepDerivative(newIngress.Spec, existingIngress.Spec) || !equality.Semantic.DeepDerivative(newIngress.ObjectMeta.Labels, existingIngress.ObjectMeta.Labels) {
		log.Info("Updating Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)

		existingIngress.Spec = newIngress.Spec
		existingIngress.ObjectMeta.Labels = newIngress.ObjectMeta.Labels

		if err := r.Update(ctx, existingIngress); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileIngresses will ensure that the required ingresses are created
func (r *UnleashReconciler) reconcileIngresses(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	res, err := r.reconcileIngress(ctx, unleash, &unleash.Spec.WebIngress, "web")
	if err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	res, err = r.reconcileIngress(ctx, unleash, &unleash.Spec.ApiIngress, "api")
	if err != nil || res.RequeueAfter > 0 {
		return res, err
	}

	return ctrl.Result{}, nil
}

// reconcileSecrets will ensure that the required secrets are created
func (r *UnleashReconciler) reconcileSecrets(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("unleash")

	// Check if operator secret already exists, if not create a new one
	operatorSecret := &corev1.Secret{}
	err := r.Get(ctx, unleash.NamespacedOperatorSecretName(r.OperatorNamespace), operatorSecret)
	if err != nil && apierrors.IsNotFound(err) {
		adminKey, err := resources.GenerateAdminKey()
		if err != nil {
			return ctrl.Result{}, err
		}

		operatorSecret = resources.OperatorSecretForUnleash(unleash.GetName(), unleash.GetOperatorSecretName(), r.OperatorNamespace, adminKey)
		log.Info("Creating Operator Secret for Unleash", "Secret.Namespace", operatorSecret.Namespace, "Secret.Name", operatorSecret.Name)
		err = r.Create(ctx, operatorSecret)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	adminKey := string(operatorSecret.Data[unleashv1.UnleashSecretTokenKey])
	if adminKey == "" {
		err = fmt.Errorf("operator secret is empty for key %s", unleashv1.UnleashSecretTokenKey)
		log.Error(err, "Failed to get admin token secret", "Secret.Namespace", operatorSecret.Namespace, "Secret.Name", operatorSecret.Name, "Secret.Key", unleashv1.UnleashSecretTokenKey)

		return ctrl.Result{}, err
	}

	// Check if instance secret already exists, if not create a new one
	instanceSecret := &corev1.Secret{}
	err = r.Get(ctx, unleash.NamespacedInstanceSecretName(), instanceSecret)
	if err != nil && apierrors.IsNotFound(err) {
		instanceSecret, err = resources.InstanceSecretForUnleash(unleash, r.Scheme, adminKey)
		if err != nil {
			return ctrl.Result{}, err
		}
		log.Info("Creating Instance Secret for Unleash", "Secret.Namespace", instanceSecret.Namespace, "Secret.Name", instanceSecret.Name)
		err = r.Create(ctx, instanceSecret)
		if err != nil {
			return ctrl.Result{}, err
		}
	} else if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// reconcileDeployment will ensure that the required deployment is created
func (r *UnleashReconciler) reconcileDeployment(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("unleash")

	found := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: unleash.Name, Namespace: unleash.Namespace}, found)
	isCreating := err != nil && apierrors.IsNotFound(err)

	// Handle ReleaseChannel resolution - always resolve when ReleaseChannel is specified
	// to keep status up to date, regardless of CustomImage setting
	if unleash.Spec.ReleaseChannel.Name != "" {
		resolvedImage, modified, err := resources.ResolveReleaseChannelImage(ctx, r.Client, unleash)
		if err != nil {
			log.Error(err, "Failed to resolve ReleaseChannel image", "ReleaseChannel", unleash.Spec.ReleaseChannel.Name)
			return ctrl.Result{}, err
		}

		// If the resource was modified (status updated), update it
		if modified {
			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status with resolved ReleaseChannel image")
				return ctrl.Result{}, err
			}

			log.Info("Updated ReleaseChannel image tracking in status", "ReleaseChannel", unleash.Spec.ReleaseChannel.Name, "ResolvedImage", resolvedImage)

			// Fetch the updated resource to get the latest status
			if err := r.Get(ctx, types.NamespacedName{Name: unleash.Name, Namespace: unleash.Namespace}, unleash); err != nil {
				log.Error(err, "Failed to get updated Unleash")
				return ctrl.Result{}, err
			}
		}
	}

	dep, err := resources.DeploymentForUnleash(unleash, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	if isCreating {
		log.Info("Creating Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	} else if err != nil {
		log.Error(err, "Failed to get Deployment")
		return ctrl.Result{}, err
	} else if !equality.Semantic.DeepDerivative(dep.Spec, found.Spec) || !equality.Semantic.DeepEqual(dep.ObjectMeta.Labels, found.ObjectMeta.Labels) {
		log.Info("Updating Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

		found.Spec = dep.Spec
		found.ObjectMeta.Labels = dep.ObjectMeta.Labels

		if err := r.Update(ctx, found); err != nil {
			log.Error(err, "Failed to update Deployment", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	} else {
		log.Info("Skip reconcile: Deployment already up to date", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
		return ctrl.Result{}, nil
	}
}

// reconcileService will ensure that the Service for the Unleash instance is created
func (r *UnleashReconciler) reconcileService(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("unleash")

	newSvc, err := resources.ServiceForUnleash(unleash, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingSvc := &corev1.Service{}
	err = r.Get(ctx, unleash.NamespacedName(), existingSvc)
	if err != nil && apierrors.IsNotFound(err) {
		log.Info("Creating Service", "Service.Namespace", newSvc.Namespace, "Service.Name", newSvc.Name)
		err = r.Create(ctx, newSvc)
		if err != nil {
			log.Error(err, "Failed to create new Service", "Service.Namespace", newSvc.Namespace, "Service.Name", newSvc.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	} else if !equality.Semantic.DeepDerivative(newSvc.Spec, existingSvc.Spec) || !equality.Semantic.DeepDerivative(newSvc.ObjectMeta.Labels, existingSvc.ObjectMeta.Labels) {
		log.Info("Updating Service", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)

		existingSvc.Spec = newSvc.Spec
		existingSvc.ObjectMeta.Labels = newSvc.ObjectMeta.Labels

		if err := r.Update(ctx, existingSvc); err != nil {
			log.Error(err, "Failed to update Service", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
			return ctrl.Result{}, err
		}
	}

	log.Info("Skip reconcile: Service up to date", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
	return ctrl.Result{}, nil
}

// waitForDeployment will wait for the deployment to be available
func (r *UnleashReconciler) waitForDeployment(ctx context.Context, timeout time.Duration, key types.NamespacedName) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		deployment := &appsv1.Deployment{}
		if err := r.Client.Get(ctx, key, deployment); err != nil {
			return false, err
		}

		return utils.DeploymentIsReady(deployment), nil
	})

	return err
}

// testConnection will test the connection to the Unleash instance
func (r *UnleashReconciler) testConnection(unleash resources.UnleashInstance, ctx context.Context, log logr.Logger) (*unleashclient.InstanceAdminStatsResult, error) {
	client, err := unleash.ApiClient(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		log.Error(err, "Failed to set up client for Unleash")
		return nil, err
	}

	stats, res, err := client.GetInstanceAdminStats(ctx)

	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to connect to Unleash instance on %s", unleash.URL()))
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		log.Error(err, fmt.Sprintf("Unleash connection check failed with status code %d", res.StatusCode))
		return nil, err
	}

	return stats, nil
}

func (r *UnleashReconciler) updateStatusReconcileSuccess(ctx context.Context, unleash *unleashv1.Unleash) error {
	return r.updateStatus(ctx, unleash, nil, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
}

func (r *UnleashReconciler) updateStatusReconcileFailed(ctx context.Context, unleash *unleashv1.Unleash, err error, message string) error {
	log := log.FromContext(ctx).WithName("unleash")

	if verr, ok := err.(*resources.ValidationError); ok {
		message = fmt.Sprintf("%s: %s", message, verr.Error())
	}

	log.Error(err, message)
	return r.updateStatus(ctx, unleash, nil, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *UnleashReconciler) updateStatusConnectionSuccess(ctx context.Context, unleash *unleashv1.Unleash, stats *unleashclient.InstanceAdminStatsResult) error {
	return r.updateStatus(ctx, unleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash instance",
	})
}

func (r *UnleashReconciler) updateStatusConnectionFailed(ctx context.Context, unleash *unleashv1.Unleash, err error, message string) error {
	log := log.FromContext(ctx).WithName("unleash")

	log.Error(err, fmt.Sprintf("%s for Unleash", message))
	return r.updateStatus(ctx, unleash, nil, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *UnleashReconciler) updateStatus(ctx context.Context, unleash *unleashv1.Unleash, stats *unleashclient.InstanceAdminStatsResult, status metav1.Condition) error {
	log := log.FromContext(ctx).WithName("unleash")

	switch status.Type {
	case unleashv1.UnleashStatusConditionTypeReconciled:
		unleash.Status.Reconciled = status.Status == metav1.ConditionTrue
	case unleashv1.UnleashStatusConditionTypeConnected:
		unleash.Status.Connected = status.Status == metav1.ConditionTrue
	}

	if stats != nil {
		if stats.VersionEnterprise != "" {
			unleash.Status.Version = stats.VersionEnterprise
		} else {
			unleash.Status.Version = stats.VersionOSS
		}
	}

	val := promGaugeValueForStatus(status.Status)
	unleashStatus.WithLabelValues(unleash.Namespace, unleash.Name, status.Type).Set(val)

	meta.SetStatusCondition(&unleash.Status.Conditions, status)

	// Retry status updates on conflicts (common when multiple controllers are operating)
	err := wait.PollUntilContextTimeout(ctx, 100*time.Millisecond, 2*time.Second, false, func(ctx context.Context) (done bool, err error) {
		if updateErr := r.Status().Update(ctx, unleash); updateErr != nil {
			if apierrors.IsConflict(updateErr) {
				// For status updates, we don't need to refetch the entire object since status is separate
				// Just retry the status update
				return false, nil // retry
			}
			return false, updateErr
		}
		return true, nil
	})

	if err != nil {
		log.Error(err, "Failed to update status for Unleash")
		return err
	}

	return nil
}

// findUnleashInstancesForReleaseChannel finds all Unleash instances that reference a given ReleaseChannel
func (r *UnleashReconciler) findUnleashInstancesForReleaseChannel(ctx context.Context, obj client.Object) []ctrl.Request {
	releaseChannel, ok := obj.(*unleashv1.ReleaseChannel)
	if !ok {
		return nil
	}

	// Find all Unleash instances in the same namespace that reference this ReleaseChannel
	unleashList := &unleashv1.UnleashList{}
	if err := r.List(ctx, unleashList, client.InNamespace(releaseChannel.Namespace)); err != nil {
		return nil
	}

	var requests []ctrl.Request
	for _, unleash := range unleashList.Items {
		if unleash.Spec.ReleaseChannel.Name == releaseChannel.Name {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      unleash.Name,
					Namespace: unleash.Namespace,
				},
			})
		}
	}

	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *UnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.Unleash{}).
		Watches(
			&unleashv1.ReleaseChannel{},
			handler.EnqueueRequestsFromMapFunc(r.findUnleashInstancesForReleaseChannel),
		).
		//Owns(&appsv1.Deployment{}).
		// Note: Removed GenerationChangedPredicate to allow status-only changes to trigger reconciliation
		// This is needed because ReleaseChannel status changes (phase transitions) need to trigger
		// Unleash controller reconciliation, but GenerationChangedPredicate only triggers on spec changes
		//WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}
