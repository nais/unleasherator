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

	log.Info("Starting reconciliation of RemoteUnleash")

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
		if err := r.updateStatus(ctx, remoteUnleash, metav1.Condition{
			Type:    typeAvailableUnleash,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		}); err != nil {
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
			log.Error(err, "Failed to get RemoteUnleash")
			return ctrl.Result{}, err
		}
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

		if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
			log.Error(err, "Failed to get RemoteUnleash")
			return ctrl.Result{}, err
		}
	}

	// Check if marked for deletion
	if remoteUnleash.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for RemoteUnleash before deletion")

			if err := r.updateStatus(ctx, remoteUnleash, metav1.Condition{
				Type:    typeDegradedUnleash,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer operations",
			}); err != nil {
				return ctrl.Result{}, err
			}

			r.doFinalizerOperationsForToken(remoteUnleash)

			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				log.Error(err, "Failed to get RemoteUnleash")
				return ctrl.Result{}, err
			}

			if err := r.updateStatus(ctx, remoteUnleash, metav1.Condition{
				Type:    typeDegradedUnleash,
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: "Finalizer operations completed",
			}); err != nil {
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

	// Get admin token from secret
	adminToken, err := remoteUnleash.GetAdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, err, "Failed to get admin token secret"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Check admin token
	if len(adminToken) == 0 {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, err, "Admin token is empty"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Create Unleash API client
	unleashClient, err := unleash.NewClient(remoteUnleash.Spec.Server.URL, string(adminToken))
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, err, "Failed to create Unleash client"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	if err = r.updateStatusReconcileSuccess(ctx, remoteUnleash); err != nil {
		return ctrl.Result{}, err
	}

	// Check Unleash connectivity
	health, res, err := unleashClient.GetHealth()
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, remoteUnleash, err, "Failed to connect to Unleash instance health endpoint"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, err
	}

	if res.StatusCode != 200 || health.Health != "GOOD" {
		err := r.updateStatusConnectionFailed(ctx, remoteUnleash, err, fmt.Sprintf("Unleash health check failed with status code %d (health: %s)", res.StatusCode, health.Health))
		return ctrl.Result{}, err
	}

	if err := r.updateStatusConnectionSuccess(ctx, remoteUnleash); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *RemoteUnleashReconciler) updateStatusConnectionSuccess(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash) error {
	log := log.FromContext(ctx)

	log.Info("Successfully connected to Unleash")
	return r.updateStatus(ctx, remoteUnleash, metav1.Condition{
		Type:    typeConnectionUnleash,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash",
	})
}

func (r *RemoteUnleashReconciler) updateStatusConnectionFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, err error, message string) error {
	log := log.FromContext(ctx)

	log.Error(err, fmt.Sprintf("%s for Unleash", message))
	return r.updateStatus(ctx, remoteUnleash, metav1.Condition{
		Type:    typeConnectionUnleash,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *RemoteUnleashReconciler) updateStatusReconcileSuccess(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash) error {
	log := log.FromContext(ctx)

	log.Info("Successfully reconciled RemoteUnleash")
	return r.updateStatus(ctx, remoteUnleash, metav1.Condition{
		Type:    typeAvailableUnleash,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
}

func (r *RemoteUnleashReconciler) updateStatusReconcileFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, err error, message string) error {
	log := log.FromContext(ctx)

	log.Error(err, fmt.Sprintf("%s for RemoteUnleash", message))
	return r.updateStatus(ctx, remoteUnleash, metav1.Condition{
		Type:    typeAvailableUnleash,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *RemoteUnleashReconciler) updateStatus(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, status metav1.Condition) error {
	log := log.FromContext(ctx)

	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, status)
	if err := r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) doFinalizerOperationsForToken(remoteUnleash *unleashv1.RemoteUnleash) {

}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteUnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.RemoteUnleash{}).
		Complete(r)
}
