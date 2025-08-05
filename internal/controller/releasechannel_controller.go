package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

const (
	ReleaseChannelFinalizer = "releasechannel.unleash.nais.io/finalizer"
)

// ReleaseChannelReconciler reconciles a ReleaseChannel object
type ReleaseChannelReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Tracer   trace.Tracer
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
	)
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/finalizers,verbs=update
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ReleaseChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrllog.FromContext(ctx).WithValues("releasechannel", req.NamespacedName)

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

	// Update final status only if there were actual changes
	if result.RequeueAfter > 0 {
		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			log.V(1).Info("Failed to update ReleaseChannel status, will retry", "error", err)
			// Don't fail the reconciliation for status update conflicts
			return ctrl.Result{RequeueAfter: time.Second * 5}, nil
		}
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
	case unleashv1.ReleaseChannelPhaseCompleted:
		return r.executeCompletedPhase(ctx, releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseFailed:
		return r.executeFailedPhase(ctx, releaseChannel, log)
	default:
		log.Info("Unknown phase, resetting to idle", "phase", releaseChannel.Status.Phase)
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
		return ctrl.Result{RequeueAfter: time.Second * 5}, nil
	}
}

// executeIdlePhase checks if a new rollout should start
func (r *ReleaseChannelReconciler) executeIdlePhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Executing idle phase")

	// Find all Unleash instances that reference this ReleaseChannel
	unleashList := &unleashv1.UnleashList{}
	if err := r.List(ctx, unleashList, client.InNamespace(releaseChannel.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list Unleash instances: %w", err)
	}

	// Filter instances that use this release channel
	var targetInstances []unleashv1.Unleash
	for _, unleash := range unleashList.Items {
		if unleash.Spec.ReleaseChannel.Name == releaseChannel.Name {
			targetInstances = append(targetInstances, unleash)
		}
	}

	if len(targetInstances) == 0 {
		log.Info("No matching Unleash instances found")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Check if all instances are already up to date
	targetImage := releaseChannel.Spec.Image
	log.Info("Checking instances for updates", "targetImage", string(targetImage), "instanceCount", len(targetInstances))

	var instancesToUpdate []unleashv1.Unleash
	for _, unleash := range targetInstances {
		currentImage := unleash.Spec.CustomImage
		log.Info("Instance status", "name", unleash.Name, "currentImage", currentImage, "targetImage", string(targetImage))
		if currentImage != string(targetImage) {
			instancesToUpdate = append(instancesToUpdate, unleash)
		}
	}

	if len(instancesToUpdate) == 0 {
		log.Info("All instances are up to date")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	log.Info("Starting rollout", "instancesToUpdate", len(instancesToUpdate))

	// Directly update instances
	var errors []string
	successCount := 0
	for _, unleash := range instancesToUpdate {
		log.Info("Updating instance", "name", unleash.Name, "from", unleash.Spec.CustomImage, "to", string(targetImage))

		if err := r.updateUnleashInstance(ctx, unleash.Namespace, unleash.Name, string(targetImage)); err != nil {
			log.Error(err, "Failed to update instance", "name", unleash.Name)
			if apierrors.IsConflict(err) {
				// For conflicts, just log and continue - the reconcile will retry
				log.V(1).Info("Conflict updating instance, will retry later", "name", unleash.Name)
				continue
			}
			errors = append(errors, fmt.Sprintf("%s: %v", unleash.Name, err))
		} else {
			successCount++
		}
	}

	// If there were non-conflict errors, fail the rollout
	if len(errors) > 0 {
		releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
		releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to update instances: %v", errors)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}

	// If some instances couldn't be updated due to conflicts, requeue to retry
	if successCount < len(instancesToUpdate) {
		log.Info("Some instances had conflicts, will retry", "successful", successCount, "total", len(instancesToUpdate))
		return ctrl.Result{RequeueAfter: time.Second * 2}, nil
	}

	// Update status to reflect progress
	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseCompleted
	releaseChannel.Status.Instances = len(targetInstances)
	releaseChannel.Status.InstancesUpToDate = len(targetInstances)
	releaseChannel.Status.Progress = 100

	meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
		Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "RolloutCompleted",
		Message: fmt.Sprintf("Successfully updated %d instances to %s", len(instancesToUpdate), string(targetImage)),
	})

	log.Info("Rollout completed successfully", "updatedInstances", len(instancesToUpdate))
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
}

// executeCompletedPhase handles completed rollouts
func (r *ReleaseChannelReconciler) executeCompletedPhase(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, log logr.Logger) (ctrl.Result, error) {
	log.Info("Rollout completed, transitioning to idle")

	releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseIdle
	return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
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

// updateUnleashInstance updates a specific Unleash instance with new image.
// This sets CustomImage to trigger reconciliation and also updates status for tracking.
func (r *ReleaseChannelReconciler) updateUnleashInstance(ctx context.Context, namespace, name, image string) error {
	unleash := &unleashv1.Unleash{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, unleash); err != nil {
		releaseChannelInstanceUpdates.WithLabelValues(namespace, "unknown", "failed").Inc()
		return fmt.Errorf("failed to get Unleash instance %s: %w", name, err)
	}

	// Get the ReleaseChannel name for metrics
	rcName := "unknown"
	if unleash.Spec.ReleaseChannel.Name != "" {
		rcName = unleash.Spec.ReleaseChannel.Name
	}

	needsSpecUpdate := false
	needsStatusUpdate := false

	// For intentional upgrades via ReleaseChannel, we set CustomImage to trigger reconciliation
	if unleash.Spec.CustomImage != image {
		unleash.Spec.CustomImage = image
		needsSpecUpdate = true
	}

	// Also update the status to track the resolved ReleaseChannel image
	if unleash.Status.ResolvedReleaseChannelImage != image ||
		unleash.Status.ReleaseChannelName != unleash.Spec.ReleaseChannel.Name {
		unleash.Status.ResolvedReleaseChannelImage = image
		unleash.Status.ReleaseChannelName = unleash.Spec.ReleaseChannel.Name
		needsStatusUpdate = true
	}

	// Update spec if needed (this triggers reconciliation)
	if needsSpecUpdate {
		if err := r.Update(ctx, unleash); err != nil {
			if apierrors.IsConflict(err) {
				// Resource was modified, requeue for retry
				releaseChannelConflicts.WithLabelValues(namespace, rcName).Inc()
				return fmt.Errorf("conflict updating Unleash instance %s spec, will retry: %w", name, err)
			}
			releaseChannelInstanceUpdates.WithLabelValues(namespace, rcName, "failed").Inc()
			return fmt.Errorf("failed to update Unleash instance %s spec: %w", name, err)
		}
	}

	// Update status if needed (this tracks the resolved image)
	if needsStatusUpdate {
		if err := r.Status().Update(ctx, unleash); err != nil {
			if apierrors.IsConflict(err) {
				// Resource was modified, requeue for retry
				releaseChannelConflicts.WithLabelValues(namespace, rcName).Inc()
				return fmt.Errorf("conflict updating Unleash instance %s status, will retry: %w", name, err)
			}
			releaseChannelInstanceUpdates.WithLabelValues(namespace, rcName, "failed").Inc()
			return fmt.Errorf("failed to update Unleash instance %s status: %w", name, err)
		}
	}

	// Record successful update
	if needsSpecUpdate || needsStatusUpdate {
		releaseChannelInstanceUpdates.WithLabelValues(namespace, rcName, "success").Inc()
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReleaseChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Tracer = otel.Tracer("github.com/nais/unleasherator/internal/controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ReleaseChannel{}).
		Complete(r)
}
