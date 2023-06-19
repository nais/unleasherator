package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/golang/protobuf/proto"
	"github.com/google/uuid"
	"github.com/nais/unleasherator/pkg/pb"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/prometheus/client_golang/prometheus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/go-logr/logr"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/resources"
	unleashclient "github.com/nais/unleasherator/pkg/unleash"
)

const unleashFinalizer = "unleash.nais.io/finalizer"

var (
	// unleashStatus is a Prometheus metric which will be used to expose the status of the Unleash instances
	unleashStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_unleash_status",
			Help: "Status of Unleash instances",
		},
		[]string{"namespace", "name", "status"},
	)
)

func init() {
	metrics.Registry.MustRegister(unleashStatus)
}

// UnleashReconciler reconciles a Unleash object
type UnleashReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string
	PubSubClient      *pubsub.Client
	TopicName         string
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
	log := log.FromContext(ctx)

	log.Info("Starting reconciliation of Unleash")

	// Fetch the Unleash instance
	unleash := &unleashv1.Unleash{}
	err := r.Get(ctx, req.NamespacedName, unleash)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If the Unleash is not found then, it usually means that it was deleted or not created
			// In this way, we will stop the reconciliation
			log.Info("unleash resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get unleash")
		return ctrl.Result{}, err
	}

	// Let's just set the status as Unknown when no status are available
	if unleash.Status.Conditions == nil || len(unleash.Status.Conditions) == 0 {
		log.Info("Setting status as Unknown for Unleash")
		meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: unleashv1.UnleashStatusConditionTypeReconciled, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
		if err = r.Status().Update(ctx, unleash); err != nil {
			log.Error(err, "Failed to update Unleash status")
			return ctrl.Result{}, err
		}

		// Let's re-fetch the unleash Unleash after update the status
		// so that we have the latest state of the resource on the cluster and we will avoid
		// raise the issue "the object has been modified, please apply
		// your changes to the latest version and try again" which would re-trigger the reconciliation
		// if we try to update it again in the following operations
		if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
			log.Error(err, "Failed to re-fetch unleash")
			return ctrl.Result{}, err
		}
	}

	// Let's add a finalizer. Then, we can define some operations which should
	// occurs before the Unleash to be deleted.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers
	if !controllerutil.ContainsFinalizer(unleash, unleashFinalizer) {
		log.Info("Adding Finalizer for Unleash")

		if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
			log.Error(err, "Failed to re-fetch unleash")
			return ctrl.Result{}, err
		}

		if ok := controllerutil.AddFinalizer(unleash, unleashFinalizer); !ok {
			log.Error(err, "Failed to add finalizer into the Unleash")
			return ctrl.Result{Requeue: true}, nil
		}

		if err = r.Update(ctx, unleash); err != nil {
			log.Error(err, "Failed to update Unleash to add finalizer")
			return ctrl.Result{}, err
		}
	}

	// Check if the Unleash instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isUnleashMarkedToBeDeleted := unleash.GetDeletionTimestamp() != nil
	if isUnleashMarkedToBeDeleted {
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

			// TODO(user): If you add operations to the doFinalizerOperationsForUnleash method
			// then you need to ensure that all worked fine before deleting and updating the Downgrade status
			// otherwise, you should requeue here.

			// Re-fetch the unleash Unleash before update the status
			// so that we have the latest state of the resource on the cluster and we will avoid
			// raise the issue "the object has been modified, please apply
			// your changes to the latest version and try again" which would re-trigger the reconciliation
			if err := r.Get(ctx, req.NamespacedName, unleash); err != nil {
				log.Error(err, "Failed to re-fetch unleash")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: unleashv1.UnleashStatusConditionTypeDegraded,
				Status: metav1.ConditionTrue, Reason: "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for Unleash %s name were successfully accomplished", unleash.Name)})

			if err := r.Status().Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return ctrl.Result{}, err
			}

			log.Info("Removing Finalizer for Unleash after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(unleash, unleashFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer for Unleash")
				return ctrl.Result{Requeue: true}, nil
			}

			if err := r.Update(ctx, unleash); err != nil {
				log.Error(err, "Failed to remove finalizer for Unleash")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	var res ctrl.Result

	res, err = r.reconcileSecrets(ctx, unleash)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Secrets"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	res, err = r.reconcileDeployment(ctx, unleash)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Deployment"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	res, err = r.reconcileService(ctx, unleash)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Service"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	res, err = r.reconcileNetworkPolicy(ctx, unleash)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile NetworkPolicy"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	res, err = r.reconcileIngresses(ctx, unleash)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile Ingresses"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	res, err = r.reconcileServiceMonitor(ctx, unleash)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, unleash, err, "Failed to reconcile ServiceMonitor"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, err
	} else if res.Requeue {
		return res, nil
	}

	// Re-fetch Unleash instance before update the status
	err = r.Get(ctx, req.NamespacedName, unleash)
	if err != nil {
		log.Error(err, "Failed to re-fetch Unleash")
		return ctrl.Result{}, err
	}

	// Set the reconcile status of the Unleash instance to available
	if err = r.updateStatusReconcileSuccess(ctx, unleash); err != nil {
		return ctrl.Result{}, err
	}

	// Test connection to Unleash instance
	stats, err := r.testConnection(unleash, ctx, log)
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, unleash, err, "Failed to connect to Unleash instance"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Set the connection status of the Unleash instance to available
	err = r.updateStatusConnectionSuccess(ctx, unleash, stats)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.updateStatusConnectionSuccess(ctx, unleash, stats)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, err
}

func (r *UnleashReconciler) PublishUnleasheratorMessage(ctx context.Context, unleash *unleashv1.Unleash) error {
	token, err := GetApiToken(ctx, r, unleash.GetName(), unleash.GetNamespace())
	if err != nil {
		return fmt.Errorf("fetch secret api token: %w", err)
	}

	instance := UnleasheratorInstance(unleash, token)
	payload, err := proto.Marshal(instance)
	if err != nil {
		return fmt.Errorf("marshal protobuf message: %w", err)
	}

	msg := &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
	}
	result := r.PubSubClient.Topic(r.TopicName).Publish(ctx, msg)
	_, err = result.Get(ctx)
	if err != nil {
		return fmt.Errorf("publish protobuf message: %w", err)
	}

	return nil
}

func UnleasheratorInstance(unleash *unleashv1.Unleash, token string) *pb.Instance {
	return &pb.Instance{
		Version:     pb.Version,
		Status:      pb.Status_Provisioned,
		Name:        unleash.GetName(),
		Url:         unleash.PublicSecureURL(),
		SecretToken: token,
		Namespaces:  []string{unleash.GetName()},
	}
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
	log := log.FromContext(ctx)

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
		log.Info("Creating a new ServiceMonitor", "ServiceMonitor.Namespace", newServiceMonitor.Namespace, "ServiceMonitor.Name", newServiceMonitor.Name)
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
	log := log.FromContext(ctx)

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
		return ctrl.Result{Requeue: true}, nil
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

		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileIngress will ensure that the required ingress is created
func (r *UnleashReconciler) reconcileIngress(ctx context.Context, unleash *unleashv1.Unleash, ingress *unleashv1.UnleashIngressConfig, nameSuffix string) (ctrl.Result, error) {
	log := log.FromContext(ctx)

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
		return ctrl.Result{Requeue: true}, nil
	}

	// If the ingress is enabled and exists, we update it if it has changed.
	if !equality.Semantic.DeepDerivative(newIngress.Spec, existingIngress.Spec) || !equality.Semantic.DeepDerivative(newIngress.ObjectMeta.Labels, existingIngress.ObjectMeta.Labels) {
		log.Info("Updating Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)

		existingIngress.Spec = newIngress.Spec
		existingIngress.ObjectMeta.Labels = newIngress.ObjectMeta.Labels

		if err := r.Update(ctx, existingIngress); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// update prometheus metrics

	return ctrl.Result{}, nil
}

// reconcileIngresses will ensure that the required ingresses are created
func (r *UnleashReconciler) reconcileIngresses(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	res, err := r.reconcileIngress(ctx, unleash, &unleash.Spec.WebIngress, "web")
	if err != nil || res.Requeue {
		return res, err
	}

	res, err = r.reconcileIngress(ctx, unleash, &unleash.Spec.ApiIngress, "api")
	if err != nil || res.Requeue {
		return res, err
	}

	return ctrl.Result{}, nil
}

// reconcileSecrets will ensure that the required secrets are created
func (r *UnleashReconciler) reconcileSecrets(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Check if operator secret already exists, if not create a new one
	operatorSecret := &corev1.Secret{}
	err := r.Get(ctx, unleash.NamespacedOperatorSecretName(r.OperatorNamespace), operatorSecret)
	if err != nil && apierrors.IsNotFound(err) {
		adminKey, err := resources.GenerateAdminKey()
		if err != nil {
			return ctrl.Result{}, err
		}

		operatorSecret, err = resources.OperatorSecretForUnleash(unleash, r.Scheme, r.OperatorNamespace, adminKey)
		if err != nil {
			return ctrl.Result{}, err
		}
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
	log := log.FromContext(ctx)

	dep, err := resources.DeploymentForUnleash(unleash, r.Scheme)
	if err != nil {
		return ctrl.Result{}, err
	}

	found := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: unleash.Name, Namespace: unleash.Namespace}, found)
	if err != nil && apierrors.IsNotFound(err) {
		log.Info("Creating Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
		if err = r.Create(ctx, dep); err != nil {
			log.Error(err, "Failed to create Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)
			return ctrl.Result{}, err
		}

		return ctrl.Result{RequeueAfter: time.Minute}, nil
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
	}

	log.Info("Skip reconcile: Deployment already up to date", "Deployment.Namespace", found.Namespace, "Deployment.Name", found.Name)
	return ctrl.Result{}, nil
}

// reconcileService will ensure that the Service for the Unleash instance is created
func (r *UnleashReconciler) reconcileService(ctx context.Context, unleash *unleashv1.Unleash) (ctrl.Result, error) {
	log := log.FromContext(ctx)

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

// testConnection will test the connection to the Unleash instance
func (r *UnleashReconciler) testConnection(unleash UnleashInstance, ctx context.Context, log logr.Logger) (*unleashclient.InstanceAdminStatsResult, error) {
	client, err := unleash.ApiClient(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		log.Error(err, "Failed to set up client for Unleash")
		return nil, err
	}

	stats, res, err := client.GetInstanceAdminStats()

	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to connect to Unleash instance on %s", unleash.URL()))
		return nil, err
	}

	if res.StatusCode != http.StatusOK {
		log.Error(err, fmt.Sprintf("Unleash connection check failed with status code %d", res.StatusCode))
		return nil, err
	}

	log.Info("Successfully connected to Unleash instance", "statusCode", res.StatusCode, "version", stats.VersionOSS)
	return stats, nil
}

func (r *UnleashReconciler) updateStatusReconcileSuccess(ctx context.Context, unleash *unleashv1.Unleash) error {
	log := log.FromContext(ctx)

	log.Info("Successfully reconciled Unleash")
	return r.updateStatus(ctx, unleash, nil, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
}

func (r *UnleashReconciler) updateStatusReconcileFailed(ctx context.Context, unleash *unleashv1.Unleash, err error, message string) error {
	log := log.FromContext(ctx)

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
	log := log.FromContext(ctx)

	log.Info("Successfully connected to Unleash")
	return r.updateStatus(ctx, unleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash instance",
	})
}

func (r *UnleashReconciler) updateStatusConnectionFailed(ctx context.Context, unleash *unleashv1.Unleash, err error, message string) error {
	log := log.FromContext(ctx)

	log.Error(err, fmt.Sprintf("%s for Unleash", message))
	return r.updateStatus(ctx, unleash, nil, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *UnleashReconciler) updateStatus(ctx context.Context, unleash *unleashv1.Unleash, stats *unleashclient.InstanceAdminStatsResult, status metav1.Condition) error {
	log := log.FromContext(ctx)

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
	if err := r.Status().Update(ctx, unleash); err != nil {
		log.Error(err, "Failed to update status for Unleash")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *UnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.Unleash{}).
		Owns(&appsv1.Deployment{}).
		//WithOptions(controller.Options{MaxConcurrentReconciles: 2}).
		Complete(r)
}

// GetApiToken will return the API token for the Unleash instance
func GetApiToken(ctx context.Context, r client.Client, unleashName, operatorNamespace string) (string, error) {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: unleashName, Namespace: operatorNamespace}, secret)
	if err != nil {
		return "", err
	}

	return string(secret.Data[unleashv1.UnleashSecretTokenKey]), nil
}
