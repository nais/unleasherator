package controllers

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/unleash"
)

// RemoteUnleashReconciler reconciles a RemoteUnleash object
type RemoteUnleashReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=RemoteUnleashs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=RemoteUnleashs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=RemoteUnleashs/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *RemoteUnleashReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	log.Info(fmt.Sprintf("Reconciling RemoteUnleash %s in namespace %s", req.Name, req.Namespace))

	remoteUnleash := &unleashv1.RemoteUnleash{}
	err := r.Get(ctx, req.NamespacedName, remoteUnleash)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("RemoteUnleash resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get RemoteUnleash")
		return ctrl.Result{}, err
	}

	// Set status to unknown if not set
	if remoteUnleash.Status.Conditions == nil || len(remoteUnleash.Status.Conditions) == 0 {
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeDegradedUnleash,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, "Failed to update RemoteUnleash status")
			return ctrl.Result{}, err
		}
	}

	// Refetch after status update
	if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
		log.Error(err, "Failed to get RemoteUnleash")
		return ctrl.Result{}, err
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
		log.Info("Adding finalizer to RemoteUnleash")
		if ok := controllerutil.AddFinalizer(remoteUnleash, tokenFinalizer); !ok {
			log.Error(err, "Failed to add finalizer to RemoteUnleash")
			return ctrl.Result{Requeue: true}, err
		}

		if err = r.Update(ctx, remoteUnleash); err != nil {
			log.Error(err, "Failed to update RemoteUnleash to add finalizer")
			return ctrl.Result{}, err
		}

		// Refetch after update
		if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
			log.Error(err, "Failed to get RemoteUnleash")
			return ctrl.Result{}, err
		}
	}

	// Check if marked for deletion
	if remoteUnleash.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for RemoteUnleash before deletion")

			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    typeDegradedUnleash,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer operations",
			})

			if err = r.Status().Update(ctx, remoteUnleash); err != nil {
				log.Error(err, "Failed to update RemoteUnleash status")
				return ctrl.Result{}, err
			}

			r.doFinalizerOperationsForToken(remoteUnleash)

			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				log.Error(err, "Failed to get RemoteUnleash")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    typeDegradedUnleash,
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: "Finalizer operations completed",
			})

			if err := r.Status().Update(ctx, remoteUnleash); err != nil {
				log.Error(err, "Failed to update RemoteUnleash status")
				return ctrl.Result{}, err
			}

			log.Info("Removing finalizer from RemoteUnleash")
			if ok := controllerutil.RemoveFinalizer(remoteUnleash, tokenFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer from RemoteUnleash")
				return ctrl.Result{Requeue: true}, err
			}

			if err = r.Update(ctx, remoteUnleash); err != nil {
				log.Error(err, "Failed to update RemoteUnleash to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Get admin token
	adminToken, err := remoteUnleash.GetAdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get Secret for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeAvailableUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to get admin secret",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Check admin token
	if len(adminToken) == 0 {
		log.Error(err, fmt.Sprintf("Failed to get token for Secret for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeAvailableUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to get admin token",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Create Unleash API client
	unleashClient, err := unleash.NewClient(remoteUnleash.Spec.Server.URL, string(adminToken))
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to create Unleash client for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeDegradedUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to create Unleash client",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Check Unleash connectivity
	health, res, err := unleashClient.GetHealth()
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get health for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeDegradedUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to get health",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	if res.StatusCode != 200 || health.Health != "GOOD" {
		log.Error(err, fmt.Sprintf("Unleash health check failed with status code %d (health: %s) for RemoteUnleash %s in namespace %s", res.StatusCode, health.Health, remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeDegradedUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: fmt.Sprintf("Unleash health check failed with status code %d (health: %s)", res.StatusCode, health.Health),
		})

		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	log.Info(fmt.Sprintf("RemoteUnleash %s in namespace %s is healthy", remoteUnleash.Name, remoteUnleash.Namespace))
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    typeAvailableUnleash,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Unleash is healthy",
	})

	if err = r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *RemoteUnleashReconciler) doFinalizerOperationsForToken(remoteUnleash *unleashv1.RemoteUnleash) {

}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteUnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.RemoteUnleash{}).
		Complete(r)
}
