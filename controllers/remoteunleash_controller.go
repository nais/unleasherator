package controllers

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
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

	unleashv1 "github.com/nais/unleasherator/api/v1"
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
	}

	// Check if marked for deletion
	if remoteUnleash.GetDeletionTimestamp() != nil {
		if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for RemoteUnleash before deletion")

			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    typeDeletedToken,
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
				Type:    typeDeletedToken,
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

	// Get RemoteUnleash secret
	secret := &corev1.Secret{}
	err = r.Get(ctx, types.NamespacedName{
		Namespace: remoteUnleash.Namespace,
		Name:      remoteUnleash.Spec.SecretName,
	}, secret)
	if err != nil {
		log.Error(err, fmt.Sprintf("Failed to get Secret for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeDegradedUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to get Secret",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Check if secret contains token
	token, ok := secret.Data["token"]
	if !ok {
		log.Error(err, fmt.Sprintf("Failed to get token from Secret for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    typeDegradedUnleash,
			Status:  metav1.ConditionFalse,
			Reason:  "Reconciling",
			Message: "Failed to get token from Secret",
		})
		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, fmt.Sprintf("Failed to update status for RemoteUnleash %s in namespace %s", remoteUnleash.Name, remoteUnleash.Namespace))
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Check connection to Unleash
	// @TODO: Implement
	// if err := r.checkUnleashConnection(remoteUnleash, token); err != nil {
	// }

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
