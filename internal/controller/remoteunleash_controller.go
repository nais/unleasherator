package controller

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
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
	"k8s.io/client-go/util/retry"
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
	remoteUnleashRequeueJitter   = 10 * time.Minute // Jitter to spread reconciliations

	// remoteUnleashStatus is a Prometheus metric which will be used to expose the status of the RemoteUnleash instances
	remoteUnleashStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_remoteunleash_status",
			Help: "Status of RemoteUnleash instances",
		},
		[]string{"resource_namespace", "name", "status"},
	)

	remoteUnleashReceived = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_federation_received_total",
			Help: "Number of Unleash federation messages received with status",
		},
		[]string{"state", "status"},
	)

	federationReceiveConsecutiveErrors = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "unleasherator_federation_receive_consecutive_errors",
			Help: "Consecutive Pub/Sub receive failures; non-zero sustained values indicate subscription instability",
		},
	)
)

const (
	federationReceiveBackoffBase         = 1 * time.Second
	federationReceiveBackoffMax          = 5 * time.Minute
	federationReceiveEscalationThreshold = 10

	// federationStateRemoved labels counters for a deletion the management
	// cluster published as such.
	federationStateRemoved = "removed"
	// federationStateDeprovisioned labels counters for a deletion this cluster
	// derived from a provisioning message that no longer names it. The two are
	// counted apart because they answer different questions: one says an
	// instance is gone, the other says it moved.
	federationStateDeprovisioned = "deprovisioned"
)

// clusterListMatch is the outcome of comparing this operator's cluster name
// against the cluster list on a federation message. It is an enum rather than a
// bool because "the list does not name me" and "the list says nothing useful"
// must lead to different actions: only the first may delete.
type clusterListMatch int

const (
	// clusterListNoStatement means the message asserts nothing about placement,
	// either because it carries no clusters at all or because every entry is
	// blank once trimmed.
	clusterListNoStatement clusterListMatch = iota
	// clusterListNamed means an entry names this cluster.
	clusterListNamed
	// clusterListCaseMismatch means an entry matches only when case is ignored.
	clusterListCaseMismatch
	// clusterListExcluded means the list names other clusters, and none of them
	// is this one under any casing.
	clusterListExcluded
)

// matchClusterList compares clusterName against the cluster list carried by a
// federation message.
//
// Entries are trimmed before comparison, and entries that are empty once
// trimmed are ignored: a stray space in a team's federation config is a typo,
// not a different cluster, and a list of nothing but blanks says as little as
// an empty list. Without this, `[" cluster-a"]` or `[""]` reads as "this
// instance does not belong in cluster-a" and deletes it.
//
// The match itself stays case-sensitive: cluster names are exact identifiers
// here (they name a real cluster in the fleet), and folding them would let two
// distinct configured names collide. But a case-only difference is reported as
// its own outcome instead of as an exclusion, because the caller's response to
// an exclusion is destructive and "cluster-a" versus "Cluster-A" is far more
// likely a naming mistake than a statement that the instance must go.
func matchClusterList(clusterName string, clusters []string) clusterListMatch {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		// An operator that does not know its own name cannot conclude anything
		// about placement. Callers reject this earlier and with a counter; this
		// is here so no future caller can turn it into a deletion by accident.
		return clusterListNoStatement
	}

	match := clusterListNoStatement
	for _, cluster := range clusters {
		cluster = strings.TrimSpace(cluster)
		switch {
		case cluster == "":
			continue
		case cluster == clusterName:
			return clusterListNamed
		case strings.EqualFold(cluster, clusterName):
			match = clusterListCaseMismatch
		case match == clusterListNoStatement:
			match = clusterListExcluded
		}
	}

	return match
}

func init() {
	metrics.Registry.MustRegister(remoteUnleashStatus, remoteUnleashReceived, federationReceiveConsecutiveErrors)
}

// RemoteUnleashReconciler reconciles a RemoteUnleash object
type RemoteUnleashReconciler struct {
	client.Client
	APIReader                   client.Reader
	Scheme                      *runtime.Scheme
	Recorder                    record.EventRecorder
	OperatorNamespace           string
	Timeout                     config.TimeoutConfig
	Federation                  RemoteUnleashFederation
	AllowLegacyNameBoundSecrets bool
	// NamespaceBoundSecrets enables reconcile-driven migration of legacy
	// admin secrets to the namespace-bound layout when combined with
	// AllowLegacyNameBoundSecrets.
	NamespaceBoundSecrets bool
	Tracer                trace.Tracer
}

type RemoteUnleashFederation struct {
	Enabled     bool
	ClusterName string
	Subscriber  federation.Subscriber
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=remoteunleashes/finalizers,verbs=update
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;create;update;patch;delete

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

	// Check if marked for deletion - handle this early to allow deletion even if Unleash server is down
	if remoteUnleash.GetDeletionTimestamp() != nil {
		log.Info("RemoteUnleash marked for deletion")
		if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
			log.Info("Performing Finalizer Operations for RemoteUnleash before deletion")

			// Try to update status, but don't block deletion if it fails
			_ = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
					return err
				}
				meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
					Type:    unleashv1.UnleashStatusConditionTypeDegraded,
					Status:  metav1.ConditionUnknown,
					Reason:  "Finalizing",
					Message: "Performing finalizer operations",
				})
				return r.Status().Update(ctx, remoteUnleash)
			})

			// Perform finalizer operations - currently a no-op but allows for future cleanup
			r.doFinalizerOperationsForToken(remoteUnleash)

			// Remove the finalizer to allow deletion to proceed
			log.Info("Removing finalizer from RemoteUnleash")
			err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
				if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
					return err
				}
				if !controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
					return nil // Already removed
				}
				controllerutil.RemoveFinalizer(remoteUnleash, tokenFinalizer)
				return r.Update(ctx, remoteUnleash)
			})
			if err != nil {
				log.Error(err, "Failed to update RemoteUnleash to remove finalizer")
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{Requeue: false}, nil
	}

	// Set status to unknown if no status is set
	if len(remoteUnleash.Status.Conditions) == 0 {
		log.Info("Setting status to unknown for RemoteUnleash")

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				return err
			}
			if len(remoteUnleash.Status.Conditions) > 0 {
				return nil // Already has conditions
			}
			meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  metav1.ConditionUnknown,
				Reason:  "Reconciling",
				Message: "Starting reconciliation",
			})
			return r.Status().Update(ctx, remoteUnleash)
		})
		if err != nil {
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

		err = retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
				return err
			}
			if controllerutil.ContainsFinalizer(remoteUnleash, tokenFinalizer) {
				return nil // Already has finalizer
			}
			controllerutil.AddFinalizer(remoteUnleash, tokenFinalizer)
			return r.Update(ctx, remoteUnleash)
		})
		if err != nil {
			log.Error(err, "Failed to update RemoteUnleash to add finalizer")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, remoteUnleash); err != nil {
			log.Error(err, "Failed to get RemoteUnleash")
			return ctrl.Result{}, err
		}
	}

	unleashClient, err := validatedRemoteUnleashAPIClient(
		ctx,
		r.Client,
		remoteUnleash,
		r.OperatorNamespace,
		r.AllowLegacyNameBoundSecrets,
	)
	if err != nil {
		message := "Failed to create Unleash client"
		switch {
		case apierrors.IsNotFound(err):
			message = "Failed to get admin token secret"
		case errors.Is(err, errRemoteUnleashAuthorization):
			message = "Validation failed"
		case errors.Is(err, errRemoteUnleashServerURL):
			message = "Server URL validation failed"
		case errors.Is(err, errRemoteUnleashEmptyToken):
			message = "Admin token is empty"
		}
		if updateErr := r.updateStatusReconcileFailed(ctx, remoteUnleash, nil, err, message); updateErr != nil {
			return ctrl.Result{}, updateErr
		}
		return remoteUnleashClientErrorResult(err)
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

	// The credential is verified by the stats call above; this is the safe
	// point to move a legacy admin secret to the namespace-bound layout.
	if r.NamespaceBoundSecrets && r.AllowLegacyNameBoundSecrets {
		migrated, err := r.migrateLegacyAdminSecret(ctx, remoteUnleash, log)
		if err != nil {
			log.Error(err, "Failed to migrate legacy admin secret")
			return ctrl.Result{}, err
		}
		if migrated {
			// The spec change triggers the next reconcile via the generation
			// predicate; the jittered requeue is belt-and-braces in case a
			// future change makes the update a no-op.
			return ctrl.Result{RequeueAfter: utils.RequeueAfterWithJitter(remoteUnleashRequeueAfter, remoteUnleashRequeueJitter)}, nil
		}
	}

	return ctrl.Result{RequeueAfter: utils.RequeueAfterWithJitter(remoteUnleashRequeueAfter, remoteUnleashRequeueJitter)}, nil
}

func remoteUnleashClientErrorResult(err error) (ctrl.Result, error) {
	switch {
	case apierrors.IsNotFound(err):
		return ctrl.Result{RequeueAfter: remoteUnleashErrorRetryDelay}, nil
	case errors.Is(err, errRemoteUnleashAuthorization),
		errors.Is(err, errRemoteUnleashServerURL),
		errors.Is(err, errRemoteUnleashEmptyToken):
		return ctrl.Result{}, nil
	default:
		return ctrl.Result{}, err
	}
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
		Type:               unleashv1.UnleashStatusConditionTypeReconciled,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: remoteUnleash.Generation,
		Reason:             "Reconciling",
		Message:            "Reconciled successfully",
	})
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:               unleashv1.UnleashStatusConditionTypeConnected,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: remoteUnleash.Generation,
		Reason:             "Reconciling",
		Message:            "Successfully connected to Unleash",
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
		Type:               unleashv1.UnleashStatusConditionTypeReconciled,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: remoteUnleash.Generation,
		Reason:             "Reconciling",
		Message:            "Reconciled successfully",
	})
	meta.SetStatusCondition(&remoteUnleash.Status.Conditions, metav1.Condition{
		Type:               unleashv1.UnleashStatusConditionTypeConnected,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: remoteUnleash.Generation,
		Reason:             "Reconciling",
		Message:            message,
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

	val := promGaugeValueForStatus(status.Status)
	remoteUnleashStatus.WithLabelValues(remoteUnleash.Namespace, remoteUnleash.Name, status.Type).Set(val)

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
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

		status.ObservedGeneration = remoteUnleash.Generation
		meta.SetStatusCondition(&remoteUnleash.Status.Conditions, status)
		return r.Status().Update(ctx, remoteUnleash)
	})

	if err != nil {
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

	backoff := federationReceiveBackoffBase
	consecutiveErrors := 0

	for ctx.Err() == nil {
		log.Info("Waiting for pubsub messages")

		// A permanent handler error cancels the subscription with the error as
		// cause, so concurrent callbacks cannot race on shared state and the
		// receive loop exits deterministically.
		subCtx, cancel := context.WithCancelCause(ctx)

		// Subscribe returns when the subscription context is cancelled. A
		// permanent handler error is recorded as the cancel cause; anything
		// else from Receive is a transient subscription failure to retry.
		started := time.Now()
		// A run that stays up past the backoff cap counts as recovered; clear
		// the gauge while healthy so alerts do not fire on a stale blip.
		healthy := time.AfterFunc(federationReceiveBackoffMax, func() {
			federationReceiveConsecutiveErrors.Set(0)
		})
		err := r.Federation.Subscriber.Subscribe(subCtx, func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecrets []*corev1.Secret, clusters []string, status pb.Status, publishTime time.Time) error {
			// failPermanently stops receiving so the operator restarts into a
			// corrected configuration, but nacks the message: operator-side
			// failures (e.g. RBAC) are recoverable, and the message must be
			// redelivered once the operator is healthy again.
			failPermanently := func(err error) error {
				cancel(err)
				return err
			}
			if len(remoteUnleashes) == 0 {
				log.Info("Received pubsub message with no namespaces, ignoring", "status", status, "clusters", clusters)
				return nil
			}
			if len(remoteUnleashes) != len(adminSecrets) {
				// Malformed payload can never be processed; drop it as poison
				// without stopping the subscription.
				return federation.Permanent(fmt.Errorf(
					"federation payload produced %d RemoteUnleash resources and %d admin secrets",
					len(remoteUnleashes),
					len(adminSecrets),
				))
			}

			log.Info("Received pubsub message", "status", status, "unleash", remoteUnleashes[0].GetName(), "clusters", clusters)

			// A federation message states where the instance should exist; it is
			// not an instruction addressed to the clusters it happens to name.
			// Acting only when named is what orphans resources: a cluster taken
			// out of a team's federation config, or one dropped from the list
			// before the instance was deleted, keeps a RemoteUnleash pointing at
			// a server it can no longer reach and alerts on it forever.
			//
			// So removals apply everywhere, and a provisioning message that no
			// longer names this cluster is treated as a removal here. A cluster
			// holding no matching resource does nothing either way, and the URL
			// and credential checks below still refuse anything that disagrees
			// with what is stored.
			//
			// Rewriting a message into a deletion is only ever done from an
			// explicit Status_Provisioned. Every other status, including the
			// proto3 zero value Status_Unknown that an absent field
			// deserialises to, keeps the old no-op behaviour: a message we
			// cannot read must not be the most destructive one we handle.
			effective := status
			// Counters in the shared removal body are labelled with the reason
			// the removal happened, so a deletion this cluster derived from a
			// dropped cluster list stays distinguishable from one the
			// management cluster actually published.
			removalState := federationStateRemoved

			if status == pb.Status_Provisioned {
				ownCluster := strings.TrimSpace(r.Federation.ClusterName)
				if ownCluster == "" {
					// Config.Validate refuses to start without CLUSTER_NAME, but
					// a single check is a poor guard for a fleet-wide deletion:
					// an operator that does not know its own name reads every
					// message as "not for me" and would empty the cluster.
					remoteUnleashReceived.WithLabelValues(strings.ToLower(status.String()), "cluster_name_unset").Inc()
					log.Info("Operator has no cluster name; refusing to act on placement", "clusters", clusters)
					return nil
				}

				switch matchClusterList(ownCluster, clusters) {
				case clusterListNamed:
					// The instance belongs here; provision it below.
				case clusterListNoStatement:
					// Asserts nothing about placement. Treating it as "remove
					// everywhere" would turn a malformed publish into fleet-wide
					// deletion, so ignore it instead.
					remoteUnleashReceived.WithLabelValues(strings.ToLower(status.String()), "no_clusters").Inc()
					log.Info("Message names no clusters; ignoring", "cluster", ownCluster)
					return nil
				case clusterListCaseMismatch:
					// Ambiguous: the list plausibly means this cluster and only
					// the casing differs. Deleting on a guess is the one reading
					// that cannot be undone, so do nothing and leave a counter
					// for whoever has to explain the naming mismatch.
					remoteUnleashReceived.WithLabelValues(strings.ToLower(status.String()), "cluster_name_case_mismatch").Inc()
					log.Info("Cluster list matches this cluster only when case is ignored; ignoring",
						"cluster", ownCluster, "clusters", clusters)
					return nil
				case clusterListExcluded:
					effective = pb.Status_Removed
					removalState = federationStateDeprovisioned
					log.Info("Instance is no longer federated to this cluster; removing local resources",
						"cluster", ownCluster, "clusters", clusters)
				}
			}

			switch effective {
			case pb.Status_Removed:
				log.Info("Received Status_Removed, deleting RemoteUnleash resources and secret")

				// Filter safe objects to prevent cross-namespace deletion hijacking
				var safeSecrets []*corev1.Secret
				var safeRUs []*unleashv1.RemoteUnleash

				for i, ru := range remoteUnleashes {
					existingRU := &unleashv1.RemoteUnleash{}
					err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(ru), existingRU)
					if apierrors.IsNotFound(err) {
						continue
					}
					if err != nil {
						if !retriableError(err) {
							return failPermanently(err)
						}
						return err
					}

					// Replay protection. A redelivered older message carries an
					// older cluster list, and acting on it deletes what a newer
					// message legitimately created. Nothing would bring it back:
					// the publisher skips republishing while its instance hash is
					// unchanged, and there is no periodic federation resync, so
					// recovery needs a human.
					//
					// A resource with no recorded time is not treated as newer
					// than everything; it is treated as unknown, and unknown
					// permits the deletion. Refusing instead would make every
					// resource predating this annotation undeletable by
					// federation, which is the orphan bug this path exists to
					// fix. The gap closes by itself: the first provisioning
					// message applied to a resource stamps it, and a transport
					// that supplies no publish time never stamps anything, so it
					// keeps working exactly as before rather than freezing.
					if lastApplied := federationLastAppliedPublishTime(existingRU); !lastApplied.IsZero() &&
						!publishTime.After(lastApplied) {
						remoteUnleashReceived.WithLabelValues(removalState, "stale").Inc()
						log.Info("Refusing to delete RemoteUnleash from a message older than the one already applied",
							"name", ru.Name, "namespace", ru.Namespace,
							"publishTime", publishTime, "lastApplied", lastApplied)
						continue
					}

					if existingRU.Spec.Server.URL != ru.Spec.Server.URL {
						remoteUnleashReceived.WithLabelValues(removalState, "rejected").Inc()
						log.Info("Refusing to delete RemoteUnleash due to URL mismatch - possible hijack attempt",
							"name", ru.Name, "namespace", ru.Namespace,
							"existingURL", existingRU.Spec.Server.URL, "newURL", ru.Spec.Server.URL)
						continue
					}

					existingSecret, err := federationAdminSecret(ctx, r.APIReader, existingRU)
					if apierrors.IsNotFound(err) {
						// Without the stored credential the removal cannot be
						// authenticated, so this resource is left alone: deleting
						// unverified is exactly the hijack the checks below exist
						// to stop. Returning the error instead would nack forever
						// — the secret is not coming back — and block every later
						// message sharing the ordering key, now in every cluster
						// rather than only the ones the message names.
						remoteUnleashReceived.WithLabelValues(removalState, "missing_secret").Inc()
						log.Info("Refusing to delete RemoteUnleash whose admin secret is missing",
							"name", ru.Name, "namespace", ru.Namespace,
							"secret", existingRU.AdminSecretNamespacedName())
						continue
					}
					if err != nil {
						if !retriableError(err) {
							return failPermanently(err)
						}
						return err
					}
					if !federationTokensEqual(existingRU, existingSecret, adminSecrets[i]) {
						remoteUnleashReceived.WithLabelValues(removalState, "rejected").Inc()
						log.Info("Refusing to delete RemoteUnleash due to credential mismatch - possible hijack attempt",
							"name", ru.Name, "namespace", ru.Namespace)
						continue
					}

					safeRUs = append(safeRUs, existingRU)
					safeSecrets = append(safeSecrets, existingSecret)
				}

				if len(safeRUs) == 0 {
					return nil
				}

				// Delete RemoteUnleash resources
				objectsCtx, objectsCancel := r.Timeout.WriteContext(ctx)
				defer objectsCancel()

				if errs := utils.DeleteAllObjects(objectsCtx, r.Client, safeRUs); len(errs) > 0 {
					var permanentErr error
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues(removalState, "failed").Inc()
						log.Error(err, "Failed to delete RemoteUnleash")

						if !retriableError(err) {
							permanentErr = err
						}
					}
					if permanentErr != nil {
						return failPermanently(permanentErr)
					}
					return errs[0]
				}

				// Delete the admin secrets
				secretCtx, secretCancel := r.Timeout.WriteContext(ctx)
				defer secretCancel()

				if errs := utils.DeleteAllObjects(secretCtx, r.Client, safeSecrets); len(errs) > 0 {
					var permanentErr error
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues(removalState, "failed").Inc()
						log.Error(err, "Failed to delete admin secret")

						if !retriableError(err) {
							permanentErr = err
						}
					}
					if permanentErr != nil {
						return failPermanently(permanentErr)
					}
					return errs[0]
				}

				remoteUnleashReceived.WithLabelValues(removalState, "success").Inc()
				log.Info("Successfully deleted RemoteUnleash resources and secret")
				return nil

			case pb.Status_Provisioned:
				log.Info("Received Status_Provisioned")

				// Filter safe objects to prevent cross-namespace overwrite hijacking
				var safeSecrets []*corev1.Secret
				var safeRUs []*unleashv1.RemoteUnleash
				supersededSecrets := make(map[client.ObjectKey]*corev1.Secret)

				for i, ru := range remoteUnleashes {
					existingRU := &unleashv1.RemoteUnleash{}
					err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(ru), existingRU)
					if err != nil && !apierrors.IsNotFound(err) {
						if !retriableError(err) {
							return failPermanently(err)
						}
						return err
					}
					if err == nil {
						if existingRU.Spec.Server.URL != ru.Spec.Server.URL {
							remoteUnleashReceived.WithLabelValues("provisioned", "rejected").Inc()
							log.Info("Refusing to overwrite RemoteUnleash due to URL mismatch - possible hijack attempt",
								"name", ru.Name, "namespace", ru.Namespace,
								"existingURL", existingRU.Spec.Server.URL, "newURL", ru.Spec.Server.URL)
							continue
						}

						existingSecret, err := federationAdminSecret(ctx, r.APIReader, existingRU)
						if err != nil {
							if !retriableError(err) {
								return failPermanently(err)
							}
							return err
						}
						if !federationTokensEqual(existingRU, existingSecret, adminSecrets[i]) {
							remoteUnleashReceived.WithLabelValues("provisioned", "rejected").Inc()
							log.Info("Refusing to overwrite RemoteUnleash due to credential mismatch - possible hijack attempt",
								"name", ru.Name, "namespace", ru.Namespace)
							continue
						}

						if client.ObjectKeyFromObject(existingSecret) != client.ObjectKeyFromObject(adminSecrets[i]) {
							referencedByOther, err := federationAdminSecretReferencedByOtherRemoteUnleash(
								ctx,
								r.APIReader,
								client.ObjectKeyFromObject(existingSecret),
								client.ObjectKeyFromObject(existingRU),
							)
							if err != nil {
								if !retriableError(err) {
									return failPermanently(err)
								}
								return err
							}
							if !referencedByOther {
								supersededSecrets[client.ObjectKeyFromObject(existingSecret)] = existingSecret
							}
						}
					}

					discoveredSecrets, err := federationSupersededAdminSecrets(
						ctx,
						r.APIReader,
						ru,
						adminSecrets[i],
						r.OperatorNamespace,
					)
					if err != nil {
						if !retriableError(err) {
							return failPermanently(err)
						}
						return err
					}
					for _, secret := range discoveredSecrets {
						supersededSecrets[client.ObjectKeyFromObject(secret)] = secret
					}

					// Record what this resource has seen, so a later replay of an
					// older message cannot delete it. The stamp only moves
					// forward: a replayed older provisioning message must not roll
					// it back and re-open the window it closes.
					stamp := publishTime
					if lastApplied := federationLastAppliedPublishTime(existingRU); lastApplied.After(stamp) {
						stamp = lastApplied
					}
					setFederationPublishTime(ru, stamp)

					safeRUs = append(safeRUs, ru)
					safeSecrets = append(safeSecrets, adminSecrets[i])
				}

				if len(safeRUs) == 0 {
					return nil
				}

				secretCtx, secretCancel := r.Timeout.WriteContext(ctx)
				defer secretCancel()

				if errs := utils.UpsertAllObjects(secretCtx, r.Client, safeSecrets); len(errs) > 0 {
					var permanentErr error
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()

						if !retriableError(err) {
							permanentErr = err
						}
					}
					if permanentErr != nil {
						return failPermanently(permanentErr)
					}
					return errs[0]
				}

				objectsCtx, objectsCancel := r.Timeout.WriteContext(ctx)
				defer objectsCancel()

				if err := utils.UpsertAllObjects(objectsCtx, r.Client, safeRUs); len(err) > 0 {
					for _, err := range err {
						remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()

						if namespaceNotFoundError(err) {
							log.Info(fmt.Sprintf("Namespace %s not found for RemoteUnleash %s", err.(*apierrors.StatusError).ErrStatus.Details.Name, remoteUnleashes[0].GetName()))
							continue
						} else {
							if !retriableError(err) {
								return failPermanently(err)
							}
							return err
						}
					}
				}

				cleanupCtx, cleanupCancel := r.Timeout.WriteContext(ctx)
				defer cleanupCancel()
				secretsToDelete := make([]*corev1.Secret, 0, len(supersededSecrets))
				for _, secret := range supersededSecrets {
					secretsToDelete = append(secretsToDelete, secret)
				}
				if errs := utils.DeleteAllObjects(cleanupCtx, r.Client, secretsToDelete); len(errs) > 0 {
					var permanentErr error
					for _, err := range errs {
						remoteUnleashReceived.WithLabelValues("provisioned", "failed").Inc()
						log.Error(err, "Failed to delete superseded federation admin secret")

						if !retriableError(err) {
							permanentErr = err
						}
					}
					if permanentErr != nil {
						return failPermanently(permanentErr)
					}
					return errs[0]
				}

				remoteUnleashReceived.WithLabelValues("provisioned", "success").Inc()
				return nil
			default:
				remoteUnleashReceived.WithLabelValues("unknown", "failed").Inc()
				log.Error(fmt.Errorf("unknown status: %s", status), "Received unknown status")
				return nil
			}
		})

		healthy.Stop()
		cancel(nil)

		if ctx.Err() != nil {
			federationReceiveConsecutiveErrors.Set(0)
			return nil
		}

		if cause := context.Cause(subCtx); cause != nil && !errors.Is(cause, context.Canceled) {
			log.Error(cause, "Permanent federation handler error, stopping subscriber")
			return cause
		}

		// A subscription that stayed up past the backoff cap counts as
		// recovered; the next blip starts from a clean retry state instead of
		// inheriting a pinned gauge and a five-minute delay.
		if err == nil || time.Since(started) > federationReceiveBackoffMax {
			consecutiveErrors = 0
			backoff = federationReceiveBackoffBase
			federationReceiveConsecutiveErrors.Set(0)
		}

		if err != nil {
			consecutiveErrors++
			federationReceiveConsecutiveErrors.Set(float64(consecutiveErrors))
			log.Error(err, "Federation subscription failed, reconnecting with backoff",
				"attempt", consecutiveErrors, "backoff", backoff)
			if consecutiveErrors >= federationReceiveEscalationThreshold {
				log.Error(err, "Federation subscription has failed repeatedly",
					"consecutiveErrors", consecutiveErrors)
			}

			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				federationReceiveConsecutiveErrors.Set(0)
				return nil
			case <-timer.C:
			}
			backoff *= 2
			if backoff > federationReceiveBackoffMax {
				backoff = federationReceiveBackoffMax
			}
			continue
		}

		// Receive exited without an error or permanent cause; nothing more to do.
		federationReceiveConsecutiveErrors.Set(0)
		return nil
	}

	federationReceiveConsecutiveErrors.Set(0)
	return nil
}

// retriableError returns true if the error is not a forbidden or unauthorized error.
func retriableError(err error) bool {
	return !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err)
}

// federationLastAppliedPublishTime reports the publish time of the most recent
// federation message applied to remoteUnleash. A missing or unparseable value
// reads as the zero time, meaning "unknown": callers must treat that as no basis
// to refuse rather than as an ordering claim.
func federationLastAppliedPublishTime(remoteUnleash *unleashv1.RemoteUnleash) time.Time {
	value, ok := remoteUnleash.Annotations[unleashv1.RemoteUnleashFederationPublishTimeAnnotation]
	if !ok {
		return time.Time{}
	}

	publishTime, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return publishTime
}

// setFederationPublishTime stamps remoteUnleash with the publish time of the
// federation message being applied to it. A zero time is not recorded: it means
// the transport supplied none, and a stamp that claims an ordering nobody
// established would refuse every later deletion.
func setFederationPublishTime(remoteUnleash *unleashv1.RemoteUnleash, publishTime time.Time) {
	if publishTime.IsZero() {
		return
	}

	if remoteUnleash.Annotations == nil {
		remoteUnleash.Annotations = map[string]string{}
	}
	remoteUnleash.Annotations[unleashv1.RemoteUnleashFederationPublishTimeAnnotation] = publishTime.UTC().Format(time.RFC3339Nano)
}

func federationAdminSecret(ctx context.Context, k8sClient client.Reader, remoteUnleash *unleashv1.RemoteUnleash) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, remoteUnleash.AdminSecretNamespacedName(), secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func federationTokensEqual(existingRU *unleashv1.RemoteUnleash, existingSecret, incomingSecret *corev1.Secret) bool {
	existingToken := secretValue(existingSecret, existingRU.Spec.AdminSecret.Key)
	incomingToken := secretValue(incomingSecret, unleashv1.UnleashSecretTokenKey)
	if len(existingToken) == 0 || len(incomingToken) == 0 || len(existingToken) != len(incomingToken) {
		return false
	}
	return subtle.ConstantTimeCompare(existingToken, incomingToken) == 1
}

func federationSupersededAdminSecrets(
	ctx context.Context,
	k8sClient client.Reader,
	remoteUnleash *unleashv1.RemoteUnleash,
	currentSecret *corev1.Secret,
	operatorNamespace string,
) ([]*corev1.Secret, error) {
	namespaces := []string{remoteUnleash.Namespace}
	if operatorNamespace != remoteUnleash.Namespace {
		namespaces = append(namespaces, operatorNamespace)
	}

	currentKey := client.ObjectKeyFromObject(currentSecret)
	superseded := make([]*corev1.Secret, 0)
	var referencedSecrets map[client.ObjectKey]struct{}
	for _, namespace := range namespaces {
		secrets := &corev1.SecretList{}
		if err := k8sClient.List(
			ctx,
			secrets,
			client.InNamespace(namespace),
			client.MatchingLabels{
				"app.kubernetes.io/instance":   remoteUnleash.Name,
				"app.kubernetes.io/part-of":    "unleasherator",
				"app.kubernetes.io/created-by": "controller-manager",
			},
		); err != nil {
			return nil, err
		}

		for i := range secrets.Items {
			candidate := &secrets.Items[i]
			if client.ObjectKeyFromObject(candidate) == currentKey ||
				!strings.HasPrefix(candidate.Name, unleashv1.UnleashSecretNamePrefix+"-") {
				continue
			}
			if namespace == operatorNamespace {
				authorizedNamespace, annotated := candidate.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation]
				if annotated && authorizedNamespace != remoteUnleash.Namespace {
					continue
				}
				if !annotated {
					if referencedSecrets == nil {
						var err error
						referencedSecrets, err = federationAdminSecretReferences(ctx, k8sClient)
						if err != nil {
							return nil, err
						}
					}
					if _, referenced := referencedSecrets[client.ObjectKeyFromObject(candidate)]; referenced {
						continue
					}
				}
			}
			if federationTokensEqual(remoteUnleash, candidate, currentSecret) {
				superseded = append(superseded, candidate)
			}
		}
	}

	return superseded, nil
}

func federationAdminSecretReferences(ctx context.Context, k8sClient client.Reader) (map[client.ObjectKey]struct{}, error) {
	remoteUnleashes := &unleashv1.RemoteUnleashList{}
	if err := k8sClient.List(ctx, remoteUnleashes); err != nil {
		return nil, err
	}

	references := make(map[client.ObjectKey]struct{}, len(remoteUnleashes.Items))
	for i := range remoteUnleashes.Items {
		references[remoteUnleashes.Items[i].AdminSecretNamespacedName()] = struct{}{}
	}
	return references, nil
}

func federationAdminSecretReferencedByOtherRemoteUnleash(
	ctx context.Context,
	k8sClient client.Reader,
	secretKey client.ObjectKey,
	currentRemoteUnleashKey client.ObjectKey,
) (bool, error) {
	remoteUnleashes := &unleashv1.RemoteUnleashList{}
	if err := k8sClient.List(ctx, remoteUnleashes); err != nil {
		return false, err
	}

	for i := range remoteUnleashes.Items {
		remoteUnleash := &remoteUnleashes.Items[i]
		if client.ObjectKeyFromObject(remoteUnleash) != currentRemoteUnleashKey &&
			remoteUnleash.AdminSecretNamespacedName() == secretKey {
			return true, nil
		}
	}
	return false, nil
}

func secretValue(secret *corev1.Secret, key string) []byte {
	if value := secret.Data[key]; len(value) > 0 {
		return value
	}
	return []byte(secret.StringData[key])
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
			MaxConcurrentReconciles: 4, // Process multiple instances in parallel
		}).
		Complete(r)
}
