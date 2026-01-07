package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/config"
	"github.com/nais/unleasherator/internal/federation"
	"github.com/nais/unleasherator/internal/o11y"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/nais/unleasherator/internal/utils"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var (
	// RemoteUnleash controller timeouts - prefixed to avoid conflicts with other controllers
	remoteUnleashErrorRetryDelay = 1 * time.Minute
	remoteUnleashRequeueAfter    = 1 * time.Hour

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
	Timeout           config.TimeoutConfig
	Federation        RemoteUnleashFederation
	Tracer            trace.Tracer
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
	spanOpts := o11y.ReconcilerAttributes(ctx, req)
	ctx, span := r.Tracer.Start(ctx, "Reconcile RemoteUnleash", spanOpts...)
	defer span.End()

	log := log.FromContext(ctx).WithName("remoteunleash").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Starting reconciliation of RemoteUnleash")

	remoteUnleash := &unleashv1.RemoteUnleash{}
	err := r.Get(ctx, req.NamespacedName, remoteUnleash)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("RemoteUnleash resource not found. Ignoring since object must be deleted")
			remoteUnleashStatus.DeleteLabelValues(req.Namespace, req.Name, unleashv1.UnleashStatusConditionTypeReconciled)
			remoteUnleashStatus.DeleteLabelValues(req.Namespace, req.Name, unleashv1.UnleashStatusConditionTypeConnected)
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "Failed to get RemoteUnleash")
		return ctrl.Result{}, err
	}

	// Check if marked for deletion
	if remoteUnleash.GetDeletionTimestamp() != nil {
		log.Info("RemoteUnleash marked for deletion")
		if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for RemoteUnleash before deletion")

			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeDegraded,
				Status:  metav1.ConditionUnknown,
				Reason:  "Finalizing",
				Message: "Performing finalizer options",
			})

			if err := r.Status().Update(ctx, remoteUnleash); err != nil {
				log.Error(err, "Failed to update RemoteUnleash status")
				return ctrl.Result{}, err
			}

			r.doFinalizerOperationsForToken(remoteUnleash)

			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				log.Error(err, "Failed to get RemoteUnleash")
				return ctrl.Result{}, err
			}

			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeDegraded,
				Status:  metav1.ConditionTrue,
				Reason:  "Finalizing",
				Message: fmt.Sprintf("Finalizer operations for RemoteUnleash %s name were successfully accomplished", remoteUnleash.Name),
			})

			if err := r.Status().Update(ctx, remoteUnleash); err != nil {
				log.Error(err, "Failed to update Unleash status")
				return ctrl.Result{}, err
			}

			log.Info("Removing finalizer from RemoteUnleash after successfully perform the operations")
			if ok := controllerutil.RemoveFinalizer(remoteUnleash, tokenFinalizer); !ok {
				log.Error(err, "Failed to remove finalizer from RemoteUnleash")
				return ctrl.Result{Requeue: true}, err
			}

			if err = r.Update(ctx, remoteUnleash); err != nil {
				log.Error(err, "Failed to update RemoteUnleash to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{Requeue: false}, nil
	}

	// Set status to unknown if no status is set
	if len(remoteUnleash.Status.Conditions) == 0 {
		log.Info("Setting status to unknown for RemoteUnleash")

		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
			Type:    unleashv1.UnleashStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})

		if err = r.Status().Update(ctx, remoteUnleash); err != nil {
			log.Error(err, "Failed to update RemoteUnleash status")
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
			return ctrl.Result{}, err
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

	// Get admin token from RemoteUnleash secret
	adminToken, err := remoteUnleash.AdminToken(ctx, r.Client, r.OperatorNamespace)
	if err != nil {
		if err := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, "Failed to get admin token secret"); err != nil {
			return ctrl.Result{}, err
		}

		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: remoteUnleashErrorRetryDelay}, nil
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

	stats, _, err := unleashClient.GetInstanceAdminStats(ctx)
	if err != nil {
		if err := r.updateStatusConnectionFailed(ctx, remoteUnleash, stats, err, fmt.Sprintf("Failed to connect to Unleash instance statistics endpoint on host %s", remoteUnleash.URL())); err != nil {
			return ctrl.Result{}, err
		}

		// Requeue after 1 minute if we failed to connect to Unleash
		return ctrl.Result{}, err
	}

	// Set RemoteUnleash status to reconciled and connected in a single update
	err = r.updateStatusSuccess(ctx, stats, remoteUnleash)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: remoteUnleashRequeueAfter}, nil
}

func (r *RemoteUnleashReconciler) updateStatusSuccess(ctx context.Context, stats *unleashclient.InstanceAdminStatsResult, remoteUnleash *unleashv1.RemoteUnleash) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	log.Info("Successfully reconciled and connected to Unleash")

	// Get fresh copy before updating
	if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
		log.Error(err, "Failed to get RemoteUnleash")
		return err
	}

	// Set version from stats
	if stats != nil {
		if stats.VersionEnterprise != "" {
			remoteUnleash.Status.Version = stats.VersionEnterprise
		} else {
			remoteUnleash.Status.Version = stats.VersionOSS
		}
	}

	// Set both statuses
	remoteUnleash.Status.Reconciled = true
	remoteUnleash.Status.Connected = true

	// Update metrics
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(1)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(1)

	// Set both conditions
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash",
	})

	// Single status update
	if err := r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) updateStatusConnectionFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, err error, message string) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	log.Error(err, fmt.Sprintf("%s for Unleash", message))

	// Get fresh copy before updating
	if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
		log.Error(err, "Failed to get RemoteUnleash")
		return err
	}

	// Set version from stats if available
	if stats != nil {
		if stats.VersionEnterprise != "" {
			remoteUnleash.Status.Version = stats.VersionEnterprise
		} else {
			remoteUnleash.Status.Version = stats.VersionOSS
		}
	}

	// Reconciled succeeded (we got this far), but connection failed
	remoteUnleash.Status.Reconciled = true
	remoteUnleash.Status.Connected = false

	// Update metrics
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled).Set(1)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected).Set(0)

	// Set both conditions in single update
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Reconciled successfully",
	})
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})

	if err := r.Status().Update(ctx, remoteUnleash); err != nil {
		log.Error(err, "Failed to update status for RemoteUnleash")
		return err
	}

	return nil
}

func (r *RemoteUnleashReconciler) updateStatusReconcileFailed(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, err error, message string) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

	log.Error(err, fmt.Sprintf("%s for RemoteUnleash", message))
	return r.updateStatus(ctx, remoteUnleash, stats, metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  "Reconciling",
		Message: message,
	})
}

func (r *RemoteUnleashReconciler) updateStatus(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, stats *unleashclient.InstanceAdminStatsResult, status metav1.Condition) error {
	log := log.FromContext(ctx).WithName("remoteunleash")

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
	log := log.FromContext(ctx).WithName("subscribe")

	if !r.Federation.Enabled {
		log.Info("Federation is disabled, not consuming pubsub messages")
		return nil
	}

	var permanentError error

	for ctx.Err() == nil && permanentError == nil {
		log.Info("Waiting for pubsub messages")
		err := r.Federation.Subscriber.Subscribe(ctx, func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecret *corev1.Secret, clusters []string, status pb.Status) error {
			if len(remoteUnleashes) == 0 {
				log.Info("Received pubsub message with no namespaces, ignoring", "status", status, "clusters", clusters)
				return nil
			}

			log.Info("Received pubsub message", "status", status, "unleash", remoteUnleashes[0].GetName(), "clusters", clusters)

			if !utils.StringInSlice(r.Federation.ClusterName, clusters) {
				log.Info("Ignoring message, not for this cluster", "cluster", r.Federation.ClusterName, "clusters", clusters)
				return nil
			}

			switch status {
			case pb.Status_Removed:
				log.Info("Received Status_Removed, deleting RemoteUnleash resources and secret")

				// Delete RemoteUnleash resources
				objectsCtx, objectsCancel := r.Timeout.WriteContext(ctx)
				defer objectsCancel()

				if errs := utils.DeleteAllObjects(objectsCtx, r.Client, remoteUnleashes); len(errs) > 0 {
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues("removed", "failed").Inc()
						log.Error(err, "Failed to delete RemoteUnleash")

						if !retriableError(err) {
							permanentError = err
						}
					}
					if permanentError != nil {
						return permanentError
					}
					return errs[0]
				}

				// Delete the admin secret
				secretCtx, secretCancel := r.Timeout.WriteContext(ctx)
				defer secretCancel()

				if err := utils.DeleteObject(secretCtx, r.Client, adminSecret); err != nil {
					remoteUnleashReceived.WithLabelValues("removed", "failed").Inc()
					log.Error(err, "Failed to delete admin secret")

					if !retriableError(err) {
						permanentError = err
					}
					return err
				}

				remoteUnleashReceived.WithLabelValues("removed", "success").Inc()
				log.Info("Successfully deleted RemoteUnleash resources and secret")
				return nil

			case pb.Status_Provisioned:
				log.Info("Received Status_Provisioned")

				secretCtx, secretCancel := r.Timeout.WriteContext(ctx)
				defer secretCancel()

				if err := utils.UpsertObject(secretCtx, r.Client, adminSecret); err != nil {
					remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()

					if !retriableError(err) {
						permanentError = err
					}
					return err
				}

				objectsCtx, objectsCancel := r.Timeout.WriteContext(ctx)
				defer objectsCancel()

				if err := utils.UpsertAllObjects(objectsCtx, r.Client, remoteUnleashes); len(err) > 0 {
					for _, err := range err {
						remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()

						if namespaceNotFoundError(err) {
							log.Info(fmt.Sprintf("Namespace %s not found for RemoteUnleash %s", err.(*apierrors.StatusError).ErrStatus.Details.Name, remoteUnleashes[0].GetName()))
							continue
						} else {
							if !retriableError(err) {
								permanentError = err
							}
							return err
						}
					}
				}

				remoteUnleashReceived.WithLabelValues("provisioned", "success").Inc()
				return nil
			default:
				remoteUnleashReceived.WithLabelValues("unknown", "failed").Inc()
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

// namespaceNotFoundError returns true if the error is a namespace not found error.
func namespaceNotFoundError(err error) bool {
	var statusErr *apierrors.StatusError
	return errors.As(err, &statusErr) && statusErr.ErrStatus.Reason == metav1.StatusReasonNotFound && statusErr.ErrStatus.Details.Kind == "namespaces"
}

// SetupWithManager sets up the controller with the Manager.
func (r *RemoteUnleashReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pred := predicate.GenerationChangedPredicate{}
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.RemoteUnleash{}).
		WithEventFilter(pred).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Prevent race conditions with rapid simultaneous changes
		}).
		Complete(r)
}
