package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/federation"
	"github.com/nais/unleasherator/pkg/pb"
	"github.com/nais/unleasherator/pkg/unleashclient"
	"github.com/nais/unleasherator/pkg/utils"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var (
	// remoteUnleashStatus is a Prometheus metric which will be used to expose the status of the RemoteUnleash instances
	remoteUnleashStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_remoteunleash_status",
			Help: "Status of RemoteUnleash instances",
		},
		[]string{"namespace", "name", "status"},
	)

	remoteUnleashReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_federation_received_total",
			Help: "Number of Unleash federation messages received with status",
		},
		[]string{"state", "status"},
	)
)

func init() {
	metrics.Registry.MustRegister(remoteUnleashStatus, remoteUnleashReceived)
}

// RemoteUnleashReconciler reconciles a RemoteUnleash object
type RemoteUnleashReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	OperatorNamespace string
	Federation        RemoteUnleashFederation
}

type RemoteUnleashFederation struct {
	Enabled     bool
	ClusterName string
	Subscriber  federation.Subscriber
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes/finalizers,verbs=update
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
		if err := r.updateStatus(ctx, remoteUnleash, nil, metav1.Condition{
			Type:    unleashv1.UnleashStatusConditionTypeReconciled,
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

			if err := r.updateStatus(ctx, remoteUnleash, nil, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeDegraded,
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

			if err := r.updateStatus(ctx, remoteUnleash, nil, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeDegraded,
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
	adminToken, err := remoteUnleash.AdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Failed to get admin token secret"); err != nil {
			return ctrl.Result{}, err
		}

		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil
		} else {
			return ctrl.Result{}, err
		}
	}

	// Check admin token
	if len(adminToken) == 0 {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Admin token is empty"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	// Create Unleash API client
	unleashClient, err := unleashclient.NewClient(remoteUnleash.Spec.Server.URL, string(adminToken))
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Failed to create Unleash client"); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, err
	}

	if err = r.updateStatusReconcileSuccess(ctx, remoteUnleash, nil); err != nil {
		return ctrl.Result{}, err
	}

	stats, _, err := unleashClient.GetInstanceAdminStats()
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, remoteUnleash, stats, err, fmt.Sprintf("Failed to connect to Unleash instance statistics endpoint on host %s", remoteUnleash.URL())); err != nil {
			return ctrl.Result{}, err
		}

		// Requeue after 1 minute if we failed to connect to Unleash
		return ctrl.Result{}, err
	}

	// Set RemoteUnleash status to connected
	err = r.updateStatusConnectionSuccess(ctx, stats, remoteUnleash)
	return ctrl.Result{RequeueAfter: 1 * time.Hour}, err
}

func (r *RemoteUnleashReconciler) updateStatusConnectionSuccess(ctx context.Context, stats *unleashclient.InstanceAdminStatsResult, remoteUnleash *unleashv1.RemoteUnleash) error {
	log := log.FromContext(ctx)

	log.Info("Successfully connected to Unleash")
	return r.updateStatus(ctx, remoteUnleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash",
	})
}

func (r *RemoteUnleashReconciler) updateStatusConnectionFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, err error, message string) error {
	log := log.FromContext(ctx)

	log.Error(err, fmt.Sprintf("%s for Unleash", message))
	return r.updateStatus(ctx, remoteUnleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *RemoteUnleashReconciler) updateStatusReconcileSuccess(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult) error {
	log := log.FromContext(ctx)

	log.Info("Successfully reconciled RemoteUnleash")
	return r.updateStatus(ctx, remoteUnleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
}

func (r *RemoteUnleashReconciler) updateStatusReconcileFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, err error, message string) error {
	log := log.FromContext(ctx)

	log.Error(err, fmt.Sprintf("%s for RemoteUnleash", message))
	return r.updateStatus(ctx, remoteUnleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *RemoteUnleashReconciler) updateStatus(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, status metav1.Condition) error {
	log := log.FromContext(ctx)

	if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
		log.Error(err, "Failed to get RemoteUnleash")
		return err
	}

	if stats != nil {
		if stats.VersionEnterprise != "" {
			remoteUnleash.Status.Version = stats.VersionEnterprise
		} else {
			remoteUnleash.Status.Version = stats.VersionOSS
		}
	}

	switch status.Type {
	case unleashv1.UnleashStatusConditionTypeReconciled:
		remoteUnleash.Status.Reconciled = status.Status == metav1.ConditionTrue
	case unleashv1.UnleashStatusConditionTypeConnected:
		remoteUnleash.Status.Connected = status.Status == metav1.ConditionTrue
	}

	val := promGaugeValueForStatus(status.Status)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, status.Type).Set(val)

	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, status)
	if err := r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) doFinalizerOperationsForToken(remoteUnleash *unleashv1.RemoteUnleash) {

}

func (r *RemoteUnleashReconciler) FederationSubscribe(ctx context.Context) error {
	log := log.FromContext(ctx).WithName("FederationSubscribe")

	if !r.Federation.Enabled {
		log.Info("Federation is disabled, not consuming pubsub messages")
		return nil
	}

	var permanentError error

	for ctx.Err() == nil && permanentError == nil {
		log.Info("Waiting for pubsub messages")
		err := r.Federation.Subscriber.Subscribe(ctx, func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecret *corev1.Secret, clusters []string, status pb.Status) error {
			log.Info("Received pubsub message", "status", status)

			if !hasValue(r.Federation.ClusterName, clusters) {
				log.Info("Ignoring message, not for this cluster", "cluster", r.Federation.ClusterName, "clusters", clusters)
				return nil
			}

			switch status {
			case pb.Status_Removed:
				log.Info("Received Status_Removed, not implemented yet")
				remoteUnleashReceived.WithLabelValues("removed", "error").Inc()
				return nil

			case pb.Status_Provisioned:
				log.Info("Received Status_Provisioned")

				const kubernetesWriteTimeout = time.Second * 5
				timeoutContext, cancel := context.WithTimeout(ctx, kubernetesWriteTimeout)
				defer cancel()

				adminSecretErr := utils.UpsertObject(timeoutContext, r.Client, adminSecret)
				remoteUnleashesErr := utils.UpsertAllObjects(timeoutContext, r.Client, remoteUnleashes)

				err := errors.Join(adminSecretErr, remoteUnleashesErr)
				if err != nil {
					remoteUnleashReceived.WithLabelValues("provisioned", "error").Inc()

					if !retriableError(err) {
						permanentError = err
					}
					return err
				}

				remoteUnleashReceived.WithLabelValues("provisioned", "success").Inc()
				return nil
			default:
				remoteUnleashReceived.WithLabelValues("unknown", "error").Inc()
				log.Error(fmt.Errorf("unknown status: %s", status), "Received unknown status")
				return nil
			}
		})

		if err != nil {
			return err
		}
	}

	return permanentError
}

// retriableError returns true if the error is not a forbidden or unauthorized error.
func retriableError(err error) bool {
	return !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err)
}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteUnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.GenerationChangedPredicate{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.RemoteUnleash{}).
		WithEventFilter(pred).
		Complete(r)
}
