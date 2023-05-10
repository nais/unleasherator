package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

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
)

const unleashFinalizer = "unleash.nais.io/finalizer"

const (
	// typeAvailableUnleash represents the status of the Deployment reconciliation
	typeAvailableUnleash = "Available"

	// typeConnectionUnleash represents the status of the connection test to Unleash
	typeConnectionUnleash = "Connection"

	// typeDegradedUnleash represents the status used when the Unleash is deleted and the finalizer operations are must to occur.
	typeDegradedUnleash = "Degraded"
)

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
		meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeAvailableUnleash, Status: metav1.ConditionUnknown, Reason: "Reconciling", Message: "Starting reconciliation"})
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
				Type:    typeDegradedUnleash,
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

			meta.SetStatusCondition(&unleash.Status.Conditions, metav1.Condition{Type: typeDegradedUnleash,
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
	err = r.testConnection(unleash, ctx, log)
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, unleash, err, "Failed to connect to Unleash instance"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Set the connection status of the Unleash instance to available
	if err = r.updateStatusConnectionSuccess(ctx, unleash); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// finalizeUnleash will perform the required operations before delete the CR.
func (r *UnleashReconciler) doFinalizerOperationsForUnleash(cr *unleashv1.Unleash, ctx context.Context, log logr.Logger) {
	// TODO(user): Add the cleanup steps that the operator
	// needs to do before the CR can be deleted. Examples
	// of finalizers include performing backups and deleting
	// resources that are not owned by this CR, like a PVC.

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

	// Note: It is not recommended to use finalizers with the purpose of delete resources which are
	// created and managed in the reconciliation. These ones, such as the Deployment created on this reconcile,
	// are defined as depended of the Unleash. See that we use the method ctrl.SetControllerReference.
	// to set the ownerRef which means that the Deployment will be deleted by the Kubernetes API.
	// More info: https://kubernetes.io/docs/tasks/administer-cluster/use-cascading-deletion/

	// The following implementation will raise an event
	r.Recorder.Event(cr, "Warning", "Deleting",
		fmt.Sprintf("Unleash %s is being deleted from the namespace %s",
			cr.Name,
			cr.Namespace))
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

	// If the netpol is not enabled, we delete it if it exists.
	if !unleash.Spec.NetworkPolicy.Enabled {
		if err == nil {
			log.Info("Deleting NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
			err = r.Delete(ctx, existingNetPol)
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
		return ctrl.Result{}, nil
	}

	// If the netpol is enabled and does not exist, we create it.
	if err != nil && apierrors.IsNotFound(err) {
		log.Info("Creating NetworkPolicy", "NetworkPolicy.Namespace", newNetPol.Namespace, "NetworkPolicy.Name", newNetPol.Name)
		err = r.Create(ctx, newNetPol)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// If the netpol is enabled and exists, we update it if it is not up to date.
	if !equality.Semantic.DeepDerivative(newNetPol.Spec, existingNetPol.Spec) || !equality.Semantic.DeepDerivative(newNetPol.Labels, existingNetPol.Labels) {
		log.Info("Updating NetworkPolicy", "NetworkPolicy.Namespace", existingNetPol.Namespace, "NetworkPolicy.Name", existingNetPol.Name)
		existingNetPol.Spec = newNetPol.Spec
		existingNetPol.Labels = newNetPol.Labels
		err = r.Update(ctx, existingNetPol)
		if err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// reconcileIngress will ensure that the required ingress is created
func (r *UnleashReconciler) reconcileIngress(ctx context.Context, unleash *unleashv1.Unleash, ingress *unleashv1.IngressConfig, nameSuffix string) (ctrl.Result, error) {
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
		if err == nil {
			log.Info("Deleting Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
			err = r.Delete(ctx, existingIngress)
			if err != nil {
				log.Error(err, "Failed to delete Ingress for Unleash", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
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
	if !equality.Semantic.DeepDerivative(newIngress.Spec, existingIngress.Spec) || !equality.Semantic.DeepDerivative(newIngress.Labels, existingIngress.Labels) {
		log.Info("Updating Ingress", "Ingress.Namespace", existingIngress.Namespace, "Ingress.Name", existingIngress.Name)
		existingIngress.Spec = newIngress.Spec
		existingIngress.Labels = newIngress.Labels
		err = r.Update(ctx, existingIngress)
		if err != nil {
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
		adminKey := resources.GenerateAdminKey()
		operatorSecret, err = resources.SecretForUnleash(unleash, r.Scheme, unleash.GetOperatorSecretName(), r.OperatorNamespace, adminKey, false)
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
		instanceSecret, err = resources.SecretForUnleash(unleash, r.Scheme, unleash.GetInstanceSecretName(), unleash.Namespace, adminKey, true)
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
	} else if !equality.Semantic.DeepDerivative(dep.Spec, found.Spec) {
		log.Info("Updating Deployment", "Deployment.Namespace", dep.Namespace, "Deployment.Name", dep.Name)

		found.Spec = dep.Spec
		if err = r.Update(ctx, found); err != nil {
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
	} else if !equality.Semantic.DeepDerivative(newSvc.Spec, existingSvc.Spec) {
		existingSvc.Spec = newSvc.Spec
		log.Info("Updating Service", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
		err = r.Update(ctx, existingSvc)
		if err != nil {
			log.Error(err, "Failed to update Service", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
			return ctrl.Result{}, err
		}
	}

	log.Info("Skip reconcile: Service up to date", "Service.Namespace", existingSvc.Namespace, "Service.Name", existingSvc.Name)
	return ctrl.Result{}, nil
}

// testConnection will test the connection to the Unleash instance
func (r *UnleashReconciler) testConnection(unleash *unleashv1.Unleash, ctx context.Context, log logr.Logger) error {
	client, err := unleash.GetApiClient(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		log.Error(err, "Failed to set up client for Unleash")
		return err
	}

	health, res, err := client.GetHealth()

	if err != nil {
		log.Error(err, "Failed to connect to Unleash instance health endpoint")
		return err
	}

	if res.StatusCode != http.StatusOK {
		log.Error(err, fmt.Sprintf("Unleash health check failed with status code %d (health: %s)", res.StatusCode, health.Health))
		return err
	}

	log.Info("Successfully connected to Unleash instance", "statusCode", res.StatusCode, "health", health)
	return nil
}

func (r *UnleashReconciler) updateStatusReconcileSuccess(ctx context.Context, unleash *unleashv1.Unleash) error {
	log := log.FromContext(ctx)

	log.Info("Successfully reconciled Unleash")
	return r.updateStatus(ctx, unleash, metav1.Condition{
		Type:    typeAvailableUnleash,
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
	return r.updateStatus(ctx, unleash, metav1.Condition{
		Type:    typeAvailableUnleash,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *UnleashReconciler) updateStatusConnectionSuccess(ctx context.Context, unleash *unleashv1.Unleash) error {
	log := log.FromContext(ctx)

	log.Info("Successfully connected to Unleash")
	return r.updateStatus(ctx, unleash, metav1.Condition{
		Type:    typeConnectionUnleash,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash instance",
	})
}

func (r *UnleashReconciler) updateStatusConnectionFailed(ctx context.Context, unleash *unleashv1.Unleash, err error, message string) error {
	log := log.FromContext(ctx)

	log.Error(err, fmt.Sprintf("%s for Unleash", message))
	return r.updateStatus(ctx, unleash, metav1.Condition{
		Type:    typeConnectionUnleash,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r UnleashReconciler) updateStatus(ctx context.Context, unleash *unleashv1.Unleash, status metav1.Condition) error {
	log := log.FromContext(ctx)

	val := 0.0
	if status.Status == metav1.ConditionTrue {
		val = 1.0
	}

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
