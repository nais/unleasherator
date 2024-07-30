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

	log := log.FromContext(ctx).WithName("unleash").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Starting reconciliation of Unleash")

	releaseChannel := &unleashv1.ReleaseChannel{}
	err := r.Get(ctx, req.NamespacedName, releaseChannel)
	if err != nil {
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
	if releaseChannel.Status.Conditions == nil || len(releaseChannel.Status.Conditions) == 0 {
		span.AddEvent("Setting status as unknown for ReleaseChannel")
		log.Info("Setting status as unknown for ReleaseChannel")
		meta.SetStatusCondition(&releaseChannel.Status.Conditions, metav1.Condition{
			Type:    unleashv1.UnleashStatusConditionTypeReconciled,
			Status:  metav1.ConditionUnknown,
			Reason:  "Reconciling",
			Message: "Starting reconciliation",
		})

		if err = r.Status().Update(ctx, releaseChannel); err != nil {
			log.Error(err, "Failed to update ReleaseChannel status")
			return ctrl.Result{}, err
		}

		if err := r.Get(ctx, req.NamespacedName, releaseChannel); err != nil {
			log.Error(err, "Failed to get ReleaseChannel")
			return ctrl.Result{}, err
		}
	}

	// @TODO: upgrade the canaries

	err = r.upgradeInstances(ctx, releaseChannel)
	if err != nil {
		span.RecordError(err)
		log.Error(err, "Failed to upgrade instances")
		return ctrl.Result{Requeue: true}, err
	}

	return ctrl.Result{}, nil
}

// upgradeInstances upgrades all Unleash instances matching the ReleaseChannel
func (r *ReleaseChannelReconciler) upgradeInstances(ctx context.Context, releaseChannel *unleashv1.ReleaseChannel) error {
	ctx, span := r.Tracer.Start(ctx, "Upgrade Unleash Instances")
	defer span.End()

	log := log.FromContext(ctx).WithName("unleash").WithValues("TraceID", span.SpanContext().TraceID())
	log.Info("Upgrading Unleash instances")

	unleashList := &unleashv1.UnleashList{}
	listLimit := 10
	listOptions := []client.ListOption{
		client.InNamespace(releaseChannel.Namespace),
		client.Limit(listLimit),
	}

	for {
		err := r.List(ctx, unleashList, listOptions...)
		if err != nil {
			return fmt.Errorf("failed to list Unleash instances: %w", err)
		}

		for _, unleash := range unleashList.Items {
			if !releaseChannel.IsCandidate(&unleash) {
				continue
			}

			if !releaseChannel.ShouldUpdate(&unleash) {
				continue
			}

			span.AddEvent(fmt.Sprintf("Upgrading Unleash instance %s", unleash.Name))
			log.Info("Upgrading Unleash instance", "name", unleash.Name)
			if err := r.upgradeInstance(ctx, &unleash, releaseChannel); err != nil {
				r.Recorder.Eventf(releaseChannel, "Warning", "UnleashInstanceUpdateFailed", "Failed to update Unleash instance %s", unleash.Name)
				return fmt.Errorf("failed to upgrade Unleash instance: %w", err)
			}

			r.Recorder.Eventf(releaseChannel, "Normal", "UnleashInstanceUpdated", "Updated Unleash instance %s", unleash.Name)
		}

		if unleashList.Continue == "" {
			return nil
		}

		listOptions = []client.ListOption{
			client.InNamespace(releaseChannel.Namespace),
			client.Continue(unleashList.Continue),
			client.Limit(listLimit),
		}
	}
}

// upgradeInstance upgrades the Unleash instance
func (r *ReleaseChannelReconciler) upgradeInstance(ctx context.Context, unleash *unleashv1.Unleash, releaseChannel *unleashv1.ReleaseChannel) error {
	ctx, span := r.Tracer.Start(ctx, "Upgrade Unleash Instances")
	span.SetAttributes(attribute.KeyValue{Key: "UnleashName", Value: attribute.StringValue(unleash.Name)})
	defer span.End()

	log := log.FromContext(ctx).WithName("unleash").WithValues("TraceID", span.SpanContext().TraceID(), "UnleashName", unleash.Name)
	log.Info("Upgrading Unleash instance", "name", unleash.Name)

	if err := r.Get(ctx, client.ObjectKeyFromObject(unleash), unleash); err != nil {
		if apierrors.IsNotFound(err) {
			span.AddEvent(fmt.Sprintf("Unleash instance %s not found", unleash.Name))
			return nil
		}
		return fmt.Errorf("failed to get Unleash instance: %w", err)
	}

	// @TODO: do we need to set the status or annotation of the Unleash instance to prevent it from being overwritten?
	unleash.Spec.CustomImage = releaseChannel.Spec.Image

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
