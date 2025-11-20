package statemachine

import (
	"strings"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DecisionEngine contains the core state machine logic for rollout decisions
type DecisionEngine struct {
	timeProvider TimeProvider
	config       DecisionConfig
}

// DefaultDecisionConfig returns sensible defaults for the decision engine
func DefaultDecisionConfig() DecisionConfig {
	return DecisionConfig{
		MinTriggerInterval:    30 * time.Second,
		FailureRetryDelay:     30 * time.Second,
		StuckDetectionTimeout: 5 * time.Minute,
		MaxFailureCount:       3,
	}
}

// NewDecisionEngine creates a new decision engine with the given config
func NewDecisionEngine(timeProvider TimeProvider, config DecisionConfig) *DecisionEngine {
	return &DecisionEngine{
		timeProvider: timeProvider,
		config:       config,
	}
}

// NewDecisionEngineWithDefaults creates a decision engine with default config
func NewDecisionEngineWithDefaults(timeProvider TimeProvider) *DecisionEngine {
	return NewDecisionEngine(timeProvider, DefaultDecisionConfig())
}

// ShouldTriggerInstance determines if an instance should be triggered for deployment
func (de *DecisionEngine) ShouldTriggerInstance(state InstanceState) TriggerDecision {
	// 1. If instance already has the target image, it's completed
	if state.CurrentImage == state.TargetImage && state.CurrentImage != "" {
		return TriggerDecision{
			ShouldTrigger: false,
			Reason:        "Instance already has target image",
			NewPhase:      InstancePhaseReady,
		}
	}

	// 2. Check for new target image first (highest priority - force retrigger scenario)
	if state.LastTargetImage != "" && state.LastTargetImage != state.TargetImage {
		return TriggerDecision{
			ShouldTrigger: true,
			Reason:        "New target image detected",
			NewPhase:      InstancePhasePending,
		}
	}

	// 3. COORDINATION FIX: If instance is actively being deployed to target image,
	// don't trigger even if annotations show a different last target
	if state.LastTargetImage == state.TargetImage && state.CurrentImage != "" {
		return TriggerDecision{
			ShouldTrigger: false,
			Reason:        "Instance already triggered for current target image",
			NewPhase:      InstancePhaseTriggered,
		}
	}

	// 4. RACE CONDITION FIX: Detect if instance is actively being processed
	// by looking at recent transitions or generation mismatches
	if de.isInstanceActivelyProcessing(state) {
		return TriggerDecision{
			ShouldTrigger: false,
			Reason:        "Instance is actively being processed by Unleash controller",
			NewPhase:      InstancePhaseReconciling,
		}
	}

	// 5. Check if instance has explicitly failed and allow retry
	if state.IsExplicitlyFailed {
		timeSinceFailure := de.timeProvider.Since(state.LastFailureTime)
		if timeSinceFailure >= de.config.FailureRetryDelay {
			if state.FailureCount >= de.config.MaxFailureCount {
				return TriggerDecision{
					ShouldTrigger: false,
					Reason:        "Maximum failure count reached",
					NewPhase:      InstancePhaseFailed,
				}
			}
			return TriggerDecision{
				ShouldTrigger: true,
				Reason:        "Retrying after failure",
				NewPhase:      InstancePhasePending,
			}
		}

		waitTime := de.config.FailureRetryDelay - timeSinceFailure
		return TriggerDecision{
			ShouldTrigger: false,
			Reason:        "Waiting for failure retry delay",
			NewPhase:      InstancePhaseFailed,
			WaitDuration:  waitTime,
		}
	}

	// 5. Check if instance was recently triggered
	if !state.LastTriggerTime.IsZero() {
		timeSinceTrigger := de.timeProvider.Since(state.LastTriggerTime)

		// If it's been too long without progress, consider it stuck
		if timeSinceTrigger >= de.config.StuckDetectionTimeout {
			return TriggerDecision{
				ShouldTrigger: true,
				Reason:        "Instance appears stuck, forcing retrigger",
				NewPhase:      InstancePhaseStuck,
			}
		}

		// If recently triggered, wait for minimum interval
		if timeSinceTrigger < de.config.MinTriggerInterval {
			waitTime := de.config.MinTriggerInterval - timeSinceTrigger
			return TriggerDecision{
				ShouldTrigger: false,
				Reason:        "Recently triggered, allowing time to process",
				NewPhase:      InstancePhaseTriggered,
				WaitDuration:  waitTime,
			}
		}
	}

	// 6. Instance needs initial trigger or retry
	return TriggerDecision{
		ShouldTrigger: true,
		Reason:        "Instance needs deployment",
		NewPhase:      InstancePhasePending,
	}
}

// isInstanceActivelyProcessing detects if an instance is currently being processed
// by the Unleash controller, even without trigger annotations
func (de *DecisionEngine) isInstanceActivelyProcessing(state InstanceState) bool {
	now := de.timeProvider.Now()

	// Check if there are very recent condition transitions (within last 30 seconds)
	// This indicates the Unleash controller is actively working on the instance
	if !state.LastTransitionTime.IsZero() {
		timeSinceTransition := now.Sub(state.LastTransitionTime)
		if timeSinceTransition < 30*time.Second {
			return true
		}
	}

	// Check if instance has a current image that's not the target, but recent transitions
	// suggest it's being updated (common during deployments)
	if state.CurrentImage != "" && state.CurrentImage != state.TargetImage {
		// If we've seen recent activity, assume it's being processed
		if !state.LastTransitionTime.IsZero() {
			timeSinceTransition := now.Sub(state.LastTransitionTime)
			if timeSinceTransition < 2*time.Minute {
				return true
			}
		}
	}

	return false
}

// ExtractInstanceStateFromUnleash extracts the current state from an Unleash instance
func ExtractInstanceStateFromUnleash(instance *unleashv1.Unleash, targetImage string) InstanceState {
	state := InstanceState{
		Name:        instance.Name,
		TargetImage: targetImage,
		Version:     instance.Status.Version,
		Generation:  instance.Generation,
	}

	// Determine current image - prefer ResolvedReleaseChannelImage when available
	if instance.Status.ResolvedReleaseChannelImage != "" {
		state.CurrentImage = instance.Status.ResolvedReleaseChannelImage
	} else if instance.Spec.CustomImage != "" {
		state.CurrentImage = instance.Spec.CustomImage
	}

	// Determine if this is a canary instance (convention: canary instances have "canary" in name)
	state.IsCanary = strings.Contains(strings.ToLower(instance.Name), "canary")

	// In the new status-based architecture, target image tracking is managed by the ReleaseChannel controller
	// The ReleaseChannel controller sets the ResolvedReleaseChannelImage and ReleaseChannelName in status
	// Target image tracking happens through status updates and condition transitions

	// Note: LastTargetImage is now managed by the ReleaseChannel controller internally
	// and doesn't need to be exposed in the instance status for coordination purposes.
	// The decision engine should rely on condition transitions to detect state changes.

	// Extract last trigger time from condition transitions (more reliable than annotations)
	state.LastTriggerTime = extractLastTriggerTimeFromConditions(instance)

	// Enhanced condition analysis
	state.IsExplicitlyFailed = isInstanceExplicitlyFailed(instance)
	state.IsDegraded = isInstanceDegraded(instance)
	state.IsReady = isInstanceReady(instance)

	// Determine phase based on enhanced status
	state.Phase = determineInstancePhase(instance, state)

	// Extract failure information
	if state.IsExplicitlyFailed {
		state.LastFailureTime = extractFailureTime(instance)
		state.FailureReason = extractFailureReason(instance)
	}

	// Extract last transition time
	state.LastTransitionTime = extractLastTransitionTime(instance)

	return state
}

// isInstanceExplicitlyFailed checks if an instance has explicitly failed conditions
func isInstanceExplicitlyFailed(instance *unleashv1.Unleash) bool {
	// Check for explicit failure conditions
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled &&
			condition.Status == metav1.ConditionFalse &&
			(condition.Reason == "Failed" || condition.Reason == "DeploymentFailed" ||
				strings.Contains(condition.Message, "failed") || strings.Contains(condition.Message, "error")) {
			return true
		}
	}

	// Also check for broken images in status
	if instance.Status.ResolvedReleaseChannelImage != "" &&
		strings.Contains(instance.Status.ResolvedReleaseChannelImage, "broken") {
		return true
	}

	return false
}

// extractFailureTime tries to extract when the failure occurred
func extractFailureTime(instance *unleashv1.Unleash) time.Time {
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled &&
			condition.Status == metav1.ConditionFalse {
			return condition.LastTransitionTime.Time
		}
	}
	return time.Now() // Fallback to current time
}

// isInstanceDegraded checks if the instance has degraded conditions
func isInstanceDegraded(instance *unleashv1.Unleash) bool {
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeDegraded &&
			condition.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// isInstanceReady checks if the instance is fully ready (reconciled and connected)
func isInstanceReady(instance *unleashv1.Unleash) bool {
	reconciled := false
	connected := false

	for _, condition := range instance.Status.Conditions {
		switch condition.Type {
		case unleashv1.UnleashStatusConditionTypeReconciled:
			reconciled = condition.Status == metav1.ConditionTrue
		case unleashv1.UnleashStatusConditionTypeConnected:
			connected = condition.Status == metav1.ConditionTrue
		}
	}

	return reconciled && connected
}

// determineInstancePhase determines the current phase based on conditions and state
func determineInstancePhase(instance *unleashv1.Unleash, state InstanceState) InstancePhase {
	// Failed state takes priority
	if state.IsExplicitlyFailed {
		return InstancePhaseFailed
	}

	// Degraded state
	if state.IsDegraded {
		return InstancePhaseDegraded
	}

	// Ready state
	if state.IsReady && state.CurrentImage == state.TargetImage {
		return InstancePhaseReady
	}

	// Check if deployment is in progress
	if state.CurrentImage != state.TargetImage && state.CurrentImage != "" {
		return InstancePhaseDeploying
	}

	// If we have a trigger time but no current image, it's triggered
	if !state.LastTriggerTime.IsZero() {
		return InstancePhaseTriggered
	}

	// Default to pending
	return InstancePhasePending
}

// extractLastTriggerTimeFromConditions attempts to determine when the instance was last triggered
// by analyzing condition transitions. In the new status-based architecture, trigger timing
// can be inferred from reconciliation condition changes.
func extractLastTriggerTimeFromConditions(instance *unleashv1.Unleash) time.Time {
	// Look for recent reconciliation condition transitions that might indicate triggering
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled {
			// A transition to "Processing" or similar states indicates recent triggering
			if condition.Reason == "Processing" || condition.Reason == "Deploying" ||
				condition.Reason == "Reconciling" {
				return condition.LastTransitionTime.Time
			}
		}
	}

	// If no specific trigger indicators found, return zero time
	return time.Time{}
}

// extractFailureReason extracts a specific failure reason from conditions
func extractFailureReason(instance *unleashv1.Unleash) string {
	for _, condition := range instance.Status.Conditions {
		if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled &&
			condition.Status == metav1.ConditionFalse {
			if condition.Reason != "" {
				return condition.Reason
			}
			return condition.Message
		}
	}
	return "Unknown failure"
}

// extractLastTransitionTime gets the most recent condition transition time
func extractLastTransitionTime(instance *unleashv1.Unleash) time.Time {
	var latest time.Time
	for _, condition := range instance.Status.Conditions {
		if condition.LastTransitionTime.After(latest) {
			latest = condition.LastTransitionTime.Time
		}
	}
	return latest
}
