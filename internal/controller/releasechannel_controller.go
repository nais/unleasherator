package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
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
	"sigs.k8s.io/controller-runtime/pkg/log"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

// ReleaseChannelReconciler reconciles a ReleaseChannel object
type ReleaseChannelReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	Recorder       record.EventRecorder
	CircuitBreaker *CircuitBreaker
	Tracer         trace.Tracer
}

const (
	ReleaseChannelFinalizer = "releasechannel.unleash.nais.io/finalizer"
)

//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/finalizers,verbs=update
//+kubebuilder:rbac:groups=unleash.nais.io,resources=unleashes,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ReleaseChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithName("releasechannel")
	log.Info("Starting reconciliation of ReleaseChannel")

	// Fetch the ReleaseChannel instance
	releaseChannel := &unleashv1.ReleaseChannel{}
	err := r.Get(ctx, req.NamespacedName, releaseChannel)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReleaseChannel resource not found, ignoring since object must be deleted")
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
	targetImage := string(releaseChannel.Spec.Image)
	log.Info("Checking instances for updates", "targetImage", targetImage, "instanceCount", len(targetInstances))

	var instancesToUpdate []unleashv1.Unleash
	for _, unleash := range targetInstances {
		currentImage := unleash.Spec.CustomImage
		log.Info("Instance status", "name", unleash.Name, "currentImage", currentImage, "targetImage", targetImage)
		if currentImage != targetImage {
			instancesToUpdate = append(instancesToUpdate, unleash)
		}
	}

	if len(instancesToUpdate) == 0 {
		log.Info("All instances are up to date")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	log.Info("Starting rollout", "instancesToUpdate", len(instancesToUpdate))

	// Directly update instances
	for _, unleash := range instancesToUpdate {
		log.Info("Updating instance", "name", unleash.Name, "from", unleash.Spec.CustomImage, "to", targetImage)

		if err := r.updateUnleashInstance(ctx, unleash.Namespace, unleash.Name, targetImage); err != nil {
			log.Error(err, "Failed to update instance", "name", unleash.Name)
			releaseChannel.Status.Phase = unleashv1.ReleaseChannelPhaseFailed
			releaseChannel.Status.FailureReason = fmt.Sprintf("Failed to update instance %s: %v", unleash.Name, err)
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
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
		Message: fmt.Sprintf("Successfully updated %d instances to %s", len(instancesToUpdate), targetImage),
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

// updateUnleashInstance updates a specific Unleash instance with new image using retry logic
func (r *ReleaseChannelReconciler) updateUnleashInstance(ctx context.Context, namespace, name, image string) error {
	// Retry logic to handle optimistic concurrency conflicts
	for i := 0; i < 3; i++ {
		unleash := &unleashv1.Unleash{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, unleash); err != nil {
			return fmt.Errorf("failed to get Unleash instance %s: %w", name, err)
		}

		// Check if already up to date
		if unleash.Spec.CustomImage == image {
			return nil
		}

		unleash.Spec.CustomImage = image
		if err := r.Update(ctx, unleash); err != nil {
			if apierrors.IsConflict(err) {
				// Resource was modified, retry after a short delay
				time.Sleep(time.Millisecond * time.Duration(100*(i+1)))
				continue
			}
			return fmt.Errorf("failed to update Unleash instance %s: %w", name, err)
		}

		return nil
	}

	return fmt.Errorf("failed to update Unleash instance %s after 3 retries due to conflicts", name)
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReleaseChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Tracer = otel.Tracer("github.com/nais/unleasherator/internal/controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ReleaseChannel{}).
		Complete(r)
}
