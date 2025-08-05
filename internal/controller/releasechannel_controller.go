package controller

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
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
	"sigs.k8s.io/controller-runtime/pkg/log"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/o11y"
)

// ReleaseChannelReconciler reconciles a ReleaseChannel object
type ReleaseChannelReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Tracer   trace.Tracer
}

//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=unleash.nais.io,resources=releasechannels/finalizers,verbs=update

func (r *ReleaseChannelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	spanOpts := o11y.ReconcilerAttributes(ctx, req)
	ctx, span := r.Tracer.Start(ctx, "Reconcile ReleaseChannel", spanOpts...)
	defer span.End()

	log := log.FromContext(ctx).WithName("releasechannel").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Starting reconciliation of ReleaseChannel")

	releaseChannel := &unleashv1.ReleaseChannel{}
	if err := r.Get(ctx, req.NamespacedName, releaseChannel); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("ReleaseChannel resource not found. Ignoring since object must be deleted")
			return ctrl.Result{Requeue: false}, nil
		}
		log.Error(err, "Failed to get ReleaseChannel")
		return ctrl.Result{}, err
	}

	// Check if marked for deletion
	if releaseChannel.GetDeletionTimestamp() != nil {
		span.AddEvent("ReleaseChannel marked for deletion")
		log.Info("ReleaseChannel marked for deletion")
		return ctrl.Result{Requeue: false}, nil
	}

	// Set status to unknown if no status is set
	if len(releaseChannel.Status.Conditions) == 0 {
		span.AddEvent("Setting status as unknown for ReleaseChannel")
		log.Info("Setting status as unknown for ReleaseChannel")
		meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})

		if err := r.Status().Update(ctx, releaseChannel); err != nil {
			log.Error(err, "Failed to update ReleaseChannel status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, releaseChannel); err != nil {
			log.Error(err, "Failed to get ReleaseChannel")
			return ctrl.Result{}, err
		}
	}

	// Calculate status before upgrade
	stats, err := r.calculateStats(ctx, releaseChannel)
	if err != nil {
		log.Error(err, "Failed to calculate release channel stats")
		return ctrl.Result{Requeue: true}, err
	}

	// Update status with current statistics
	r.updateStatus(ctx, releaseChannel, stats)

	// Update Unleash instances that match this release channel
	err = r.upgradeInstances(ctx, releaseChannel)
	if err != nil {
		span.RecordError(err)
		log.Error(err, "Failed to upgrade instances")
		meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
			Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
			Status:  metav1.ConditionFalse,
			Reason:  "UpgradeFailed",
			Message: fmt.Sprintf("Failed to upgrade instances: %v", err),
		})
		if updateErr := r.Status().Update(ctx, releaseChannel); updateErr != nil {
			log.Error(updateErr, "Failed to update ReleaseChannel status")
		}
		return ctrl.Result{Requeue: true}, err
	}

	// Recalculate final status after upgrade
	finalStats, err := r.calculateStats(ctx, releaseChannel)
	if err != nil {
		log.Error(err, "Failed to calculate final release channel stats")
		return ctrl.Result{Requeue: true}, err
	}

	// Mark as successfully reconciled
	meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
		Type:    unleashv1.ReleaseChannelStatusConditionTypeReconciled,
		Status:  metav1.ConditionTrue,
		Reason:  "ReconcileSuccess",
		Message: "ReleaseChannel reconciled successfully",
	})

	// Update final status
	r.updateStatus(ctx, releaseChannel, finalStats)

	if err := r.Status().Update(ctx, releaseChannel); err != nil {
		log.Error(err, "Failed to update ReleaseChannel status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// upgradeInstances upgrades all Unleash instances matching the ReleaseChannel
func (r *ReleaseChannelReconciler) upgradeInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) error {
	ctx, span := r.Tracer.Start(ctx, "Upgrade Unleash Instances")
	defer span.End()

	log := log.FromContext(ctx).WithName("releasechannel").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Upgrading Unleash instances")

	unleashList := &unleashv1.UnleashList{}
	listOptions := []client.ListOption{
		client.InNamespace(releaseChannel.Namespace),
	}

	var canaryInstances []unleashv1.Unleash
	var regularInstances []unleashv1.Unleash

	// List all Unleash instances in the namespace
	err := r.List(ctx, unleashList, listOptions...)
	if err != nil {
		return fmt.Errorf("failed to list Unleash instances: %w", err)
	}

	// Collect all candidate instances
	for _, unleash := range unleashList.Items {
		if !releaseChannel.IsCandidate(&unleash) {
			continue
		}

		if !releaseChannel.ShouldUpdate(&unleash) {
			continue
		}

		// Separate canary and regular instances
		if r.isCanaryInstance(releaseChannel, &unleash) {
			canaryInstances = append(canaryInstances, unleash)
		} else {
			regularInstances = append(regularInstances, unleash)
		}
	}

	// Implement canary deployment strategy
	if releaseChannel.Spec.Strategy.Canary.Enabled && len(canaryInstances) > 0 {
		log.Info("Starting canary deployment", "canaryInstances", len(canaryInstances))

		// First, upgrade canary instances
		for _, unleash := range canaryInstances {
			span.AddEvent(fmt.Sprintf("Upgrading canary Unleash instance %s", unleash.Name))
			log.Info("Upgrading canary Unleash instance", "name", unleash.Name)
			if err := r.upgradeInstance(ctx, &unleash, releaseChannel); err != nil {
				r.Recorder.Eventf(releaseChannel, "Warning", "CanaryUnleashInstanceUpdateFailed", "Failed to update canary Unleash instance %s", unleash.Name)
				return fmt.Errorf("failed to upgrade canary Unleash instance: %w", err)
			}
			r.Recorder.Eventf(releaseChannel, "Normal", "CanaryUnleashInstanceUpdated", "Updated canary Unleash instance %s", unleash.Name)
		}

		log.Info("Canary deployment completed, proceeding with regular instances", "regularInstances", len(regularInstances))
	}

	// Upgrade regular instances with maxParallel constraint
	maxParallel := releaseChannel.Spec.Strategy.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}

	return r.upgradeInstancesParallel(ctx, releaseChannel, regularInstances, maxParallel)
}

// upgradeInstancesParallel upgrades instances in parallel with a maximum concurrency limit
func (r *ReleaseChannelReconciler) upgradeInstancesParallel(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, instances []unleashv1.Unleash, maxParallel int) error {
	semaphore := make(chan struct{}, maxParallel)
	errChan := make(chan error, len(instances))

	log := log.FromContext(ctx).WithName("releasechannel")

	for i := range instances {
		unleash := &instances[i]

		go func(unleash *unleashv1.Unleash) {
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			log.Info("Upgrading Unleash instance", "name", unleash.Name)
			if err := r.upgradeInstance(ctx, unleash, releaseChannel); err != nil {
				r.Recorder.Eventf(releaseChannel, "Warning", "UnleashInstanceUpdateFailed", "Failed to update Unleash instance %s", unleash.Name)
				errChan <- fmt.Errorf("failed to upgrade Unleash instance %s: %w", unleash.Name, err)
				return
			}

			r.Recorder.Eventf(releaseChannel, "Normal", "UnleashInstanceUpdated", "Updated Unleash instance %s", unleash.Name)
			errChan <- nil
		}(unleash)
	}

	// Wait for all goroutines to complete
	for i := 0; i < len(instances); i++ {
		if err := <-errChan; err != nil {
			return err
		}
	}

	return nil
}

// upgradeInstance upgrades the Unleash instance
func (r *ReleaseChannelReconciler) upgradeInstance(ctx context.Context, unleash *unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel) error {
	ctx, span := r.Tracer.Start(ctx, "Upgrade Unleash Instance")
	span.SetAttributes(attribute.KeyValue{Key: "UnleashName", Value: attribute.StringValue(unleash.Name)})
	defer span.End()

	log := log.FromContext(ctx).WithName("releasechannel").WithValues("TraceID", span.SpanContext().TraceID(), "UnleashName", unleash.Name)
	log.Info("Upgrading Unleash instance", "name", unleash.Name)

	if err := r.Get(ctx, client.ObjectKeyFromObject(unleash), unleash); err != nil {
		if apierrors.IsNotFound(err) {
			span.AddEvent(fmt.Sprintf("Unleash instance %s not found", unleash.Name))
			return nil
		}
		return fmt.Errorf("failed to get Unleash instance: %w", err)
	}

	// Update the Unleash instance to use the ReleaseChannel image
	unleash.Spec.CustomImage = string(releaseChannel.Spec.Image)

	if err := r.Update(ctx, unleash); err != nil {
		return fmt.Errorf("failed to update Unleash instance: %w", err)
	}

	if err := r.waitForUnleash(ctx, 5*time.Minute, client.ObjectKeyFromObject(unleash)); err != nil {
		return fmt.Errorf("failed to wait for Unleash instance to be ready: %w", err)
	}

	return nil
}

// waitForUnleash waits for the Unleash instance to be ready
func (r *ReleaseChannelReconciler) waitForUnleash(ctx context.Context, timeout time.Duration, key types.NamespacedName) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := wait.PollUntilContextCancel(ctx, time.Second, true, func(ctx context.Context) (bool, error) {
		unleash := &unleashv1.Unleash{}
		if err := r.Client.Get(ctx, key, unleash); err != nil {
			return false, err
		}

		return unleashIsReady(unleash), nil
	})

	return err
}

// unleashIsReady checks if the Unleash instance is ready
func unleashIsReady(unleash *unleashv1.Unleash) bool {
	for _, condition := range unleash.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled && condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *ReleaseChannelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&unleashv1.ReleaseChannel{}).
		Complete(r)
}

// ReleaseChannelStats holds statistics about a release channel
type ReleaseChannelStats struct {
	TotalInstances          int
	UpToDateInstances       int
	CanaryInstances         int
	CanaryUpToDateInstances int
}

// calculateStats calculates the current statistics for a release channel
func (r *ReleaseChannelReconciler) calculateStats(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) (*ReleaseChannelStats, error) {
	unleashList := &unleashv1.UnleashList{}
	err := r.List(ctx, unleashList, client.InNamespace(releaseChannel.Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list Unleash instances: %w", err)
	}

	stats := &ReleaseChannelStats{}

	for _, unleash := range unleashList.Items {
		if !releaseChannel.IsCandidate(&unleash) {
			continue
		}

		stats.TotalInstances++

		// Check if instance is up to date
		if !releaseChannel.ShouldUpdate(&unleash) {
			stats.UpToDateInstances++
		}

		// Check if instance is a canary
		if r.isCanaryInstance(releaseChannel, &unleash) {
			stats.CanaryInstances++
			if !releaseChannel.ShouldUpdate(&unleash) {
				stats.CanaryUpToDateInstances++
			}
		}
	}

	return stats, nil
}

// updateStatus updates the release channel status with current statistics
func (r *ReleaseChannelReconciler) updateStatus(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel, stats *ReleaseChannelStats) {
	releaseChannel.Status.Instances = stats.TotalInstances
	releaseChannel.Status.InstancesUpToDate = stats.UpToDateInstances
	releaseChannel.Status.CanaryInstances = stats.CanaryInstances
	releaseChannel.Status.CanaryInstancesUpToDate = stats.CanaryUpToDateInstances
	releaseChannel.Status.Rollout = stats.TotalInstances > 0 && stats.UpToDateInstances == stats.TotalInstances
	releaseChannel.Status.LastReconcileTime = &metav1.Time{Time: time.Now()}
}

// isCanaryInstance checks if an Unleash instance is a canary instance
func (r *ReleaseChannelReconciler) isCanaryInstance(releaseChannel *unleashv1.ReleaseChannel, unleash *unleashv1.Unleash) bool {
	if !releaseChannel.Spec.Strategy.Canary.Enabled {
		return false
	}

	// Check if the instance matches the canary label selector
	selector, err := metav1.LabelSelectorAsSelector(&releaseChannel.Spec.Strategy.Canary.LabelSelector)
	if err != nil {
		return false
	}

	return selector.Matches(labels.Set(unleash.Labels))
}
