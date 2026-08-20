package controller

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-logr/logr"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ReleaseChannelFinalizer = "releasechannel.unleash.nais.io/finalizer"
)

var (
	// Prefixed to avoid conflicts with other controllers (vars are overridden in tests)
	releaseChannelErrorRetryDelay         = 5 * time.Second
	releaseChannelIdleRequeueInterval     = 10 * time.Minute
	releaseChannelValidatingRetryDelay    = 5 * time.Minute
	releaseChannelValidatingTransition    = 5 * time.Second
	releaseChannelCanaryWaitDelay         = 30 * time.Second
	releaseChannelRollingWaitDelay        = 30 * time.Second
	releaseChannelRollingBackWaitDelay    = 1 * time.Minute
	releaseChannelFailedRetryDelay        = 10 * time.Minute
	releaseChannelStatusUpdateSuccess     = 30 * time.Second
	releaseChannelBackoffBase             = 10 * time.Second
	releaseChannelBackoffMedium           = 20 * time.Second
	releaseChannelBackoffLong             = 30 * time.Second
	releaseChannelBatchInterval           = 30 * time.Second
	releaseChannelDeletionCheckInterval   = 30 * time.Second
	releaseChannelHealthCheckInitialDelay = 30 * time.Second
	releaseChannelDefaultMaxUpgradeTime   = 10 * time.Minute
	// Mirrors the CRD default for spec.healthChecks.timeout, used when the field
	// is unset.
	releaseChannelDefaultHealthCheckTimeout = 5 * time.Minute
	// Ceiling on a derived upgrade budget. Without it the derivation grows with
	// fleet size until a wedged rollout would hang for the better part of a day,
	// which is the opposite of what this timeout exists for.
	releaseChannelMaxDerivedUpgradeTime = 2 * time.Hour
	releaseChannelHealthCheckTimeout    = 5 * time.Second
	releaseChannelTransientRetryBase    = 30 * time.Second
	releaseChannelMaxTransientRetries   = 5
)

// ReleaseChannelReconciler reconciles a ReleaseChannel object
type ReleaseChannelReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Tracer   trace.Tracer
}

var (
	releaseChannelStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_releasechannel_status",
			Help: "Status of ReleaseChannel resources (1=healthy, 0.5=in-progress, 0=failed)",
		},
		[]string{"namespace", "name"},
	)

	releaseChannelInstances = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_releasechannel_instances_total",
			Help: "Total number of Unleash instances managed by ReleaseChannel",
		},
		[]string{"namespace", "name"},
	)

	releaseChannelInstancesUpToDate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_releasechannel_instances_up_to_date",
			Help: "Number of Unleash instances running the target image",
		},
		[]string{"namespace", "name"},
	)

	releaseChannelRollouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_rollouts_total",
			Help: "Total number of ReleaseChannel rollout events",
		},
		[]string{"namespace", "name", "result"},
	)

	releaseChannelRolloutDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "unleasherator_releasechannel_rollout_duration_seconds",
			Help:    "Duration of ReleaseChannel rollouts in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s to ~17 minutes
		},
		[]string{"namespace", "name"},
	)

	releaseChannelConflicts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_conflicts_total",
			Help: "Total number of resource conflicts encountered during rollouts",
		},
		[]string{"namespace", "name"},
	)

	releaseChannelTransientRetries = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_transient_retries_total",
			Help: "Total number of automatic retries for transient failures",
		},
		[]string{"namespace", "name"},
	)

	releaseChannelPhaseTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_phase_transitions_total",
			Help: "Total number of phase transitions for ReleaseChannels",
		},
		[]string{"namespace", "name", "phase"},
	)

	releaseChannelHealthChecks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_health_checks_total",
			Help: "Total number of health check attempts",
		},
		[]string{"namespace", "name", "result"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		releaseChannelStatus,
		releaseChannelInstances,
		releaseChannelInstancesUpToDate,
		releaseChannelRollouts,
		releaseChannelRolloutDuration,
		releaseChannelConflicts,
		releaseChannelTransientRetries,
		releaseChannelPhaseTransitions,
		releaseChannelHealthChecks,
	)
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/finalizers,verbs=update
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes,verbs=get;list;watch;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes/status,verbs=get;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ReleaseChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("releasechannel", req.NamespacedName)

	ctx, span := r.Tracer.Start(ctx, "releasechannel.reconcile")
	defer span.End()

	labels := []string{req.Namespace, req.Name}

	releaseChannel := &unleashv1.ReleaseChannel{}
	err := r.Get(ctx, req.NamespacedName, releaseChannel)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReleaseChannel deleted, ignoring")
			releaseChannelStatus.DeleteLabelValues(labels...)
			releaseChannelInstances.DeleteLabelValues(labels...)
			releaseChannelInstancesUpToDate.DeleteLabelValues(labels...)
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get ReleaseChannel")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if releaseChannel.DeletionTimestamp != nil {
		return r.handleDeletion(ctx, releaseChannel, log)
	}

	// Add finalizer if not present - use retry to handle concurrent modifications
	if !controllerutil.ContainsFinalizer(releaseChannel, ReleaseChannelFinalizer) {
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// Fetch fresh copy for each retry attempt
			fresh := &unleashv1.ReleaseChannel{}
			if err := r.Get(ctx, req.NamespacedName, fresh); err != nil {
				return err
			}
			// Check again - another reconcile might have added it
			if controllerutil.ContainsFinalizer(fresh, ReleaseChannelFinalizer) {
				*releaseChannel = *fresh
				return nil
			}
			controllerutil.AddFinalizer(fresh, ReleaseChannelFinalizer)
			err := r.Update(ctx, fresh)
			if err == nil {
				*releaseChannel = *fresh
			}
			return err
		})
		if err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Initialize status if needed
	if err := r.initializeStatus(ctx, releaseChannel); err != nil {
		log.Error(err, "Failed to initialize status")
		return ctrl.Result{}, err
	}

	// Execute the rollout based on current phase
	result, err := r.executePhase(ctx, releaseChannel, log)
	if err != nil {
		// Conflict errors are transient — let controller-runtime requeue without
		// transitioning to Failed phase.
		if apierrors.IsConflict(err) {
			log.V(1).Info("Conflict during phase execution, requeueing", "phase", releaseChannel.Status.Phase, "error", err)
			return ctrl.Result{Requeue: true}, nil
		}
		log.Error(err, "Failed to execute phase", "phase", releaseChannel.Status.Phase)
		r.recordError(ctx, releaseChannel, err)
		// Record failed rollout
		releaseChannelRollouts.WithLabelValues(labels[0], labels[1], "failed").Inc()
		// Try to update status even on error
		if statusErr := r.Status().Update(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update status after error", "error", statusErr)
		}
		return result, err
	}

	// Only update status at the end if the phase execution didn't already handle it
	// Phase execution methods that return updateReleaseChannelStatus already handle status updates
	if result.RequeueAfter > 0 {
		// Skip final status update since phase methods handle their own status updates
		log.V(1).Info("Skipping final status update as phase method handles it")
	}

	// Record metrics for successful reconciliation
	r.recordMetrics(releaseChannel, labels)

	// Record rollout duration if this was a completed rollout
	if releaseChannel.Status.Phase == unleashv1.ReleaseChannelPhaseCompleted && releaseChannel.Status.LastReconcileTime != nil {
		rolloutDuration := time.Since(releaseChannel.Status.LastReconcileTime.Time).Seconds()
		releaseChannelRolloutDuration.WithLabelValues(labels[0], labels[1]).Observe(rolloutDuration)
		// Record successful rollout
		releaseChannelRollouts.WithLabelValues(labels[0], labels[1], "success").Inc()
	}

	return result, nil
}

// handleDeletion implements orphan protection - blocks deletion while Unleash instances reference this channel
func (r *ReleaseChannelReconciler) handleDeletion(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("ReleaseChannel marked for deletion")

	// Check for referencing Unleash instances (orphan protection)
	referencingInstances, err := r.getReferencingInstances(ctx, releaseChannel)
	if err != nil {
		log.Error(err, "Failed to check for referencing instances")
		return ctrl.Result{}, fmt.Errorf("failed to check for referencing instances: %w", err)
	}

	if len(referencingInstances) > 0 {
		instanceNames := make([]string, len(referencingInstances))
		for i, inst := range referencingInstances {
			instanceNames[i] = inst.Name
		}
		log.Info("Cannot delete ReleaseChannel: still referenced by Unleash instances",
			"referencingInstances", instanceNames,
			"count", len(referencingInstances))

		// Update status to indicate deletion is blocked - use retry for conflict handling
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			fresh := &unleashv1.ReleaseChannel{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(releaseChannel), fresh); err != nil {
				return err
			}
			meta.SetStatusCondition(&fresh.Status.Conditions, metav1.Condition{
				Type:    "DeletionBlocked",
				Status:  metav1.ConditionTrue,
				Reason:  "ReferencingInstancesExist",
				Message: fmt.Sprintf("Cannot delete: %d Unleash instance(s) still reference this ReleaseChannel: %v", len(referencingInstances), instanceNames),
			})
			return r.Status().Update(ctx, fresh)
		})
		if err != nil {
			log.Error(err, "Failed to update status with deletion blocked condition")
		}

		// Requeue to check again later - instances may be deleted or updated
		return ctrl.Result{RequeueAfter: releaseChannelDeletionCheckInterval}, nil
	}

	log.Info("No referencing instances found, proceeding with deletion")

	// Remove finalizer to allow deletion - use retry for conflict handling
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		fresh := &unleashv1.ReleaseChannel{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(releaseChannel), fresh); err != nil {
			return err
		}
		controllerutil.RemoveFinalizer(fresh, ReleaseChannelFinalizer)
		return r.Update(ctx, fresh)
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *ReleaseChannelReconciler) getReferencingInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) ([]unleashv1.Unleash, error) {
	log := log.FromContext(ctx)
	unleashList := &unleashv1.UnleashList{}
	if err := r.List(ctx, unleashList, client.InNamespace(releaseChannel.ObjectMeta.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Unleash instances: %w", err)
	}

	log.V(1).Info("Checking for referencing Unleash instances",
		"releaseChannel", releaseChannel.ObjectMeta.Name,
		"namespace", releaseChannel.ObjectMeta.Namespace,
		"totalInstances", len(unleashList.Items))

	var referencingInstances []unleashv1.Unleash
	for _, unleash := range unleashList.Items {
		log.V(1).Info("Checking Unleash instance reference",
			"unleash", unleash.ObjectMeta.Name,
			"referencedChannel", unleash.Spec.ReleaseChannel.Name,
			"targetChannel", releaseChannel.ObjectMeta.Name,
			"matches", unleash.Spec.ReleaseChannel.Name == releaseChannel.ObjectMeta.Name)
		// Include ALL instances that reference this ReleaseChannel, regardless of CustomImage
		if unleash.Spec.ReleaseChannel.Name == releaseChannel.ObjectMeta.Name {
			referencingInstances = append(referencingInstances, unleash)
		}
	}

	return referencingInstances, nil
}

func (r *ReleaseChannelReconciler) initializeStatus(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) error {
	if len(releaseChannel.Status.Conditions) == 0 || releaseChannel.Status.Phase == "" {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Initializing",
			Message: "Initializing ReleaseChannel",
		})
	}
	return nil
}

// updateConditionForPhase sets the appropriate condition based on the current phase and rollout state.
// This ensures conditions accurately reflect the ReleaseChannel's operational status.
func (r *ReleaseChannelReconciler) updateConditionForPhase(releaseChannel *unleashv1.ReleaseChannel) {
	phase := releaseChannel.Status.Phase
	instances := releaseChannel.Status.Instances
	rolloutComplete := releaseChannel.Status.Rollout

	var condition metav1.Condition

	switch {
	case instances == 0:
		// No instances managed by this ReleaseChannel
		condition = metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionTrue,
			Reason:  "NoInstances",
			Message: "No Unleash instances are managed by this ReleaseChannel",
		}
	case phase == unleashv1.ReleaseChannelPhaseFailed:
		condition = metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionFalse,
			Reason:  "Failed",
			Message: fmt.Sprintf("Rollout failed: %s", releaseChannel.Status.FailureReason),
		}
	case phase == unleashv1.ReleaseChannelPhaseRollingBack:
		condition = metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionFalse,
			Reason:  "RollingBack",
			Message: "Rolling back to previous image",
		}
	case phase == unleashv1.ReleaseChannelPhaseCanary || phase == unleashv1.ReleaseChannelPhaseRolling || phase == unleashv1.ReleaseChannelPhaseValidating:
		condition = metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Progressing",
			Message: fmt.Sprintf("Rollout in progress (%d/%d instances up to date)", releaseChannel.Status.InstancesUpToDate, instances),
		}
	case rolloutComplete || phase == unleashv1.ReleaseChannelPhaseCompleted || (phase == unleashv1.ReleaseChannelPhaseIdle && instances > 0):
		condition = metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionTrue,
			Reason:  "Ready",
			Message: fmt.Sprintf("All %d instances are up to date", instances),
		}
	default:
		// Fallback for unknown states
		condition = metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Unknown",
			Message: fmt.Sprintf("Unknown phase: %s", phase),
		}
	}

	meta.SetStatusCondition(&releaseChannel.Status.Conditions, condition)
}

func (r *ReleaseChannelReconciler) executePhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	switch releaseChannel.Status.Phase {
	case unleashv1.ReleaseChannelPhaseIdle, "": // Handle empty phase as idle
		return r.executeIdlePhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseValidating:
		return r.executeValidatingPhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseCanary:
		return r.executeCanaryPhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseRolling:
		return r.executeRollingPhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseCompleted:
		return r.executeCompletedPhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseFailed:
		return r.executeFailedPhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseRollingBack:
		return r.executeRollingBackPhase(ctx, releaseChannel, log)
	default:
		log.Info("Unknown phase, resetting to idle", "phase", releaseChannel.Status.Phase)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		return ctrl.Result{RequeueAfter: releaseChannelErrorRetryDelay}, nil
	}
}

func (r *ReleaseChannelReconciler) executeIdlePhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing idle phase", "specImage", string(releaseChannel.Spec.Image), "previousImage", releaseChannel.Status.PreviousImage)
	releaseChannel.Status.ActiveBatch = nil

	// Find all Unleash instances that reference this ReleaseChannel
	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get target instances: %w", err)
	}

	if len(targetInstances) == 0 {
		log.Info("No matching Unleash instances found")
		// Set phase to Idle and update status even when no instances found
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		releaseChannel.Status.Instances = 0
		releaseChannel.Status.InstancesUpToDate = 0
		releaseChannel.Status.Rollout = false
		// Update condition to reflect no instances state
		r.updateConditionForPhase(releaseChannel)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	// Check if all instances are already up to date
	targetImage := releaseChannel.Spec.Image
	log.Info("Checking instances for updates", "targetImage", string(targetImage), "instanceCount", len(targetInstances))

	// Before checking if instances need updates, check if the target image has changed
	// and if so, capture the previous target for rollback purposes
	if _, err := r.ensurePreviousImageTracked(ctx, releaseChannel, targetInstances, log); err != nil {
		return ctrl.Result{RequeueAfter: releaseChannelErrorRetryDelay}, err
	}

	var instancesToUpdate []unleashv1.Unleash
	for _, unleash := range targetInstances {
		currentImage := unleash.Status.ResolvedReleaseChannelImage
		log.Info("Instance status", "name", unleash.Name, "currentImage", currentImage, "targetImage", string(targetImage))

		// Determine expected image for this instance based on rollout strategy
		expectedImage := r.getExpectedImageForInstance(ctx, &unleash, string(targetImage))

		if currentImage != expectedImage {
			instancesToUpdate = append(instancesToUpdate, unleash)
		}
	}

	// The rollback baseline is only meaningful when instances agree; see
	// rollbackBaseline. When they disagree this stays empty and no PreviousImage
	// is recorded, so a later rollback refuses instead of guessing a target.
	currentDeployedImage := rollbackBaseline(targetInstances)

	log.Info("Image tracking state", "currentDeployedImage", currentDeployedImage, "previousImage", releaseChannel.Status.PreviousImage, "targetImage", string(targetImage))

	if len(instancesToUpdate) == 0 {
		log.Info("All instances are up to date")
		// Adopt instances that reached the target without ever being batched —
		// they joined the channel after its last rollout, and nothing else adds
		// them to InstanceImages: rollouts only ever assign instances that need
		// updating, and these already match. Left untracked they would resolve
		// through the fallback in ResolveReleaseChannelImage forever instead of
		// from the map that is meant to be the source of truth.
		r.adoptUpToDateInstances(releaseChannel, targetInstances, log)
		// Update instance counts even when no updates are needed to maintain accurate status
		r.updateInstanceCounts(releaseChannel, targetInstances)
		labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
		r.recordMetrics(releaseChannel, labels)

		if statusResult, err := r.updateReleaseChannelStatus(ctx, releaseChannel); err != nil {
			return statusResult, err
		}
		return ctrl.Result{RequeueAfter: releaseChannelIdleRequeueInterval}, nil
	}

	// Refuse to roll out a version older than the fleet already runs. Nothing
	// else in the pipeline distinguishes a downgrade from an upgrade: a yanked
	// or stale tag differs from the deployed image by string inequality just
	// like a new release does, so it would roll out and "complete" successfully.
	// Automatic rollback only fires on outright deployment failure, which a
	// working-but-old image never triggers.
	// Checked against every deployed image rather than the rollback baseline: a
	// fleet that disagrees has no baseline, and that must not be the reason a
	// downgrade slips through.
	if downgradedFrom, refuse := r.downgradeFrom(releaseChannel, targetInstances); refuse {
		message := fmt.Sprintf("Refusing rollout: target image %s is an older version than the deployed image %s; set spec.allowDowngrade to override", string(targetImage), downgradedFrom)
		log.Info("Refusing downgrade", "targetImage", string(targetImage), "deployedImage", downgradedFrom)

		// Stay in Idle rather than failing. Failed would hand the channel to
		// the rollback machinery, which would start moving instances around
		// even though nothing has been deployed yet.
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		r.updateInstanceCounts(releaseChannel, targetInstances)
		labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
		r.recordMetrics(releaseChannel, labels)
		meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionFalse,
			Reason:  "DowngradeRefused",
			Message: message,
		})
		r.Recorder.Event(releaseChannel, "Warning", "DowngradeRefused", message)

		if statusResult, err := r.updateReleaseChannelStatus(ctx, releaseChannel); err != nil {
			return statusResult, err
		}
		return ctrl.Result{RequeueAfter: releaseChannelIdleRequeueInterval}, nil
	}

	// Every rollout goes through the Rolling state machine, first deploy included.
	// There used to be a shortcut here that wrote the target image for every
	// matching instance in one pass, with no maxParallel batching and no health
	// gating. Its trigger was that no instance reported a resolved image, which
	// is true of a genuine first deploy but equally true after any event that
	// clears instance status — a status migration, a field rename, a mass
	// re-create — turning a bookkeeping change into a simultaneous fleet-wide
	// upgrade. Its comment claimed the guard also required an empty
	// PreviousImage; the condition never checked that, so the shortcut was
	// weaker than it read.

	log.Info("Starting rollout", "instancesToUpdate", len(instancesToUpdate))

	// Store the previous image for potential rollback
	// When starting a new rollout, we need to capture what image instances should rollback to
	// If PreviousImage is not set and we have instances that need updating, it means this is
	// a new rollout and we should use the currently deployed image as the previous image
	if releaseChannel.Status.PreviousImage == "" && currentDeployedImage != "" {
		releaseChannel.Status.PreviousImage = currentDeployedImage
		log.Info("Setting previous image for rollback", "previousImage", releaseChannel.Status.PreviousImage, "targetImage", string(targetImage))
	} else if releaseChannel.Status.PreviousImage == "" {
		// For the first deployment (no current deployed image), there's no previous image to rollback to
		// In this case, we'll leave PreviousImage empty which is correct
		log.Info("No previous image to set - this appears to be initial deployment", "targetImage", string(targetImage))
	} else {
		log.Info("Previous image already set", "previousImage", releaseChannel.Status.PreviousImage, "targetImage", string(targetImage))
	}

	// Update instance counts before transitioning phases
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
	r.recordMetrics(releaseChannel, labels)

	// Set StartTime for the rollout (used by maxUpgradeTime enforcement)
	now := metav1.Now()
	releaseChannel.Status.StartTime = &now

	// Determine which strategy to use
	strategy := releaseChannel.Spec.Strategy
	if strategy.Canary.Enabled {
		// Start canary deployment
		log.Info("Transitioning to canary phase")
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseCanary
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseCanary)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	} else {
		// Start rolling deployment
		log.Info("Transitioning to rolling phase")
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseRolling
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseRolling)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}
}

func (r *ReleaseChannelReconciler) executeCompletedPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Rollout completed, transitioning to idle")

	// Clear rollout state
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
	releaseChannel.Status.StartTime = nil
	releaseChannel.Status.FailureReason = ""
	releaseChannel.Status.RetryCount = 0
	releaseChannel.Status.LastFailureTime = nil
	releaseChannel.Status.ActiveBatch = nil
	releaseChannel.Status.FailedImage = ""
	// Note: PreviousImage is kept for potential future rollback reference

	return r.updateReleaseChannelStatus(ctx, releaseChannel)
}

func (r *ReleaseChannelReconciler) executeFailedPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Handling failed rollout", "reason", releaseChannel.Status.FailureReason)
	labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}

	if releaseChannel.Status.ActiveBatch != nil {
		releaseChannel.Status.ActiveBatch = nil
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	// Pointing spec.image somewhere else is an operator saying "not that one,
	// try this instead". It is the only way out of a failure that has nothing to
	// roll back to, so it has to be checked before the branches that give up.
	if releaseChannel.Status.FailedImage != "" &&
		string(releaseChannel.Spec.Image) != releaseChannel.Status.FailedImage {
		log.Info("Target image changed since the rollout failed, retrying",
			"failedImage", releaseChannel.Status.FailedImage,
			"newImage", string(releaseChannel.Spec.Image))
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseIdle)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		releaseChannel.Status.FailureReason = ""
		releaseChannel.Status.FailedImage = ""
		releaseChannel.Status.RetryCount = 0
		releaseChannel.Status.LastFailureTime = nil
		r.Recorder.Event(releaseChannel, "Normal", "RetryAfterImageChange",
			fmt.Sprintf("Retrying rollout with %s after failure on %s", string(releaseChannel.Spec.Image), releaseChannel.Status.FailedImage))
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	// Record which image failed, so a later pass can tell that spec.image has
	// moved. Nothing has edited it yet on the pass that first lands here, so it
	// is still the image the rollout was on. Set in place rather than persisted
	// on its own, to avoid spending a reconcile before anything is handled; every
	// branch below that settles the channel writes status.
	if releaseChannel.Status.FailedImage == "" {
		releaseChannel.Status.FailedImage = string(releaseChannel.Spec.Image)
	}

	// Auto-retry transient errors before considering rollback
	if isTransientError(releaseChannel.Status.FailureReason) {
		if releaseChannel.Status.RetryCount < releaseChannelMaxTransientRetries {
			// Ensure LastFailureTime is set for backoff calculation
			if releaseChannel.Status.LastFailureTime == nil {
				now := metav1.Now()
				releaseChannel.Status.LastFailureTime = &now
				return r.updateReleaseChannelStatus(ctx, releaseChannel)
			}

			backoff := calculateTransientBackoff(releaseChannel.Status.RetryCount)
			timeSinceFailure := time.Since(releaseChannel.Status.LastFailureTime.Time)

			if timeSinceFailure >= backoff {
				log.Info("Auto-retrying transient failure",
					"attempt", releaseChannel.Status.RetryCount+1,
					"maxAttempts", releaseChannelMaxTransientRetries,
					"reason", releaseChannel.Status.FailureReason)
				releaseChannel.Status.RetryCount++
				releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
				releaseChannel.Status.FailureReason = ""
				releaseChannel.Status.LastFailureTime = nil
				releaseChannelTransientRetries.WithLabelValues(labels[0], labels[1]).Inc()
				r.Recorder.Event(releaseChannel, "Normal", "TransientRetry",
					fmt.Sprintf("Auto-retrying after transient failure (attempt %d/%d)", releaseChannel.Status.RetryCount, releaseChannelMaxTransientRetries))
				return r.updateReleaseChannelStatus(ctx, releaseChannel)
			}
			// Not enough time has passed, requeue after remaining backoff
			remainingBackoff := backoff - timeSinceFailure
			log.V(1).Info("Waiting for backoff before transient retry",
				"remainingBackoff", remainingBackoff,
				"attempt", releaseChannel.Status.RetryCount+1)
			return ctrl.Result{RequeueAfter: remainingBackoff}, nil
		}
		log.Info("Max transient retries exceeded", "retries", releaseChannel.Status.RetryCount)
	}

	// Automatic rollback when enabled and onFailure is set (or defaults to true)
	if releaseChannel.Spec.Rollback.Enabled && releaseChannel.Spec.Rollback.OnFailure {
		// Same resolution as executeRollingBackPhase and releasePhaseOnFailure.
		// Reading only the tracked baseline here made spec.rollback.previousImage
		// inert from this phase, which is exactly where an operator reaches for
		// it: the condition message tells them to set it.
		if rollbackImage := rollbackTarget(releaseChannel); rollbackImage != "" {
			log.Info("Automatic rollback triggered",
				"previousImage", rollbackImage,
				"failureReason", releaseChannel.Status.FailureReason)
			r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseRollingBack)
			releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseRollingBack
			// Record rollback event
			r.Recorder.Event(releaseChannel, "Warning", "AutomaticRollback",
				fmt.Sprintf("Automatic rollback triggered due to: %s", releaseChannel.Status.FailureReason))
			return r.updateReleaseChannelStatus(ctx, releaseChannel)
		}
		// No baseline was ever captured — either nothing was deployed before this
		// rollout, or instances disagreed on what they ran and rollbackBaseline
		// deliberately refused to pick one. Guessing would move instances to a
		// version they never ran, so this is terminal: nothing the controller can
		// do on its own will change it, and requeueing forever only reprints the
		// same warning. A spec change wakes the channel through its own watch.
		condition := meta.FindStatusCondition(releaseChannel.Status.Conditions, unleashv1.ReleaseChannelStatusConditionTypeReconciled)
		if condition != nil && condition.Reason == "RollbackUnavailable" {
			log.V(1).Info("Rollout failed with no rollback baseline, waiting for operator")
			return ctrl.Result{}, nil
		}

		message := fmt.Sprintf("Rollout failed and cannot be rolled back automatically: no rollback baseline was captured. Original failure: %s. Set spec.rollback.previousImage to roll back explicitly, or correct spec.image.", releaseChannel.Status.FailureReason)
		log.Info("Automatic rollback enabled but no previous image available", "failureReason", releaseChannel.Status.FailureReason)
		meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionFalse,
			Reason:  "RollbackUnavailable",
			Message: message,
		})
		r.Recorder.Event(releaseChannel, "Warning", "RollbackUnavailable", message)
		if _, err := r.updateReleaseChannelStatus(ctx, releaseChannel); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Stay in failed state and requeue periodically
	return ctrl.Result{RequeueAfter: releaseChannelFailedRetryDelay}, nil
}

// isTransientError returns true if the error reason indicates a transient failure
// that may succeed on retry (conflicts, timeouts, connection issues).
func isTransientError(reason string) bool {
	reason = strings.ToLower(reason)
	transientPatterns := []string{
		"conflict",
		"the object has been modified",
		"timeout",
		"context deadline exceeded",
		"connection refused",
		"connection reset",
		"no such host",
		"i/o timeout",
		"temporary failure",
	}
	for _, pattern := range transientPatterns {
		if strings.Contains(reason, pattern) {
			return true
		}
	}
	return false
}

// calculateTransientBackoff returns exponential backoff duration for retry attempt.
// Backoff sequence: 30s, 1m, 2m, 4m, 8m
func calculateTransientBackoff(retryCount int) time.Duration {
	backoff := releaseChannelTransientRetryBase
	for i := 0; i < retryCount; i++ {
		backoff *= 2
	}
	// Cap at 8 minutes
	if backoff > 8*time.Minute {
		backoff = 8 * time.Minute
	}
	return backoff
}

func (r *ReleaseChannelReconciler) recordError(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, err error) {
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
	releaseChannel.Status.FailureReason = err.Error()

	// Set LastFailureTime on first failure (preserve for backoff calculation)
	if releaseChannel.Status.LastFailureTime == nil {
		now := metav1.Now()
		releaseChannel.Status.LastFailureTime = &now
	}

	meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
		Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  "Failed",
		Message: fmt.Sprintf("ReleaseChannel failed: %v", err),
	})
}

func (r *ReleaseChannelReconciler) recordMetrics(releaseChannel *unleashv1.ReleaseChannel, labels []string) {
	// Update status metrics (1=healthy, 0.5=in-progress, 0=failed)
	var status float64
	switch releaseChannel.Status.Phase {
	case unleashv1.ReleaseChannelPhaseCompleted, unleashv1.ReleaseChannelPhaseIdle:
		status = 1 // Healthy (completed rollout or idle waiting for changes)
	case unleashv1.ReleaseChannelPhaseFailed:
		status = 0 // Failed
	default:
		status = 0.5 // In progress (validating, canary, rolling, etc.)
	}
	releaseChannelStatus.WithLabelValues(labels[0], labels[1]).Set(status)

	// Update instance metrics
	releaseChannelInstances.WithLabelValues(labels[0], labels[1]).Set(float64(releaseChannel.Status.Instances))
	releaseChannelInstancesUpToDate.WithLabelValues(labels[0], labels[1]).Set(float64(releaseChannel.Status.InstancesUpToDate))
}

func (r *ReleaseChannelReconciler) executeValidatingPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing validating phase")

	// Basic validation checks
	if string(releaseChannel.Spec.Image) == "" {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = "Invalid image specification"
		return ctrl.Result{}, nil
	}

	// Find target instances
	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances: %v", err)
		return ctrl.Result{}, err
	}

	if len(targetInstances) == 0 {
		log.Info("No target instances found")
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		return ctrl.Result{RequeueAfter: releaseChannelValidatingRetryDelay}, nil
	}

	// Update status and determine next phase
	r.updateInstanceCounts(releaseChannel, targetInstances)

	// Record updated metrics after counting instances
	labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
	r.recordMetrics(releaseChannel, labels)

	// Update status to persist instance counts
	if err := r.Status().Update(ctx, releaseChannel); err != nil {
		log.V(1).Info("Failed to update ReleaseChannel status with instance counts", "error", err)
	}

	// Check if canary deployment is enabled
	if releaseChannel.Spec.Strategy.Canary.Enabled {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseCanary
		log.Info("Transitioning to canary phase")
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseCanary)
	} else {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseRolling
		log.Info("Transitioning to rolling phase")
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseRolling)
	}

	return ctrl.Result{RequeueAfter: releaseChannelValidatingTransition}, nil
}

func (r *ReleaseChannelReconciler) executeCanaryPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing canary phase")

	// Check if we've exceeded maxUpgradeTime
	if exceeded, reason := r.checkMaxUpgradeTimeExceeded(releaseChannel); exceeded {
		budget, derivation := upgradeTimeBudget(releaseChannel)
		log.Info("Canary phase exceeded maxUpgradeTime", "reason", reason, "budget", budget, "derivation", derivation)
		newPhase := releasePhaseOnFailure(releaseChannel)
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.FailureReason = reason
		r.Recorder.Event(releaseChannel, "Warning", "RolloutTimeout", reason)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances: %v", err)
		return ctrl.Result{}, err
	}

	// Check for target image changes during canary phase and track previous image
	if _, err := r.ensurePreviousImageTracked(ctx, releaseChannel, targetInstances, log); err != nil {
		return ctrl.Result{RequeueAfter: releaseChannelErrorRetryDelay}, err
	}

	// Identify canary instances
	canaryInstances := r.getCanaryInstances(targetInstances, releaseChannel)
	if len(canaryInstances) == 0 {
		log.Info("No canary instances found, transitioning to rolling phase")
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseRolling)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseRolling
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	log.Info("Processing canary instances", "count", len(canaryInstances))

	// Deploy to ALL instances - canaries get new image, others stay on previous
	// The getExpectedImageForInstance function determines correct image per instance
	result, err := r.deployToInstances(ctx, releaseChannel, targetInstances, log)
	if err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
		newPhase := releasePhaseOnFailure(releaseChannel)
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.FailureReason = fmt.Sprintf("Canary deployment failed: %v", err)
		// Record metrics for failure state
		labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return result, err
	}

	// Update instance counts after deployment (use targetInstances for total counts)
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
	r.recordMetrics(releaseChannel, labels)

	// Check if canary deployment is complete
	canaryComplete := r.areInstancesReady(ctx, canaryInstances, string(releaseChannel.Spec.Image), true, log)
	if !canaryComplete {
		log.Info("Canary instances not ready yet")
		if _, err := r.updateReleaseChannelStatus(ctx, releaseChannel); err != nil {
			log.V(1).Info("Failed to update ReleaseChannel status before requeue", "error", err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: releaseChannelCanaryWaitDelay}, nil
	}

	// Perform health checks on canary instances
	if releaseChannel.Spec.HealthChecks.Enabled {
		healthy, err := r.performHealthChecks(ctx, canaryInstances, releaseChannel, settleReference(releaseChannel), log)
		if err != nil {
			newPhase := releasePhaseOnFailure(releaseChannel)
			r.recordPhaseTransition(releaseChannel, newPhase)
			releaseChannel.Status.Phase = newPhase
			releaseChannel.Status.FailureReason = fmt.Sprintf("Canary health check failed: %v", err)
			// Record metrics for failure state
			labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
			r.recordMetrics(releaseChannel, labels)
			if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
				log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
			}
			return ctrl.Result{}, err
		}

		if !healthy {
			log.Info("Canary health checks not passing yet")
			return ctrl.Result{RequeueAfter: releaseChannelCanaryWaitDelay}, nil
		}
	}

	log.Info("All instances passed health checks")
	log.Info("Canary deployment successful, transitioning to rolling phase")
	r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseRolling)
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseRolling
	// Record metrics for successful phase transition
	labels = []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
	r.recordMetrics(releaseChannel, labels)
	return r.updateReleaseChannelStatus(ctx, releaseChannel)
}

func (r *ReleaseChannelReconciler) executeRollingPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing rolling phase")

	// Check if we've exceeded maxUpgradeTime
	if exceeded, reason := r.checkMaxUpgradeTimeExceeded(releaseChannel); exceeded {
		budget, derivation := upgradeTimeBudget(releaseChannel)
		log.Info("Rolling phase exceeded maxUpgradeTime", "reason", reason, "budget", budget, "derivation", derivation)
		newPhase := releasePhaseOnFailure(releaseChannel)
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.ActiveBatch = nil
		releaseChannel.Status.FailureReason = reason
		r.Recorder.Event(releaseChannel, "Warning", "RolloutTimeout", reason)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		newPhase := unleashv1.ReleaseChannelPhaseFailed
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.ActiveBatch = nil
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances: %v", err)
		// Record metrics for failure state
		labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	// A rollout that starts here rather than in Idle still needs a rollback
	// baseline. spec.image can change while the channel is already Rolling — a
	// first deploy now runs through this phase, and it holds the channel until
	// every batch is healthy — and Idle is not entered again in between. The
	// baseline is only captured while the fleet still agrees on the old image,
	// so calling this once a batch has moved on is a no-op.
	if _, err := r.ensurePreviousImageTracked(ctx, releaseChannel, targetInstances, log); err != nil {
		return ctrl.Result{RequeueAfter: releaseChannelErrorRetryDelay}, err
	}

	if releaseChannel.Status.ActiveBatch == nil {
		if recoveredBatch := recoverActiveBatch(targetInstances, releaseChannel); recoveredBatch != nil {
			log.Info("Recovered active batch from existing image assignments", "batchSize", len(recoveredBatch.InstanceNames))
			releaseChannel.Status.ActiveBatch = recoveredBatch
			return r.updateReleaseChannelStatus(ctx, releaseChannel)
		}
	}

	// Update instance counts
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
	r.recordMetrics(releaseChannel, labels)

	// Update status to persist instance counts using improved conflict handling
	if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
		log.V(1).Info("Failed to update ReleaseChannel status with instance counts", "error", statusErr)
		// Continue processing even if status update fails
	}

	if releaseChannel.Status.ActiveBatch != nil {
		batch := activeBatchInstances(targetInstances, releaseChannel.Status.ActiveBatch)
		if len(batch) == 0 {
			log.Info("Active batch contains no remaining instances, clearing it")
			releaseChannel.Status.ActiveBatch = nil
			return r.updateReleaseChannelStatus(ctx, releaseChannel)
		}

		targetImage := releaseChannel.Status.ActiveBatch.TargetImage
		if targetImage == "" {
			targetImage = string(releaseChannel.Spec.Image)
		}
		if !r.areInstancesReady(ctx, batch, targetImage, true, log) {
			log.Info("Active batch instances are not ready yet", "batchSize", len(batch))
			return ctrl.Result{RequeueAfter: r.getBackoffDuration(releaseChannel)}, nil
		}

		if releaseChannel.Spec.HealthChecks.Enabled {
			// The batch carries its own clock, which is more precise than
			// LastDeployTime for this path and survives operator restarts.
			healthy, err := r.performHealthChecks(
				ctx,
				batch,
				releaseChannel,
				&releaseChannel.Status.ActiveBatch.StartTime,
				log,
			)
			if err != nil {
				newPhase := releasePhaseOnFailure(releaseChannel)
				r.recordPhaseTransition(releaseChannel, newPhase)
				releaseChannel.Status.Phase = newPhase
				releaseChannel.Status.ActiveBatch = nil
				releaseChannel.Status.FailureReason = fmt.Sprintf("Rolling deployment health check failed: %v", err)
				r.recordMetrics(releaseChannel, labels)
				return r.updateReleaseChannelStatus(ctx, releaseChannel)
			}
			if !healthy {
				log.Info("Active batch health checks are not passing yet", "batchSize", len(batch))
				return ctrl.Result{RequeueAfter: releaseChannelRollingWaitDelay}, nil
			}
		}

		log.Info("Active batch completed successfully", "batchSize", len(batch))
		releaseChannel.Status.ActiveBatch = nil
		if _, err := r.updateReleaseChannelStatus(ctx, releaseChannel); err != nil {
			return ctrl.Result{}, err
		}

		batchInterval := releaseChannelBatchInterval
		if releaseChannel.Spec.Strategy.BatchInterval != nil {
			batchInterval = releaseChannel.Spec.Strategy.BatchInterval.Duration
		}
		return ctrl.Result{RequeueAfter: batchInterval}, nil
	}

	// Get instances that need updates (excluding already updated canary instances)
	instancesToUpdate := r.getInstancesToUpdate(targetInstances, releaseChannel)
	if len(instancesToUpdate) == 0 {
		if !r.areInstancesReady(ctx, targetInstances, string(releaseChannel.Spec.Image), true, log) {
			log.Info("Instances have the target image but are not ready yet")
			return ctrl.Result{RequeueAfter: r.getBackoffDuration(releaseChannel)}, nil
		}
		if releaseChannel.Spec.HealthChecks.Enabled {
			healthy, err := r.performHealthChecks(ctx, targetInstances, releaseChannel, settleReference(releaseChannel), log)
			if err != nil {
				newPhase := releasePhaseOnFailure(releaseChannel)
				r.recordPhaseTransition(releaseChannel, newPhase)
				releaseChannel.Status.Phase = newPhase
				releaseChannel.Status.FailureReason = fmt.Sprintf("Rolling deployment health check failed: %v", err)
				r.recordMetrics(releaseChannel, labels)
				return r.updateReleaseChannelStatus(ctx, releaseChannel)
			}
			if !healthy {
				log.Info("Instances have the target image but health checks are not passing yet")
				return ctrl.Result{RequeueAfter: releaseChannelRollingWaitDelay}, nil
			}
		}

		log.Info("All instances are up to date, completing rollout")
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseCompleted)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseCompleted
		// Record metrics for completion
		r.recordMetrics(releaseChannel, labels)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	log.Info("Processing rolling deployment", "instances", len(instancesToUpdate))

	// Apply maxParallel limits
	maxParallel := releaseChannel.Spec.Strategy.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}

	batchSize := min(len(instancesToUpdate), maxParallel)
	batch := instancesToUpdate[:batchSize]

	// Assigning the image and persisting ActiveBatch must happen together. Even
	// if InstanceImages already contains the target, this may be an operator
	// upgrade recovering a batch whose persisted state predates ActiveBatch.
	result, err := r.deployToInstances(ctx, releaseChannel, batch, log)
	if err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{}, err
		}
		newPhase := releasePhaseOnFailure(releaseChannel)
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.ActiveBatch = nil
		releaseChannel.Status.FailureReason = fmt.Sprintf("Rolling deployment failed: %v", err)
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return result, err
	}

	log.Info("Active batch assigned, waiting for instances to become ready", "batchSize", len(batch))
	return ctrl.Result{RequeueAfter: releaseChannelRollingWaitDelay}, nil
}

func (r *ReleaseChannelReconciler) executeRollingBackPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing rollback phase")
	releaseChannel.Status.ActiveBatch = nil

	// Determine rollback image: use spec override if provided, otherwise use tracked previous image
	rollbackImage := releaseChannel.Spec.Rollback.PreviousImage
	if rollbackImage == "" {
		// Automatic rollback: use the tracked previous image from status
		rollbackImage = releaseChannel.Status.PreviousImage
	}

	if rollbackImage == "" {
		log.Info("No previous image available for rollback, marking as failed")
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseFailed)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = "No previous image available for rollback (neither spec.rollback.previousImage nor status.previousImage is set)"
		// Record metrics for failure state
		labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
		r.recordMetrics(releaseChannel, labels)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	log.Info("Using rollback image", "rollbackImage", rollbackImage, "source", func() string {
		if releaseChannel.Spec.Rollback.PreviousImage != "" {
			return "spec.rollback.previousImage"
		}
		return "status.previousImage (automatic)"
	}())

	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseFailed)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances for rollback: %v", err)
		// Record metrics for failure state
		labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	// Update instance counts and record metrics
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name}
	r.recordMetrics(releaseChannel, labels)

	// Update InstanceImages map with rollback image for all instances
	// This ensures Unleash controllers will pull the rollback image
	if releaseChannel.Status.InstanceImages == nil {
		releaseChannel.Status.InstanceImages = make(map[string]string)
	}
	for _, instance := range targetInstances {
		if releaseChannel.Status.InstanceImages[instance.ObjectMeta.Name] != rollbackImage {
			releaseChannel.Status.InstanceImages[instance.ObjectMeta.Name] = rollbackImage
		}
	}
	releaseChannel.Status.InstanceImagesGeneration++

	// Persist the updated InstanceImages map
	if err := r.Status().Update(ctx, releaseChannel); err != nil {
		log.Error(err, "Failed to update ReleaseChannel status with rollback images")
		return ctrl.Result{RequeueAfter: releaseChannelErrorRetryDelay}, err
	}

	log.Info("Updated InstanceImages map with rollback image", "rollbackImage", rollbackImage)

	// Check if rollback is complete by verifying instances are using the rollback image
	rollbackComplete := r.areInstancesReady(ctx, targetInstances, rollbackImage, false, log)

	if !rollbackComplete {
		log.Info("Rollback still in progress, waiting for instances to update")
		// Record metrics for rollback in progress
		r.recordMetrics(releaseChannel, labels)
		return ctrl.Result{RequeueAfter: releaseChannelRollingBackWaitDelay}, nil
	}

	log.Info("Rollback completed successfully")
	// Clear PreviousImage since rollback is complete
	releaseChannel.Status.PreviousImage = ""
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
	// Reset transient retry state for future rollouts
	releaseChannel.Status.RetryCount = 0
	releaseChannel.Status.LastFailureTime = nil
	// Record metrics for successful rollback completion
	r.recordMetrics(releaseChannel, labels)
	return r.updateReleaseChannelStatus(ctx, releaseChannel)
}

// adoptUpToDateInstances records instances that already run the target image but
// have no InstanceImages entry, so the map covers every instance the channel
// manages. Entries that already exist are never touched — an in-flight batch owns
// those — and instances that still need updating are left to the rollout.
func (r *ReleaseChannelReconciler) adoptUpToDateInstances(
	releaseChannel *unleashv1.ReleaseChannel,
	targetInstances []unleashv1.Unleash,
	log logr.Logger,
) {
	targetImage := string(releaseChannel.Spec.Image)

	for _, instance := range targetInstances {
		if instance.Status.ResolvedReleaseChannelImage != targetImage {
			continue
		}
		if _, tracked := releaseChannel.Status.InstanceImages[instance.Name]; tracked {
			continue
		}

		if releaseChannel.Status.InstanceImages == nil {
			releaseChannel.Status.InstanceImages = make(map[string]string)
		}
		releaseChannel.Status.InstanceImages[instance.Name] = targetImage
		releaseChannel.Status.InstanceImagesGeneration++
		log.Info("Adopted untracked instance into InstanceImages", "instance", instance.Name, "image", targetImage)
	}
}

// ensurePreviousImageTracked captures the currently deployed image before starting a rollout.
// Critical for rollback: without this, automatic rollback has no image to revert to.
// Also sets LastImageChangeTime when a new image change is detected.
func (r *ReleaseChannelReconciler) ensurePreviousImageTracked(
	ctx context.Context,
	releaseChannel *unleashv1.ReleaseChannel,
	targetInstances []unleashv1.Unleash,
	log logr.Logger,
) (bool, error) {
	targetImage := releaseChannel.Spec.Image

	if len(targetInstances) == 0 {
		return false, nil
	}

	// Only an image the whole fleet agrees on is a usable rollback baseline.
	// Taking the first resolved image out of an unordered List picked a baseline
	// by List order whenever instances disagreed, which could send every instance
	// back to a version most of them never ran.
	currentDeployedImage := rollbackBaseline(targetInstances)
	if currentDeployedImage == "" && len(deployedImages(targetInstances)) > 1 {
		log.Info("Instances disagree on their deployed image, not capturing a rollback baseline",
			"deployedImages", deployedImages(targetInstances))
		return false, nil
	}

	log.V(1).Info("Image change detection",
		"currentDeployedImage", currentDeployedImage,
		"targetImage", string(targetImage),
		"existingPreviousImage", releaseChannel.Status.PreviousImage)

	// If deployed image differs from target and isn't already tracked, capture it.
	// Use retry to prevent losing PreviousImage on conflict — once instances are updated
	// to the target image, the window to capture PreviousImage closes permanently.
	if currentDeployedImage != "" &&
		currentDeployedImage != string(targetImage) &&
		releaseChannel.Status.PreviousImage != currentDeployedImage {

		captured := currentDeployedImage
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			key := types.NamespacedName{Name: releaseChannel.Name, Namespace: releaseChannel.Namespace}
			if err := r.Get(ctx, key, releaseChannel); err != nil {
				return err
			}
			if releaseChannel.Status.PreviousImage == captured {
				return nil
			}
			if captured == string(releaseChannel.Spec.Image) {
				return nil
			}
			now := metav1.Now()
			releaseChannel.Status.PreviousImage = captured
			releaseChannel.Status.LastImageChangeTime = &now
			return r.Status().Update(ctx, releaseChannel)
		})
		if err != nil {
			log.Error(err, "Failed to update ReleaseChannel status with previous image")
			return false, err
		}
		log.Info("Captured previous image for rollback",
			"previousImage", captured,
			"newTarget", string(targetImage))
		return true, nil
	}

	return false, nil
}

// shouldTriggerDeployment checks InstanceImages map to avoid duplicate deployments
// getBackoffDuration reduces controller load during wait periods via exponential backoff
func (r *ReleaseChannelReconciler) getBackoffDuration(releaseChannel *unleashv1.ReleaseChannel) time.Duration {
	// Base duration for waiting
	baseDuration := releaseChannelBackoffBase

	// Check how long we've been in the current phase to implement backoff
	if releaseChannel.Status.LastReconcileTime != nil {
		timeSinceLastReconcile := time.Since(releaseChannel.Status.LastReconcileTime.Time)

		// If we've been waiting for a while, increase the backoff
		if timeSinceLastReconcile > time.Minute*2 {
			return releaseChannelBackoffLong // Longer backoff if we've been waiting
		} else if timeSinceLastReconcile > time.Minute {
			return releaseChannelBackoffMedium // Medium backoff
		}
	}

	return baseDuration // Default backoff
}

// getTargetInstances returns instances referencing this ReleaseChannel.
// Excludes instances with CustomImage since they explicitly opt out of ReleaseChannel management.
func (r *ReleaseChannelReconciler) getTargetInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) ([]unleashv1.Unleash, error) {
	unleashList := &unleashv1.UnleashList{}
	if err := r.List(ctx, unleashList, client.InNamespace(releaseChannel.ObjectMeta.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Unleash instances: %w", err)
	}

	log := log.FromContext(ctx)
	log.V(1).Info("Listing all Unleash instances in namespace",
		"namespace", releaseChannel.ObjectMeta.Namespace,
		"totalFound", len(unleashList.Items),
		"lookingForRC", releaseChannel.ObjectMeta.Name)

	var targetInstances []unleashv1.Unleash
	for _, unleash := range unleashList.Items {
		log.V(1).Info("Checking Unleash instance",
			"name", unleash.ObjectMeta.Name,
			"releaseChannel", unleash.Spec.ReleaseChannel.Name,
			"customImage", unleash.Spec.CustomImage,
			"matches", unleash.Spec.ReleaseChannel.Name == releaseChannel.ObjectMeta.Name && unleash.Spec.CustomImage == "")
		// Only manage instances that reference this ReleaseChannel AND do not have CustomImage set
		if unleash.Spec.ReleaseChannel.Name == releaseChannel.ObjectMeta.Name && unleash.Spec.CustomImage == "" {
			targetInstances = append(targetInstances, unleash)
		}
	}

	log.V(1).Info("Finished filtering instances", "targetCount", len(targetInstances))

	return targetInstances, nil
}

func (r *ReleaseChannelReconciler) getCanaryInstances(instances []unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel) []unleashv1.Unleash {
	if !releaseChannel.Spec.Strategy.Canary.Enabled {
		return nil
	}

	selector := releaseChannel.Spec.Strategy.Canary.LabelSelector
	var canaryInstances []unleashv1.Unleash

	for _, instance := range instances {
		if r.matchesLabelSelector(instance, selector) {
			canaryInstances = append(canaryInstances, instance)
		}
	}

	return canaryInstances
}

func (r *ReleaseChannelReconciler) getInstancesToUpdate(instances []unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel) []unleashv1.Unleash {
	targetImage := string(releaseChannel.Spec.Image)
	var instancesToUpdate []unleashv1.Unleash

	for _, instance := range instances {
		// Since we exclude instances with CustomImage, we check the resolved ReleaseChannel image
		if instance.Status.ResolvedReleaseChannelImage != targetImage {
			instancesToUpdate = append(instancesToUpdate, instance)
		}
	}

	return instancesToUpdate
}

func activeBatchInstances(instances []unleashv1.Unleash, batch *unleashv1.ReleaseChannelActiveBatch) []unleashv1.Unleash {
	names := make(map[string]struct{}, len(batch.InstanceNames))
	for _, name := range batch.InstanceNames {
		names[name] = struct{}{}
	}

	active := make([]unleashv1.Unleash, 0, len(batch.InstanceNames))
	for _, instance := range instances {
		if _, ok := names[instance.Name]; ok {
			active = append(active, instance)
		}
	}
	return active
}

// recoverActiveBatch preserves rollout safety when upgrading from a controller
// version that assigned InstanceImages before ActiveBatch existed.
func recoverActiveBatch(instances []unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel) *unleashv1.ReleaseChannelActiveBatch {
	targetImage := string(releaseChannel.Spec.Image)
	var instanceNames []string

	for _, instance := range instances {
		if releaseChannel.Status.InstanceImages[instance.Name] != targetImage {
			continue
		}
		if instance.Status.ResolvedReleaseChannelImage != targetImage ||
			!instanceReady(&instance) ||
			!instanceConnected(&instance) {
			instanceNames = append(instanceNames, instance.Name)
		}
	}
	if len(instanceNames) == 0 {
		return nil
	}

	startTime := metav1.Now()
	if releaseChannel.Status.StartTime != nil {
		startTime = *releaseChannel.Status.StartTime
	}
	return &unleashv1.ReleaseChannelActiveBatch{
		InstanceNames: instanceNames,
		TargetImage:   targetImage,
		StartTime:     startTime,
	}
}

// deployToInstances updates InstanceImages map - Unleash controllers pull from this (unidirectional)
func (r *ReleaseChannelReconciler) deployToInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, instances []unleashv1.Unleash, log logr.Logger) (ctrl.Result, error) {
	log.Info("Coordinating deployment by updating InstanceImages map", "instances", len(instances), "phase", releaseChannel.Status.Phase)

	// Build the desired instance images map from instances
	desiredImages := make(map[string]string)
	for _, instance := range instances {
		targetImage := r.getExpectedImageForInstance(ctx, &instance, string(releaseChannel.Spec.Image))
		desiredImages[instance.ObjectMeta.Name] = targetImage
	}

	// Use RetryOnConflict for robust conflict handling
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch fresh copy
		fresh := &unleashv1.ReleaseChannel{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(releaseChannel), fresh); err != nil {
			return err
		}

		// Initialize maps if needed
		if fresh.Status.InstanceImages == nil {
			fresh.Status.InstanceImages = make(map[string]string)
		}
		if fresh.Status.LastTargetImages == nil {
			fresh.Status.LastTargetImages = make(map[string]string)
		}
		if fresh.Status.Phase == unleashv1.ReleaseChannelPhaseRolling && fresh.Status.ActiveBatch == nil {
			instanceNames := make([]string, 0, len(instances))
			for _, instance := range instances {
				instanceNames = append(instanceNames, instance.Name)
			}
			fresh.Status.ActiveBatch = &unleashv1.ReleaseChannelActiveBatch{
				InstanceNames: instanceNames,
				TargetImage:   string(fresh.Spec.Image),
				StartTime:     metav1.Now(),
			}
		}

		// Track if any changes were made
		changesDetected := false

		// Apply desired images to fresh copy
		for name, targetImage := range desiredImages {
			if fresh.Status.InstanceImages[name] != targetImage {
				fresh.Status.InstanceImages[name] = targetImage
				changesDetected = true
				log.Info("Set desired image for instance in ReleaseChannel", "instance", name, "image", targetImage)
			}
			fresh.Status.LastTargetImages[name] = targetImage
		}

		// Increment generation if changes were made
		if changesDetected {
			fresh.Status.InstanceImagesGeneration++
			// Stamp when these instances were handed their image. The health
			// check settle delay is measured from here so it applies to every
			// deploy, not just the first one of a rollout.
			now := metav1.Now()
			fresh.Status.LastDeployTime = &now
			log.Info("Incremented InstanceImagesGeneration", "generation", fresh.Status.InstanceImagesGeneration)
		}

		err := r.Status().Update(ctx, fresh)
		if err == nil {
			*releaseChannel = *fresh
		} else if apierrors.IsConflict(err) {
			log.V(1).Info("Resource conflict when updating InstanceImages, retrying", "error", err)
			releaseChannelConflicts.WithLabelValues(releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name).Inc()
		}
		return err
	})

	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ReleaseChannel status: %w", err)
	}

	log.Info("Updated ReleaseChannel InstanceImages map", "instanceCount", len(instances))
	return ctrl.Result{}, nil
}

func (r *ReleaseChannelReconciler) areInstancesReady(ctx context.Context, instances []unleashv1.Unleash, targetImage string, requireConnected bool, log logr.Logger) bool {
	for _, instance := range instances {
		// Re-fetch instance to get current status
		currentInstance := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: instance.ObjectMeta.Name, Namespace: instance.ObjectMeta.Namespace}, currentInstance); err != nil {
			log.Error(err, "Failed to get instance status", "name", instance.ObjectMeta.Name)
			return false
		}

		// Check if instance has the expected image (via ResolvedReleaseChannelImage)
		// The Unleash controller should have resolved the correct image based on ReleaseChannel phase
		expectedImage := r.getExpectedImageForInstance(ctx, currentInstance, targetImage)
		if currentInstance.Status.ResolvedReleaseChannelImage != expectedImage {
			log.V(1).Info("Instance not updated yet", "name", instance.ObjectMeta.Name,
				"resolved", currentInstance.Status.ResolvedReleaseChannelImage,
				"expected", expectedImage)
			return false
		}

		// Check if instance is ready
		if !r.isInstanceReady(currentInstance) {
			log.V(1).Info("Instance not ready yet", "name", instance.ObjectMeta.Name)
			return false
		}
		if requireConnected && !r.isInstanceConnected(currentInstance) {
			log.V(1).Info("Instance not connected yet", "name", instance.ObjectMeta.Name)
			return false
		}
	}

	return true
}

// getExpectedImageForInstance determines correct image based on rollout phase.
// During canary: only canary instances get new image. During rollback: all get previous image.
func (r *ReleaseChannelReconciler) getExpectedImageForInstance(ctx context.Context, instance *unleashv1.Unleash, targetImage string) string {
	// During a rollout, we need to check which image this instance should have based on the phase
	// This should match the logic in the Unleash controller's getImageForInstance function

	// Get the ReleaseChannel to check the current phase and previous image
	releaseChannelName := instance.Spec.ReleaseChannel.Name
	if releaseChannelName == "" {
		// If no release channel, expect the target image
		return targetImage
	}

	releaseChannel := &unleashv1.ReleaseChannel{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      releaseChannelName,
		Namespace: instance.ObjectMeta.Namespace,
	}, releaseChannel)
	if err != nil {
		// If we can't get the release channel, expect the target image
		return targetImage
	}

	if releaseChannel.Status.Phase == unleashv1.ReleaseChannelPhaseRollingBack &&
		releaseChannel.Spec.Rollback.PreviousImage != "" {
		return releaseChannel.Spec.Rollback.PreviousImage
	}

	previousImage := string(releaseChannel.Status.PreviousImage)

	// No rollout in progress, all instances get the target image
	if previousImage == "" {
		return targetImage
	}

	// A rollout is in progress (PreviousImage is set)
	isCanary := releaseChannel.Spec.Strategy.Canary.Enabled && r.matchesLabelSelector(*instance, releaseChannel.Spec.Strategy.Canary.LabelSelector)

	switch releaseChannel.Status.Phase {
	case unleashv1.ReleaseChannelPhaseCanary:
		if isCanary {
			return targetImage
		}
		return previousImage
	case unleashv1.ReleaseChannelPhaseRolling, unleashv1.ReleaseChannelPhaseCompleted:
		// During rolling and completed phases, all instances should be on the target image
		return targetImage
	case unleashv1.ReleaseChannelPhaseRollingBack:
		// During rollback, all instances should be on the previous image
		return previousImage
	default:
		// For any other phase, expect the target image
		return targetImage
	}
}

func (r *ReleaseChannelReconciler) isInstanceReady(instance *unleashv1.Unleash) bool {
	return instanceReady(instance)
}

func instanceReady(instance *unleashv1.Unleash) bool {
	// Check for Ready condition
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

func (r *ReleaseChannelReconciler) isInstanceConnected(instance *unleashv1.Unleash) bool {
	return instanceConnected(instance)
}

func instanceConnected(instance *unleashv1.Unleash) bool {
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeConnected {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

// settleReference returns the timestamp the health check settle delay is measured
// from: when instances were last handed a new image. It falls back to the start
// of the rollout for rollouts that were already in flight when this field was
// introduced, so an operator upgrade does not drop the guard entirely.
func settleReference(releaseChannel *unleashv1.ReleaseChannel) *metav1.Time {
	if releaseChannel.Status.LastDeployTime != nil {
		return releaseChannel.Status.LastDeployTime
	}
	return releaseChannel.Status.StartTime
}

func (r *ReleaseChannelReconciler) performHealthChecks(ctx context.Context, instances []unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel, startTime *metav1.Time, log logr.Logger) (bool, error) {
	// Wait for initial delay if configured
	initialDelay := releaseChannelHealthCheckInitialDelay
	if releaseChannel.Spec.HealthChecks.InitialDelay != nil {
		initialDelay = releaseChannel.Spec.HealthChecks.InitialDelay.Duration
	}

	// Check if we're still in initial delay period
	if startTime != nil {
		elapsed := time.Since(startTime.Time)
		if elapsed < initialDelay {
			log.V(1).Info("Still in initial delay period", "elapsed", elapsed, "delay", initialDelay)
			return false, nil
		}
	}

	for _, instance := range instances {
		// Re-fetch to get latest status
		currentInstance := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: instance.ObjectMeta.Name, Namespace: instance.ObjectMeta.Namespace}, currentInstance); err != nil {
			return false, fmt.Errorf("failed to get instance %s: %w", instance.ObjectMeta.Name, err)
		}

		// Check for explicit failure conditions first
		for _, condition := range currentInstance.Status.Conditions {
			if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled &&
				condition.Status == metav1.ConditionFalse &&
				condition.Reason == "Failed" {
				// Record failed health check
				releaseChannelHealthChecks.WithLabelValues(releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name, "failed").Inc()
				return false, fmt.Errorf("instance %s failed: %s", instance.ObjectMeta.Name, condition.Message)
			}
		}

		// Check if instance is connected (healthy) via Kubernetes conditions
		connected := false
		for _, condition := range currentInstance.Status.Conditions {
			if condition.Type == unleashv1.UnleashStatusConditionTypeConnected {
				connected = condition.Status == metav1.ConditionTrue
				break
			}
		}

		if !connected {
			log.V(1).Info("Instance not connected/healthy yet", "name", instance.ObjectMeta.Name)
			return false, nil
		}

		// Perform custom HTTP health check if endpoint is configured
		if releaseChannel.Spec.HealthChecks.Endpoint != "" {
			healthy, err := r.performHTTPHealthCheck(ctx, currentInstance, releaseChannel.Spec.HealthChecks.Endpoint, log)
			if err != nil {
				releaseChannelHealthChecks.WithLabelValues(releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name, "failed").Inc()
				return false, fmt.Errorf("HTTP health check failed for instance %s: %w", instance.ObjectMeta.Name, err)
			}
			if !healthy {
				log.V(1).Info("Instance HTTP health check not passing", "name", instance.ObjectMeta.Name, "endpoint", releaseChannel.Spec.HealthChecks.Endpoint)
				return false, nil
			}
		}
	}

	log.Info("All instances passed health checks")
	// Record successful health check
	releaseChannelHealthChecks.WithLabelValues(releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name, "success").Inc()
	return true, nil
}

func (r *ReleaseChannelReconciler) performHTTPHealthCheck(ctx context.Context, instance *unleashv1.Unleash, endpoint string, log logr.Logger) (bool, error) {
	url := instance.URL() + endpoint
	log.V(1).Info("Performing HTTP health check", "instance", instance.ObjectMeta.Name, "url", url)

	client := &http.Client{
		Timeout: releaseChannelHealthCheckTimeout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.V(1).Info("HTTP health check request failed", "instance", instance.ObjectMeta.Name, "error", err)
		return false, nil // Return false but no error to allow retry
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.V(1).Info("HTTP health check returned non-success status", "instance", instance.ObjectMeta.Name, "status", resp.StatusCode)
		return false, nil // Return false but no error to allow retry
	}

	log.V(1).Info("HTTP health check passed", "instance", instance.ObjectMeta.Name, "status", resp.StatusCode)
	return true, nil
}

func (r *ReleaseChannelReconciler) updateInstanceCounts(releaseChannel *unleashv1.ReleaseChannel, targetInstances []unleashv1.Unleash) {
	releaseChannel.Status.Instances = len(targetInstances)

	targetImage := string(releaseChannel.Spec.Image)
	upToDateCount := 0
	canaryCount := 0
	canaryUpToDateCount := 0

	// Track version from up-to-date instances
	var resolvedVersion string

	// Build set of current target instance names for stale entry cleanup
	currentInstanceNames := make(map[string]struct{}, len(targetInstances))

	for _, instance := range targetInstances {
		currentInstanceNames[instance.ObjectMeta.Name] = struct{}{}

		isCanary := releaseChannel.Spec.Strategy.Canary.Enabled &&
			r.matchesLabelSelector(instance, releaseChannel.Spec.Strategy.Canary.LabelSelector)

		if isCanary {
			canaryCount++
			if instance.Status.ResolvedReleaseChannelImage == targetImage {
				canaryUpToDateCount++
			}
		}

		if instance.Status.ResolvedReleaseChannelImage == targetImage {
			upToDateCount++
			// Capture version from up-to-date instances, but only if the instance
			// was actually managed by THIS ReleaseChannel (not switching from another).
			// This prevents version downgrades when instances switch between channels.
			if resolvedVersion == "" && instance.Status.Version != "" &&
				instance.Status.ReleaseChannelName == releaseChannel.ObjectMeta.Name {
				resolvedVersion = instance.Status.Version
			}
		}
	}

	releaseChannel.Status.InstancesUpToDate = upToDateCount
	releaseChannel.Status.CanaryInstances = canaryCount
	releaseChannel.Status.CanaryInstancesUpToDate = canaryUpToDateCount

	// Update Version from Unleash instances (only if we have a valid version)
	if resolvedVersion != "" {
		releaseChannel.Status.Version = resolvedVersion
	}

	// Set Rollout (completed) flag: true when all instances are up-to-date
	releaseChannel.Status.Rollout = upToDateCount == len(targetInstances) && len(targetInstances) > 0

	// Calculate progress
	if len(targetInstances) > 0 {
		releaseChannel.Status.Progress = (upToDateCount * 100) / len(targetInstances)
	} else {
		releaseChannel.Status.Progress = 100
	}

	// Clean stale InstanceImages entries for instances that no longer exist
	// Only remove entries for deleted instances, not instances that changed ReleaseChannel
	if releaseChannel.Status.InstanceImages != nil {
		for instanceName := range releaseChannel.Status.InstanceImages {
			if _, exists := currentInstanceNames[instanceName]; !exists {
				delete(releaseChannel.Status.InstanceImages, instanceName)
			}
		}
	}

	// Clean stale LastTargetImages entries for consistency
	if releaseChannel.Status.LastTargetImages != nil {
		for instanceName := range releaseChannel.Status.LastTargetImages {
			if _, exists := currentInstanceNames[instanceName]; !exists {
				delete(releaseChannel.Status.LastTargetImages, instanceName)
			}
		}
	}

	// Update condition based on current phase and rollout state
	r.updateConditionForPhase(releaseChannel)
}

func (r *ReleaseChannelReconciler) matchesLabelSelector(instance unleashv1.Unleash, selector metav1.LabelSelector) bool {
	// Convert the selector to a labels.Selector
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		// Handle the error, e.g., by logging it and returning false
		// In a real-world scenario, you might want to log this error
		return false
	}

	// Create a label set from the instance's labels
	instanceLabels := labels.Set(instance.ObjectMeta.Labels)

	// Check if the selector matches the instance's labels
	return sel.Matches(instanceLabels)
}

// rollbackTarget returns the image a rollback would move instances to, or "" when
// there is none. Resolution order matches executeRollingBackPhase.
func rollbackTarget(releaseChannel *unleashv1.ReleaseChannel) string {
	if releaseChannel.Spec.Rollback.PreviousImage != "" {
		return releaseChannel.Spec.Rollback.PreviousImage
	}
	return releaseChannel.Status.PreviousImage
}

// releasePhaseOnFailure picks the phase a failed rollout moves to. Rollback is
// only chosen when there is somewhere to roll back to: routing a channel with no
// baseline into RollingBack sent it somewhere it could not act, and the failure
// it reported on arrival replaced the reason the rollout actually failed.
func releasePhaseOnFailure(releaseChannel *unleashv1.ReleaseChannel) unleashv1.ReleaseChannelPhase {
	if releaseChannel.Spec.Rollback.Enabled &&
		releaseChannel.Spec.Rollback.OnFailure &&
		rollbackTarget(releaseChannel) != "" {
		return unleashv1.ReleaseChannelPhaseRollingBack
	}
	return unleashv1.ReleaseChannelPhaseFailed
}

// batchAllowance is how long a single batch is given. It is the sum of what a
// batch actually waits for: the settle delay before health checks start, the
// operator's own bound on how long verifying a batch may take, and the pause
// before the next batch begins. Summing the maxima makes this a worst case,
// which is what a timeout budget wants — too tight fails healthy rollouts,
// while too generous only delays detection, and the cap bounds that.
func batchAllowance(releaseChannel *unleashv1.ReleaseChannel) time.Duration {
	settle := releaseChannelHealthCheckInitialDelay
	if releaseChannel.Spec.HealthChecks.InitialDelay != nil {
		settle = releaseChannel.Spec.HealthChecks.InitialDelay.Duration
	}

	// Applied even when health checks are disabled: instances still have to
	// become ready and connected before a batch completes, and this is the only
	// bound an operator declares on how long one batch may take.
	verify := releaseChannelDefaultHealthCheckTimeout
	if releaseChannel.Spec.HealthChecks.Timeout != nil {
		verify = releaseChannel.Spec.HealthChecks.Timeout.Duration
	}

	interval := releaseChannelBatchInterval
	if releaseChannel.Spec.Strategy.BatchInterval != nil {
		interval = releaseChannel.Spec.Strategy.BatchInterval.Duration
	}

	return settle + verify + interval
}

// upgradeTimeBudget returns how long a rollout has to finish, and how that
// number was arrived at. An empty derivation means the operator pinned the
// value in spec.strategy.maxUpgradeTime and it is used exactly as given.
//
// Otherwise the budget scales with the work: a flat wall clock is wrong the
// moment fleet size or maxParallel changes, since the same setting is generous
// for three instances and impossible for sixty-five. It is floored at the old
// flat default so small fleets never get a tighter budget than before, and
// capped so a large fleet cannot make the timeout effectively infinite.
func upgradeTimeBudget(releaseChannel *unleashv1.ReleaseChannel) (time.Duration, string) {
	if releaseChannel.Spec.Strategy.MaxUpgradeTime != nil {
		return releaseChannel.Spec.Strategy.MaxUpgradeTime.Duration, ""
	}

	maxParallel := releaseChannel.Spec.Strategy.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}

	instances := releaseChannel.Status.Instances
	if instances < 0 {
		instances = 0
	}
	batches := (instances + maxParallel - 1) / maxParallel

	allowance := batchAllowance(releaseChannel)
	budget := time.Duration(batches) * allowance

	if budget < releaseChannelDefaultMaxUpgradeTime {
		budget = releaseChannelDefaultMaxUpgradeTime
	}
	if budget > releaseChannelMaxDerivedUpgradeTime {
		budget = releaseChannelMaxDerivedUpgradeTime
	}

	return budget, fmt.Sprintf("derived from %d batch(es) of %s for %d instance(s) at maxParallel %d, capped at %s",
		batches, allowance, instances, maxParallel, releaseChannelMaxDerivedUpgradeTime)
}

func (r *ReleaseChannelReconciler) checkMaxUpgradeTimeExceeded(releaseChannel *unleashv1.ReleaseChannel) (bool, string) {
	if releaseChannel.Status.StartTime == nil {
		return false, ""
	}

	maxUpgradeTime, derivation := upgradeTimeBudget(releaseChannel)

	elapsed := time.Since(releaseChannel.Status.StartTime.Time)
	if elapsed <= maxUpgradeTime {
		return false, ""
	}

	// An operator looking at a limit they never configured needs to see where
	// the number came from, and how to pin it if the answer is wrong.
	if derivation != "" {
		return true, fmt.Sprintf("Rollout exceeded maxUpgradeTime (%s elapsed, limit %s %s; set spec.strategy.maxUpgradeTime to override)",
			elapsed.Round(time.Second), maxUpgradeTime, derivation)
	}
	return true, fmt.Sprintf("Rollout exceeded maxUpgradeTime (%s elapsed, limit %s)", elapsed.Round(time.Second), maxUpgradeTime)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *ReleaseChannelReconciler) recordPhaseTransition(releaseChannel *unleashv1.ReleaseChannel, newPhase unleashv1.ReleaseChannelPhase) {
	labels := []string{
		releaseChannel.ObjectMeta.Namespace,
		releaseChannel.ObjectMeta.Name,
		string(newPhase),
	}
	releaseChannelPhaseTransitions.WithLabelValues(labels...).Inc()
}

// updateReleaseChannelStatus persists status with change detection to avoid update loops.
// Status updates trigger watch events; without change detection we'd reconcile endlessly.
func (r *ReleaseChannelReconciler) updateReleaseChannelStatus(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Store the status values we want to persist
	statusToApply := releaseChannel.Status.DeepCopy()

	// Use RetryOnConflict for robust conflict handling with exponential backoff
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch fresh copy to compare status and avoid unnecessary updates
		fresh := &unleashv1.ReleaseChannel{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(releaseChannel), fresh); err != nil {
			return err
		}

		// Compare status (ignoring LastTransitionTime in conditions)
		if releaseChannelStatusEqual(&fresh.Status, statusToApply) {
			log.V(1).Info("Status unchanged, skipping update", "phase", statusToApply.Phase)
			*releaseChannel = *fresh
			return nil
		}

		// Apply our status to the fresh copy
		fresh.Status = *statusToApply
		err := r.Status().Update(ctx, fresh)
		if err == nil {
			log.V(1).Info("Successfully updated ReleaseChannel status", "instances", statusToApply.Instances, "upToDate", statusToApply.InstancesUpToDate)
			*releaseChannel = *fresh
		} else if apierrors.IsConflict(err) {
			log.V(1).Info("Resource conflict during status update, retrying", "error", err)
			releaseChannelConflicts.WithLabelValues(releaseChannel.ObjectMeta.Namespace, releaseChannel.ObjectMeta.Name).Inc()
		}
		return err
	})

	if err != nil {
		log.V(1).Info("Failed to update ReleaseChannel status", "error", err, "instances", statusToApply.Instances)
		return ctrl.Result{}, fmt.Errorf("failed to update ReleaseChannel status: %w", err)
	}

	return ctrl.Result{RequeueAfter: releaseChannelStatusUpdateSuccess}, nil
}

func (r *ReleaseChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Tracer = otel.Tracer("github.com/nais/unleasherator/internal/controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ReleaseChannel{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Prevent concurrent reconciles that could cause race conditions
		}).
		Watches(
			&unleashv1.Unleash{},
			handler.EnqueueRequestsFromMapFunc(r.findReleaseChannelsForUnleash),
			builder.WithPredicates(predicate.Funcs{
				// React to create events so ReleaseChannel discovers new instances immediately
				CreateFunc: func(e event.CreateEvent) bool {
					unleash, ok := e.Object.(*unleashv1.Unleash)
					if !ok {
						return false
					}
					// Only trigger if the instance references a ReleaseChannel
					return unleash.Spec.ReleaseChannel.Name != ""
				},
				// React to Unleash status changes that affect ReleaseChannel coordination:
				// - ResolvedImage changes: allows ReleaseChannel to detect when instances are up-to-date
				// - Conditions changes: allows ReleaseChannel to detect readiness state
				UpdateFunc: func(e event.UpdateEvent) bool {
					oldUnleash, oldOk := e.ObjectOld.(*unleashv1.Unleash)
					newUnleash, newOk := e.ObjectNew.(*unleashv1.Unleash)
					if !oldOk || !newOk {
						return false
					}

					// Only trigger for instances that reference a ReleaseChannel
					if newUnleash.Spec.ReleaseChannel.Name == "" {
						return false
					}

					// Trigger if ResolvedReleaseChannelImage changed - this is critical for detecting
					// when Unleash instances have pulled and applied the new image
					if oldUnleash.Status.ResolvedReleaseChannelImage != newUnleash.Status.ResolvedReleaseChannelImage {
						return true
					}

					// Trigger if conditions changed (readiness state)
					return !conditionsEqual(oldUnleash.Status.Conditions, newUnleash.Status.Conditions)
				},
				// Don't react to delete events
				DeleteFunc: func(e event.DeleteEvent) bool {
					return false
				},
			}),
		).
		Complete(r)
}

func (r *ReleaseChannelReconciler) findReleaseChannelsForUnleash(ctx context.Context, unleash client.Object) []reconcile.Request {
	log := log.FromContext(ctx)

	unleashInstance, ok := unleash.(*unleashv1.Unleash)
	if !ok {
		log.Error(fmt.Errorf("expected an Unleash object, but got %T", unleash), "failed to cast object")
		return nil
	}

	if unleashInstance.Spec.ReleaseChannel.Name == "" {
		return nil
	}

	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      unleashInstance.Spec.ReleaseChannel.Name,
				Namespace: unleashInstance.ObjectMeta.Namespace,
			},
		},
	}
}

// conditionsEqual ignores LastTransitionTime which changes even when condition content is same
func conditionsEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}

	aMap := make(map[string]metav1.Condition)
	for _, cond := range a {
		aMap[cond.Type] = cond
	}

	for _, condB := range b {
		condA, exists := aMap[condB.Type]
		if !exists {
			return false
		}
		// Compare relevant fields, ignoring LastTransitionTime
		if condA.Type != condB.Type ||
			condA.Status != condB.Status ||
			condA.Reason != condB.Reason ||
			condA.Message != condB.Message {
			return false
		}
	}

	return true
}

// releaseChannelStatusEqual compares status objects for change detection.
// Ignores volatile time fields (LastReconcileTime, StartTime, EstimatedCompletion) which change
// frequently but don't represent meaningful state. LastImageChangeTime IS compared since it
// represents a significant event (spec.image change).
func releaseChannelStatusEqual(a, b *unleashv1.ReleaseChannelStatus) bool {
	// Compare all non-time fields
	if a.Phase != b.Phase {
		return false
	}
	if a.Version != b.Version {
		return false
	}
	if a.Rollout != b.Rollout {
		return false
	}
	if a.Progress != b.Progress {
		return false
	}
	if a.Instances != b.Instances {
		return false
	}
	if a.CanaryInstances != b.CanaryInstances {
		return false
	}
	if a.InstancesUpToDate != b.InstancesUpToDate {
		return false
	}
	if a.CanaryInstancesUpToDate != b.CanaryInstancesUpToDate {
		return false
	}
	if a.FailureReason != b.FailureReason {
		return false
	}
	if a.RetryCount != b.RetryCount {
		return false
	}
	// Compare LastFailureTime: detect if one is nil and the other is not
	if (a.LastFailureTime == nil) != (b.LastFailureTime == nil) {
		return false
	}
	if a.LastFailureTime != nil && b.LastFailureTime != nil {
		if !a.LastFailureTime.Equal(b.LastFailureTime) {
			return false
		}
	}
	if a.PreviousImage != b.PreviousImage {
		return false
	}
	if a.FailedImage != b.FailedImage {
		return false
	}
	// LastDeployTime is compared despite being a timestamp: it drives the health
	// check settle delay, so treating a status that only changes it as unchanged
	// would skip the write and then overwrite the in-memory value with the stale
	// one from the API server.
	if (a.LastDeployTime == nil) != (b.LastDeployTime == nil) {
		return false
	}
	if a.LastDeployTime != nil && b.LastDeployTime != nil {
		if !a.LastDeployTime.Equal(b.LastDeployTime) {
			return false
		}
	}
	if !reflect.DeepEqual(a.ActiveBatch, b.ActiveBatch) {
		return false
	}
	if a.InstanceImagesGeneration != b.InstanceImagesGeneration {
		return false
	}
	// Compare maps
	if !reflect.DeepEqual(a.InstanceImages, b.InstanceImages) {
		return false
	}
	if !reflect.DeepEqual(a.LastTargetImages, b.LastTargetImages) {
		return false
	}
	if !reflect.DeepEqual(a.InstanceStatus, b.InstanceStatus) {
		return false
	}
	// Compare conditions (ignoring LastTransitionTime)
	if !conditionsEqual(a.Conditions, b.Conditions) {
		return false
	}
	// Compare LastImageChangeTime: detect if one is nil and the other is not
	if (a.LastImageChangeTime == nil) != (b.LastImageChangeTime == nil) {
		return false
	}
	// Both are non-nil, compare actual values
	if a.LastImageChangeTime != nil && b.LastImageChangeTime != nil {
		if !a.LastImageChangeTime.Equal(b.LastImageChangeTime) {
			return false
		}
	}
	// Deliberately ignore time fields: LastReconcileTime, StartTime, EstimatedCompletion
	// These change frequently and don't represent meaningful state changes
	return true
}
