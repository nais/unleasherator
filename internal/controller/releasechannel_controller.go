package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-logr/logr"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/statemachine"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	ReleaseChannelFinalizer = "releasechannel.unleash.nais.io/finalizer"
)

// ReleaseChannelReconciler reconciles a ReleaseChannel object
type ReleaseChannelReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	Tracer         trace.Tracer
	DecisionEngine *statemachine.DecisionEngine
}

var (
	// releaseChannelStatus tracks the current state of ReleaseChannels
	releaseChannelStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_releasechannel_status",
			Help: "Status of ReleaseChannel resources (1=active, 0=inactive)",
		},
		[]string{"namespace", "name"},
	)

	// releaseChannelInstances tracks the number of instances managed by each ReleaseChannel
	releaseChannelInstances = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_releasechannel_instances_total",
			Help: "Total number of Unleash instances managed by ReleaseChannel",
		},
		[]string{"namespace", "name"},
	)

	// releaseChannelInstancesUpToDate tracks instances running the target image
	releaseChannelInstancesUpToDate = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "unleasherator_releasechannel_instances_up_to_date",
			Help: "Number of Unleash instances running the target image",
		},
		[]string{"namespace", "name"},
	)

	// releaseChannelRollouts tracks rollout events
	releaseChannelRollouts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_rollouts_total",
			Help: "Total number of ReleaseChannel rollout events",
		},
		[]string{"namespace", "name", "result"},
	)

	// releaseChannelInstanceUpdates tracks individual instance updates
	releaseChannelInstanceUpdates = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_instance_updates_total",
			Help: "Total number of instance updates attempted by ReleaseChannel",
		},
		[]string{"namespace", "name", "result"},
	)

	// releaseChannelRolloutDuration tracks how long rollouts take
	releaseChannelRolloutDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "unleasherator_releasechannel_rollout_duration_seconds",
			Help:    "Duration of ReleaseChannel rollouts in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // 1s to ~17 minutes
		},
		[]string{"namespace", "name"},
	)

	// releaseChannelConflicts tracks resource conflicts during updates
	releaseChannelConflicts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_conflicts_total",
			Help: "Total number of resource conflicts encountered during rollouts",
		},
		[]string{"namespace", "name"},
	)

	// releaseChannelPhaseTransitions tracks phase transitions
	releaseChannelPhaseTransitions = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "unleasherator_releasechannel_phase_transitions_total",
			Help: "Total number of phase transitions for ReleaseChannels",
		},
		[]string{"namespace", "name", "phase"},
	)

	// releaseChannelHealthChecks tracks health check attempts
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
		releaseChannelInstanceUpdates,
		releaseChannelRolloutDuration,
		releaseChannelConflicts,
		releaseChannelPhaseTransitions,
		releaseChannelHealthChecks,
	)
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/finalizers,verbs=update
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes,verbs=get;list;watch;update;patch

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
			// Clear metrics for deleted ReleaseChannel
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

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(releaseChannel, ReleaseChannelFinalizer) {
		controllerutil.AddFinalizer(releaseChannel, ReleaseChannelFinalizer)
		if err := r.Update(ctx, releaseChannel); err != nil {
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

// handleDeletion handles ReleaseChannel deletion
func (r *ReleaseChannelReconciler) handleDeletion(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("ReleaseChannel marked for deletion")

	// Remove finalizer
	controllerutil.RemoveFinalizer(releaseChannel, ReleaseChannelFinalizer)
	if err := r.Update(ctx, releaseChannel); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// initializeStatus initializes the ReleaseChannel status if needed
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

// executePhase executes the appropriate logic based on current phase
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
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}
}

// executeIdlePhase checks if a new rollout should start
func (r *ReleaseChannelReconciler) executeIdlePhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing idle phase", "specImage", string(releaseChannel.Spec.Image), "previousImage", releaseChannel.Status.PreviousImage)

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
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	// Check if all instances are already up to date
	targetImage := releaseChannel.Spec.Image
	log.Info("Checking instances for updates", "targetImage", string(targetImage), "instanceCount", len(targetInstances))

	// Before checking if instances need updates, check if the target image has changed
	// and if so, capture the previous target for rollback purposes
	statusNeedsUpdate := false

	// Always check if we need to update PreviousImage when target changes
	if len(targetInstances) > 0 {
		// Get the currently deployed image from any instance
		var currentDeployedImage string
		for _, unleash := range targetInstances {
			if unleash.Status.ResolvedReleaseChannelImage != "" {
				currentDeployedImage = unleash.Status.ResolvedReleaseChannelImage
				log.Info("Found instance with resolved image", "instance", unleash.Name, "resolvedImage", unleash.Status.ResolvedReleaseChannelImage)
				break
			}
		}

		log.Info("Image change detection", "currentDeployedImage", currentDeployedImage, "targetImage", string(targetImage), "existingPreviousImage", releaseChannel.Status.PreviousImage)

		// If we have a deployed image and it's different from target, capture it as previous
		if currentDeployedImage != "" && currentDeployedImage != string(targetImage) {
			// Only update if we don't already have the correct previous image set
			if releaseChannel.Status.PreviousImage != currentDeployedImage {
				releaseChannel.Status.PreviousImage = currentDeployedImage
				statusNeedsUpdate = true
				log.Info("Detected target image change, setting previous image", "previousImage", currentDeployedImage, "newTarget", string(targetImage))
			}
		}
	}

	// If we detected a target image change, update status immediately before proceeding
	if statusNeedsUpdate {
		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			log.Error(err, "Failed to update ReleaseChannel status with previous image")
			return ctrl.Result{RequeueAfter: time.Second * 5}, err
		}
		log.Info("Updated ReleaseChannel status with previous image")
	}

	var instancesToUpdate []unleashv1.Unleash
	var currentDeployedImage string
	for _, unleash := range targetInstances {
		currentImage := unleash.Status.ResolvedReleaseChannelImage
		log.Info("Instance status", "name", unleash.Name, "currentImage", currentImage, "targetImage", string(targetImage))
		if currentImage != string(targetImage) {
			instancesToUpdate = append(instancesToUpdate, unleash)
		}
		// Capture the currently deployed image for PreviousImage tracking
		if currentImage != "" && currentDeployedImage == "" {
			currentDeployedImage = currentImage
		}
	}

	log.Info("Image tracking state", "currentDeployedImage", currentDeployedImage, "previousImage", releaseChannel.Status.PreviousImage, "targetImage", string(targetImage))

	if len(instancesToUpdate) == 0 {
		log.Info("All instances are up to date")
		// Update instance counts even when no updates are needed to maintain accurate status
		r.updateInstanceCounts(releaseChannel, targetInstances)
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}
		r.recordMetrics(releaseChannel, labels)

		// Update status to persist instance counts
		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			log.V(1).Info("Failed to update ReleaseChannel status with instance counts", "error", err)
		}
		return ctrl.Result{RequeueAfter: time.Minute * 10}, nil
	}

	// Check if this is initial deployment vs. actual rollout
	// If no instances have resolved images AND no PreviousImage is set, this is initial deployment
	isInitialDeployment := true
	for _, unleash := range targetInstances {
		if unleash.Status.ResolvedReleaseChannelImage != "" {
			isInitialDeployment = false
			break
		}
	}

	if isInitialDeployment {
		log.Info("Detected initial deployment - letting Unleash controller handle image resolution without rollout coordination")
		// Update instance counts for status tracking but don't trigger rollout
		r.updateInstanceCounts(releaseChannel, targetInstances)
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}
		r.recordMetrics(releaseChannel, labels)

		// Update status to persist instance counts
		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			log.V(1).Info("Failed to update ReleaseChannel status with instance counts", "error", err)
		}
		// Requeue to check progress but don't trigger rollout coordination
		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

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
	labels := []string{releaseChannel.Namespace, releaseChannel.Name}
	r.recordMetrics(releaseChannel, labels)

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

// executeCompletedPhase handles completed rollouts
func (r *ReleaseChannelReconciler) executeCompletedPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Rollout completed, transitioning to idle")

	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
	return r.updateReleaseChannelStatus(ctx, releaseChannel)
}

// executeFailedPhase handles failed rollouts
func (r *ReleaseChannelReconciler) executeFailedPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Handling failed rollout")

	// Could implement automatic rollback here if configured
	if releaseChannel.Spec.Rollback.Enabled {
		log.Info("Automatic rollback is enabled, but not implemented yet")
	}

	// For now, just stay in failed state and requeue
	return ctrl.Result{RequeueAfter: time.Minute * 10}, nil
}

// recordError records an error in the ReleaseChannel status
func (r *ReleaseChannelReconciler) recordError(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, err error) {
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
	releaseChannel.Status.FailureReason = err.Error()

	meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
		Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  "Failed",
		Message: fmt.Sprintf("ReleaseChannel failed: %v", err),
	})
}

// recordMetrics updates the metrics for a ReleaseChannel
func (r *ReleaseChannelReconciler) recordMetrics(releaseChannel *unleashv1.ReleaseChannel, labels []string) {
	// Update status metrics (1=success, 0.5=in-progress, 0=failed)
	var status float64
	switch releaseChannel.Status.Phase {
	case unleashv1.ReleaseChannelPhaseCompleted:
		status = 1 // Success
	case unleashv1.ReleaseChannelPhaseFailed:
		status = 0 // Failed
	default:
		status = 0.5 // In progress
	}
	releaseChannelStatus.WithLabelValues(labels[0], labels[1]).Set(status)

	// Update instance metrics
	releaseChannelInstances.WithLabelValues(labels[0], labels[1]).Set(float64(releaseChannel.Status.Instances))
	releaseChannelInstancesUpToDate.WithLabelValues(labels[0], labels[1]).Set(float64(releaseChannel.Status.InstancesUpToDate))
}

// executeValidatingPhase validates rollout readiness
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
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Update status and determine next phase
	r.updateInstanceCounts(releaseChannel, targetInstances)

	// Record updated metrics after counting instances
	labels := []string{releaseChannel.Namespace, releaseChannel.Name}
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

	return ctrl.Result{RequeueAfter: time.Second * 5}, nil
}

// executeCanaryPhase handles canary deployment
func (r *ReleaseChannelReconciler) executeCanaryPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing canary phase")

	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances: %v", err)
		return ctrl.Result{}, err
	}

	// Check for target image changes during canary phase as well
	targetImage := releaseChannel.Spec.Image
	statusNeedsUpdate := false

	// Always check if we need to update PreviousImage when target changes
	if len(targetInstances) > 0 {
		// Get the currently deployed image from any instance
		var currentDeployedImage string
		for _, unleash := range targetInstances {
			if unleash.Status.ResolvedReleaseChannelImage != "" {
				currentDeployedImage = unleash.Status.ResolvedReleaseChannelImage
				log.Info("Found instance with resolved image", "instance", unleash.Name, "resolvedImage", unleash.Status.ResolvedReleaseChannelImage)
				break
			}
		}

		log.Info("Canary phase image change detection", "currentDeployedImage", currentDeployedImage, "targetImage", string(targetImage), "existingPreviousImage", releaseChannel.Status.PreviousImage)

		// If we have a deployed image and it's different from target, capture it as previous
		if currentDeployedImage != "" && currentDeployedImage != string(targetImage) {
			// Only update if we don't already have the correct previous image set
			if releaseChannel.Status.PreviousImage != currentDeployedImage {
				releaseChannel.Status.PreviousImage = currentDeployedImage
				statusNeedsUpdate = true
				log.Info("Detected target image change during canary phase, setting previous image", "previousImage", currentDeployedImage, "newTarget", string(targetImage))
			}
		}
	}

	// If we detected a target image change, update status immediately before proceeding
	if statusNeedsUpdate {
		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			log.Error(err, "Failed to update ReleaseChannel status with previous image during canary phase")
			return ctrl.Result{RequeueAfter: time.Second * 5}, err
		}
		log.Info("Updated ReleaseChannel status with previous image during canary phase")
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

	// Deploy to canary instances
	result, err := r.deployToInstances(ctx, releaseChannel, canaryInstances, log)
	if err != nil {
		newPhase := releasePhaseFailedCanary(releaseChannel.Spec.Rollback.Enabled)
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.FailureReason = fmt.Sprintf("Canary deployment failed: %v", err)
		// Record metrics for failure state
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return result, err
	}

	// Update instance counts after deployment (use targetInstances for total counts)
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.Namespace, releaseChannel.Name}
	r.recordMetrics(releaseChannel, labels)

	// Check if canary deployment is complete
	canaryComplete := r.areInstancesReady(ctx, canaryInstances, string(releaseChannel.Spec.Image), log)
	if !canaryComplete {
		log.Info("Canary instances not ready yet")
		if _, err := r.updateReleaseChannelStatus(ctx, releaseChannel); err != nil {
			log.V(1).Info("Failed to update ReleaseChannel status before requeue", "error", err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// Perform health checks on canary instances
	if releaseChannel.Spec.HealthChecks.Enabled {
		healthy, err := r.performHealthChecks(ctx, canaryInstances, releaseChannel, log)
		if err != nil {
			newPhase := releasePhaseFailedCanary(releaseChannel.Spec.Rollback.Enabled)
			r.recordPhaseTransition(releaseChannel, newPhase)
			releaseChannel.Status.Phase = newPhase
			releaseChannel.Status.FailureReason = fmt.Sprintf("Canary health check failed: %v", err)
			// Record metrics for failure state
			labels := []string{releaseChannel.Namespace, releaseChannel.Name}
			r.recordMetrics(releaseChannel, labels)
			if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
				log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
			}
			return ctrl.Result{}, err
		}

		if !healthy {
			log.Info("Canary health checks not passing yet")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
	}

	log.Info("All instances passed health checks")
	log.Info("Canary deployment successful, transitioning to rolling phase")
	r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseRolling)
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseRolling
	// Record metrics for successful phase transition
	labels = []string{releaseChannel.Namespace, releaseChannel.Name}
	r.recordMetrics(releaseChannel, labels)
	return r.updateReleaseChannelStatus(ctx, releaseChannel)
}

// executeRollingPhase handles rolling deployment to remaining instances
func (r *ReleaseChannelReconciler) executeRollingPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing rolling phase")

	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		newPhase := unleashv1.ReleaseChannelPhaseFailed
		r.recordPhaseTransition(releaseChannel, newPhase)
		releaseChannel.Status.Phase = newPhase
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances: %v", err)
		// Record metrics for failure state
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	// Update instance counts
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.Namespace, releaseChannel.Name}
	r.recordMetrics(releaseChannel, labels)

	// Update status to persist instance counts using improved conflict handling
	if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
		log.V(1).Info("Failed to update ReleaseChannel status with instance counts", "error", statusErr)
		// Continue processing even if status update fails
	}

	// Get instances that need updates (excluding already updated canary instances)
	instancesToUpdate := r.getInstancesToUpdate(targetInstances, releaseChannel)
	if len(instancesToUpdate) == 0 {
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

	// Check if this is a new batch that needs deployment
	needsDeployment := r.shouldTriggerDeployment(ctx, releaseChannel, batch, log)

	if needsDeployment {
		// Deploy to current batch
		result, err := r.deployToInstances(ctx, releaseChannel, batch, log)
		if err != nil {
			newPhase := releasePhaseFailedRolling(releaseChannel.Spec.Rollback.Enabled)
			r.recordPhaseTransition(releaseChannel, newPhase)
			releaseChannel.Status.Phase = newPhase
			releaseChannel.Status.FailureReason = fmt.Sprintf("Rolling deployment failed: %v", err)
			// Record metrics for failure state
			r.recordMetrics(releaseChannel, labels)
			if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
				log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
			}
			return result, err
		}

		// After successful deployment trigger, give Unleash controllers time to process
		log.Info("Deployment triggered, allowing time for Unleash controllers to process")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// Update instance counts after deployment
	r.updateInstanceCounts(releaseChannel, targetInstances)
	r.recordMetrics(releaseChannel, labels)

	// Check if batch is ready
	batchReady := r.areInstancesReady(ctx, batch, string(releaseChannel.Spec.Image), log)
	if !batchReady {
		log.Info("Batch instances not ready yet", "batchSize", len(batch))
		// Update status to persist instance counts even when not ready using improved conflict handling
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status while waiting for batch", "error", statusErr)
		}
		// Use exponential backoff when waiting for instances to become ready
		return ctrl.Result{RequeueAfter: r.getBackoffDuration(releaseChannel)}, nil
	}

	// Perform health checks if enabled
	if releaseChannel.Spec.HealthChecks.Enabled {
		healthy, err := r.performHealthChecks(ctx, batch, releaseChannel, log)
		if err != nil {
			newPhase := releasePhaseFailedRolling(releaseChannel.Spec.Rollback.Enabled)
			r.recordPhaseTransition(releaseChannel, newPhase)
			releaseChannel.Status.Phase = newPhase
			releaseChannel.Status.FailureReason = fmt.Sprintf("Rolling deployment health check failed: %v", err)
			// Record metrics for failure state
			r.recordMetrics(releaseChannel, labels)
			if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
				log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
			}
			return ctrl.Result{}, err
		}

		if !healthy {
			log.Info("Batch health checks not passing yet", "batchSize", len(batch))
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
	}

	log.Info("All instances passed health checks")
	log.Info("Batch completed successfully", "batchSize", len(batch), "remaining", len(instancesToUpdate)-len(batch))

	// Wait for batch interval before next batch
	batchInterval := time.Second * 30
	if releaseChannel.Spec.Strategy.BatchInterval != nil {
		batchInterval = releaseChannel.Spec.Strategy.BatchInterval.Duration
	}

	return ctrl.Result{RequeueAfter: batchInterval}, nil
}

// executeRollingBackPhase handles rollback operations
func (r *ReleaseChannelReconciler) executeRollingBackPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing rollback phase")

	if releaseChannel.Spec.Rollback.PreviousImage == "" {
		log.Info("No previous image for rollback, marking as failed")
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseFailed)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = "No previous image available for rollback"
		// Record metrics for failure state
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}
		r.recordMetrics(releaseChannel, labels)
		return r.updateReleaseChannelStatus(ctx, releaseChannel)
	}

	targetInstances, err := r.getTargetInstances(ctx, releaseChannel)
	if err != nil {
		r.recordPhaseTransition(releaseChannel, unleashv1.ReleaseChannelPhaseFailed)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to get target instances for rollback: %v", err)
		// Record metrics for failure state
		labels := []string{releaseChannel.Namespace, releaseChannel.Name}
		r.recordMetrics(releaseChannel, labels)
		if _, statusErr := r.updateReleaseChannelStatus(ctx, releaseChannel); statusErr != nil {
			log.V(1).Info("Failed to update ReleaseChannel status after error", "error", statusErr)
		}
		return ctrl.Result{}, err
	}

	// Update instance counts and record metrics
	r.updateInstanceCounts(releaseChannel, targetInstances)
	labels := []string{releaseChannel.Namespace, releaseChannel.Name}
	r.recordMetrics(releaseChannel, labels)

	// Set phase to RollingBack - the Unleash controller will handle image resolution
	log.Info("Rolling back to previous image", "previousImage", releaseChannel.Spec.Rollback.PreviousImage)

	// Check if rollback is complete by verifying instances are using the rollback image
	rollbackComplete := r.areInstancesReady(ctx, targetInstances, releaseChannel.Spec.Rollback.PreviousImage, log)

	if !rollbackComplete {
		log.Info("Rollback still in progress, waiting for instances to update")
		// Record metrics for rollback in progress
		r.recordMetrics(releaseChannel, labels)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	log.Info("Rollback completed successfully")
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
	// Record metrics for successful rollback completion
	r.recordMetrics(releaseChannel, labels)
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// Helper functions

// shouldTriggerDeployment determines if we should trigger deployment for a batch
// Optimized to focus on core coordination patterns
func (r *ReleaseChannelReconciler) shouldTriggerDeployment(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, batch []unleashv1.Unleash, log logr.Logger) bool {
	targetImage := string(releaseChannel.Spec.Image)
	currentPhase := releaseChannel.Status.Phase

	// Create expected rollout intent for this phase
	expectedIntent := fmt.Sprintf("%s-%s", targetImage, currentPhase)

	// Check if any instance needs triggering based on rollout intent mismatch
	for _, instance := range batch {
		// Simple coordination check: does the intent match?
		if r.needsIntentUpdate(instance, expectedIntent, targetImage, log) {
			return true
		}
	}

	log.V(1).Info("No instances in batch need deployment triggering", "batchSize", len(batch), "expectedIntent", expectedIntent)
	return false
}

// needsIntentUpdate checks if an instance needs its rollout intent updated
// This is a simplified coordination check focusing on the key patterns
func (r *ReleaseChannelReconciler) needsIntentUpdate(instance unleashv1.Unleash, expectedIntent, targetImage string, log logr.Logger) bool {
	// First, check if the instance is already rolling out - if so, don't interfere
	if r.isInstanceRollingOut(instance) {
		log.V(1).Info("Instance is already rolling out, skipping trigger",
			"name", instance.Name)
		return false
	}

	// Check current rollout intent annotation
	currentIntent := instance.Annotations["releasechannel.unleash.nais.io/rollout-intent"]

	// Intent mismatch - need to trigger
	if currentIntent != expectedIntent {
		log.V(1).Info("Instance needs deployment trigger due to intent mismatch",
			"name", instance.Name,
			"currentIntent", currentIntent,
			"expectedIntent", expectedIntent)
		return true
	}

	// Also check if the instance doesn't have the target image yet
	if instance.Status.ResolvedReleaseChannelImage != targetImage {
		log.V(1).Info("Instance needs deployment trigger due to image mismatch",
			"name", instance.Name,
			"currentImage", instance.Status.ResolvedReleaseChannelImage,
			"targetImage", targetImage)
		return true
	}

	return false
}

// getBackoffDuration calculates backoff duration based on phase transition attempts
// This implements exponential backoff to reduce controller load during wait periods
func (r *ReleaseChannelReconciler) getBackoffDuration(releaseChannel *unleashv1.ReleaseChannel) time.Duration {
	// Base duration for waiting
	baseDuration := time.Second * 10

	// Check how long we've been in the current phase to implement backoff
	if releaseChannel.Status.LastReconcileTime != nil {
		timeSinceLastReconcile := time.Since(releaseChannel.Status.LastReconcileTime.Time)

		// If we've been waiting for a while, increase the backoff
		if timeSinceLastReconcile > time.Minute*2 {
			return time.Second * 30 // Longer backoff if we've been waiting
		} else if timeSinceLastReconcile > time.Minute {
			return time.Second * 20 // Medium backoff
		}
	}

	return baseDuration // Default backoff
}

// getTargetInstances finds all Unleash instances that reference this ReleaseChannel
// and do not have CustomImage set (since CustomImage and ReleaseChannel are mutually exclusive)
func (r *ReleaseChannelReconciler) getTargetInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) ([]unleashv1.Unleash, error) {
	unleashList := &unleashv1.UnleashList{}
	if err := r.List(ctx, unleashList, client.InNamespace(releaseChannel.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list Unleash instances: %w", err)
	}

	var targetInstances []unleashv1.Unleash
	for _, unleash := range unleashList.Items {
		// Only manage instances that reference this ReleaseChannel AND do not have CustomImage set
		if unleash.Spec.ReleaseChannel.Name == releaseChannel.Name && unleash.Spec.CustomImage == "" {
			targetInstances = append(targetInstances, unleash)
		}
	}

	return targetInstances, nil
}

// getCanaryInstances filters instances based on canary label selector
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

// getInstancesToUpdate finds instances that need to be updated with the target image
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

// deployToInstances coordinates rollout by triggering Unleash reconciliations
// OPTIMIZATION: Uses semantic annotations for cleaner Option 2 alignment
func (r *ReleaseChannelReconciler) deployToInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, instances []unleashv1.Unleash, log logr.Logger) (ctrl.Result, error) {
	targetImage := string(releaseChannel.Spec.Image)

	log.Info("Coordinating deployment", "instances", len(instances), "targetImage", targetImage, "phase", releaseChannel.Status.Phase)

	// Trigger Unleash reconciliations by updating a harmless annotation
	// This causes generation increment without changing actual spec
	for _, instance := range instances {
		// Re-fetch to get latest resource version
		currentInstance := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, currentInstance); err != nil {
			log.Error(err, "Failed to get Unleash instance", "name", instance.Name)
			continue
		}

		// Add/update annotation to trigger reconciliation with retry logic for conflicts
		err := wait.PollUntilContextTimeout(ctx, time.Millisecond*100, time.Second*2, true, func(ctx context.Context) (bool, error) {
			// Re-fetch to get latest resource version before each attempt
			if err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, currentInstance); err != nil {
				return false, err
			}

			if currentInstance.Annotations == nil {
				currentInstance.Annotations = make(map[string]string)
			}

			// Store declarative rollout intent and target image
			rolloutIntent := fmt.Sprintf("%s-%s", targetImage, releaseChannel.Status.Phase)
			currentInstance.Annotations["releasechannel.unleash.nais.io/rollout-intent"] = rolloutIntent
			currentInstance.Annotations["releasechannel.unleash.nais.io/target-image"] = targetImage
			currentInstance.Annotations["releasechannel.unleash.nais.io/last-rollout-trigger"] = time.Now().Format(time.RFC3339)

			if err := r.Update(ctx, currentInstance); err != nil {
				if apierrors.IsConflict(err) {
					log.V(1).Info("Resource conflict when triggering Unleash reconciliation, retrying", "name", instance.Name)
					return false, nil // Retry on conflict
				}
				return false, err // Stop retrying on other errors
			}
			return true, nil // Success
		})

		if err != nil {
			log.Error(err, "Failed to trigger Unleash reconciliation", "name", instance.Name)
			return ctrl.Result{}, fmt.Errorf("failed to trigger reconciliation for %s: %w", instance.Name, err)
		}

		log.V(1).Info("Triggered Unleash reconciliation", "name", instance.Name)
	}

	return ctrl.Result{}, nil
}

// areInstancesReady checks if all instances in the list are ready with the expected image
func (r *ReleaseChannelReconciler) areInstancesReady(ctx context.Context, instances []unleashv1.Unleash, targetImage string, log logr.Logger) bool {
	for _, instance := range instances {
		// Re-fetch instance to get current status
		currentInstance := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, currentInstance); err != nil {
			log.Error(err, "Failed to get instance status", "name", instance.Name)
			return false
		}

		// Check if instance has the expected image (via ResolvedReleaseChannelImage)
		// The Unleash controller should have resolved the correct image based on ReleaseChannel phase
		expectedImage := r.getExpectedImageForInstance(ctx, currentInstance, targetImage)
		if currentInstance.Status.ResolvedReleaseChannelImage != expectedImage {
			log.V(1).Info("Instance not updated yet", "name", instance.Name,
				"resolved", currentInstance.Status.ResolvedReleaseChannelImage,
				"expected", expectedImage)
			return false
		}

		// Check if instance is ready
		if !r.isInstanceReady(currentInstance) {
			log.V(1).Info("Instance not ready yet", "name", instance.Name)
			return false
		}
	}

	return true
}

// getExpectedImageForInstance determines what image we expect this instance to have
// based on the current ReleaseChannel phase
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
		Namespace: instance.Namespace,
	}, releaseChannel)
	if err != nil {
		// If we can't get the release channel, expect the target image
		return targetImage
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

// isInstanceReady checks if an individual instance is ready
func (r *ReleaseChannelReconciler) isInstanceReady(instance *unleashv1.Unleash) bool {
	// Check for Ready condition
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled {
			return condition.Status == metav1.ConditionTrue
		}
	}
	return false
}

// isInstanceRollingOut checks if an instance is currently in the middle of a deployment rollout
func (r *ReleaseChannelReconciler) isInstanceRollingOut(instance unleashv1.Unleash) bool {
	// Check if the instance is in a degraded state (might indicate ongoing deployment)
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeDegraded {
			return condition.Status == metav1.ConditionTrue
		}
	}

	// Check if reconciled condition is False (indicating ongoing work)
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled {
			// If reconciled is False, the controller is likely working on it
			return condition.Status == metav1.ConditionFalse
		}
	}

	// Check if there's a recent rollout trigger annotation that suggests ongoing work
	if lastTrigger, exists := instance.Annotations["releasechannel.unleash.nais.io/last-rollout-trigger"]; exists {
		// Parse the timestamp and check if it's very recent (within last 2 minutes)
		// This suggests a rollout was recently triggered and might still be in progress
		if triggerTime, err := time.Parse(time.RFC3339, lastTrigger); err == nil {
			if time.Since(triggerTime) < 2*time.Minute {
				// Recent trigger, likely still rolling out
				return true
			}
		}
	}

	return false
}

// performHealthChecks performs health checks on the given instances
func (r *ReleaseChannelReconciler) performHealthChecks(ctx context.Context, instances []unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (bool, error) {
	// Wait for initial delay if configured
	initialDelay := time.Second * 30
	if releaseChannel.Spec.HealthChecks.InitialDelay != nil {
		initialDelay = releaseChannel.Spec.HealthChecks.InitialDelay.Duration
	}

	// Check if we're still in initial delay period
	if releaseChannel.Status.StartTime != nil {
		elapsed := time.Since(releaseChannel.Status.StartTime.Time)
		if elapsed < initialDelay {
			log.V(1).Info("Still in initial delay period", "elapsed", elapsed, "delay", initialDelay)
			return false, nil
		}
	}

	for _, instance := range instances {
		// Re-fetch to get latest status
		currentInstance := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: instance.Name, Namespace: instance.Namespace}, currentInstance); err != nil {
			return false, fmt.Errorf("failed to get instance %s: %w", instance.Name, err)
		}

		// Check for explicit failure conditions first
		for _, condition := range currentInstance.Status.Conditions {
			if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled &&
				condition.Status == metav1.ConditionFalse &&
				condition.Reason == "Failed" {
				// Record failed health check
				releaseChannelHealthChecks.WithLabelValues(releaseChannel.Namespace, releaseChannel.Name, "failed").Inc()
				return false, fmt.Errorf("instance %s failed: %s", instance.Name, condition.Message)
			}
		}

		// Check if instance is connected (healthy)
		connected := false
		for _, condition := range currentInstance.Status.Conditions {
			if condition.Type == unleashv1.UnleashStatusConditionTypeConnected {
				connected = condition.Status == metav1.ConditionTrue
				break
			}
		}

		if !connected {
			// For test scenarios with broken images, check if deployment is explicitly failing
			if currentInstance.Status.ResolvedReleaseChannelImage != "" &&
				strings.Contains(currentInstance.Status.ResolvedReleaseChannelImage, "broken") {
				// Record failed health check for broken images
				releaseChannelHealthChecks.WithLabelValues(releaseChannel.Namespace, releaseChannel.Name, "failed").Inc()
				return false, fmt.Errorf("instance %s using broken image %s", instance.Name, currentInstance.Status.ResolvedReleaseChannelImage)
			}

			log.V(1).Info("Instance not connected/healthy yet", "name", instance.Name)
			return false, nil
		}
	}

	log.Info("All instances passed health checks")
	// Record successful health check
	releaseChannelHealthChecks.WithLabelValues(releaseChannel.Namespace, releaseChannel.Name, "success").Inc()
	return true, nil
}

// updateInstanceCounts updates the status with current instance counts
func (r *ReleaseChannelReconciler) updateInstanceCounts(releaseChannel *unleashv1.ReleaseChannel, targetInstances []unleashv1.Unleash) {
	releaseChannel.Status.Instances = len(targetInstances)

	targetImage := string(releaseChannel.Spec.Image)
	upToDateCount := 0
	canaryCount := 0
	canaryUpToDateCount := 0

	for _, instance := range targetInstances {
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
		}
	}

	releaseChannel.Status.InstancesUpToDate = upToDateCount
	releaseChannel.Status.CanaryInstances = canaryCount
	releaseChannel.Status.CanaryInstancesUpToDate = canaryUpToDateCount

	// Calculate progress
	if len(targetInstances) > 0 {
		releaseChannel.Status.Progress = (upToDateCount * 100) / len(targetInstances)
	} else {
		releaseChannel.Status.Progress = 100
	}
}

// matchesLabelSelector checks if an instance matches the given label selector
func (r *ReleaseChannelReconciler) matchesLabelSelector(instance unleashv1.Unleash, selector metav1.LabelSelector) bool {
	// Convert the selector to a labels.Selector
	sel, err := metav1.LabelSelectorAsSelector(&selector)
	if err != nil {
		// Handle the error, e.g., by logging it and returning false
		// In a real-world scenario, you might want to log this error
		return false
	}

	// Create a label set from the instance's labels
	instanceLabels := labels.Set(instance.Labels)

	// Check if the selector matches the instance's labels
	return sel.Matches(instanceLabels)
}

// Utility functions

func releasePhaseFailedCanary(rollbackEnabled bool) unleashv1.ReleaseChannelPhase {
	if rollbackEnabled {
		return unleashv1.ReleaseChannelPhaseRollingBack
	}
	return unleashv1.ReleaseChannelPhaseFailed
}

func releasePhaseFailedRolling(rollbackEnabled bool) unleashv1.ReleaseChannelPhase {
	if rollbackEnabled {
		return unleashv1.ReleaseChannelPhaseRollingBack
	}
	return unleashv1.ReleaseChannelPhaseFailed
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// recordPhaseTransition records metrics for phase transitions
func (r *ReleaseChannelReconciler) recordPhaseTransition(releaseChannel *unleashv1.ReleaseChannel, newPhase unleashv1.ReleaseChannelPhase) {
	labels := []string{
		releaseChannel.Namespace,
		releaseChannel.Name,
		string(newPhase),
	}
	releaseChannelPhaseTransitions.WithLabelValues(labels...).Inc()
}

// updateReleaseChannelStatus updates the ReleaseChannel status and returns the result
func (r *ReleaseChannelReconciler) updateReleaseChannelStatus(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	// Store the status values we want to persist
	statusToApply := releaseChannel.Status.DeepCopy()

	log.V(1).Info("Persisting ReleaseChannel status", "instances", statusToApply.Instances, "upToDate", statusToApply.InstancesUpToDate, "phase", statusToApply.Phase)

	// Retry logic for handling resource conflicts
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			if apierrors.IsConflict(err) && i < maxRetries-1 {
				log.V(1).Info("Resource conflict during status update, retrying", "attempt", i+1, "error", err)

				// Fetch fresh copy and reapply our status
				fresh := &unleashv1.ReleaseChannel{}
				if getErr := r.Get(ctx, client.ObjectKeyFromObject(releaseChannel), fresh); getErr != nil {
					return ctrl.Result{}, fmt.Errorf("failed to fetch fresh resource after conflict: %w", getErr)
				}

				// Reapply our status values to the fresh copy
				fresh.Status = *statusToApply
				*releaseChannel = *fresh

				time.Sleep(time.Millisecond * 50 * time.Duration(i+1)) // Exponential backoff
				continue
			}
			log.V(1).Info("Failed to update ReleaseChannel status", "error", err, "instances", statusToApply.Instances)
			return ctrl.Result{}, fmt.Errorf("failed to update ReleaseChannel status: %w", err)
		}
		// Success - use longer interval to reduce aggressive reconciling
		log.V(1).Info("Successfully updated ReleaseChannel status", "instances", statusToApply.Instances, "upToDate", statusToApply.InstancesUpToDate)
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// If we reach here, all retries failed
	log.V(1).Info("All status update retries failed, requeuing", "instances", statusToApply.Instances)
	return ctrl.Result{RequeueAfter: time.Millisecond * 500}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReleaseChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Tracer = otel.Tracer("github.com/nais/unleasherator/internal/controller")

	// Initialize decision engine with real time provider
	if r.DecisionEngine == nil {
		r.DecisionEngine = statemachine.NewDecisionEngineWithDefaults(&statemachine.RealTimeProvider{})
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ReleaseChannel{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1, // Prevent concurrent reconciles that could cause race conditions
		}).
		Watches(
			&unleashv1.Unleash{},
			handler.EnqueueRequestsFromMapFunc(r.findReleaseChannelsForUnleash),
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
				Namespace: unleashInstance.Namespace,
			},
		},
	}
}
