package statemachine

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCoordinationScenarios(t *testing.T) {
	tests := []struct {
		name            string
		state           InstanceState
		expectedTrigger bool
		expectedReason  string
		expectedPhase   InstancePhase
		description     string
	}{
		{
			name: "instance already has target image - no trigger",
			state: InstanceState{
				Name:         "test-instance",
				CurrentImage: "app:v2.0",
				TargetImage:  "app:v2.0",
			},
			expectedTrigger: false,
			expectedReason:  "Instance already has target image",
			expectedPhase:   InstancePhaseReady,
			description:     "When instance already has the target image, no triggering should occur",
		},
		{
			name: "instance already triggered for current target - prevent race",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTargetImage: "app:v2.0", // Already triggered for this target
			},
			expectedTrigger: false,
			expectedReason:  "Instance already triggered for current target image",
			expectedPhase:   InstancePhaseTriggered,
			description:     "Prevents re-triggering when we've already triggered for this target",
		},
		{
			name: "instance actively processing - recent transition",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				LastTransitionTime: time.Now().Add(-10 * time.Second), // Recent activity
			},
			expectedTrigger: false,
			expectedReason:  "Instance is actively being processed by Unleash controller",
			expectedPhase:   InstancePhaseReconciling,
			description:     "Detects active processing by recent condition transitions",
		},
		{
			name: "instance actively processing - recent deployment activity",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				LastTransitionTime: time.Now().Add(-1 * time.Minute), // Recent activity during deployment
			},
			expectedTrigger: false,
			expectedReason:  "Instance is actively being processed by Unleash controller",
			expectedPhase:   InstancePhaseReconciling,
			description:     "Detects active processing during deployment transitions",
		},
		{
			name: "new target image - force retrigger",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v3.0",
				LastTargetImage: "app:v2.0", // Different from current target
			},
			expectedTrigger: true,
			expectedReason:  "New target image detected",
			expectedPhase:   InstancePhasePending,
			description:     "Forces retrigger when target image changes",
		},
		{
			name: "brand new instance - needs initial trigger",
			state: InstanceState{
				Name:         "test-instance",
				CurrentImage: "",
				TargetImage:  "app:v2.0",
			},
			expectedTrigger: true,
			expectedReason:  "Instance needs deployment",
			expectedPhase:   InstancePhasePending,
			description:     "New instances with no current image should be triggered",
		},
		{
			name: "stale instance - no recent activity",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				LastTransitionTime: time.Now().Add(-10 * time.Minute), // Old activity
			},
			expectedTrigger: true,
			expectedReason:  "Instance needs deployment",
			expectedPhase:   InstancePhasePending,
			description:     "Instances with old transitions should be triggered if needed",
		},
		{
			name: "failed instance ready for retry",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				IsExplicitlyFailed: true,
				LastFailureTime:    time.Now().Add(-1 * time.Minute), // Failed a minute ago
				FailureCount:       1,
			},
			expectedTrigger: true,
			expectedReason:  "Retrying after failure",
			expectedPhase:   InstancePhasePending,
			description:     "Failed instances should retry after delay",
		},
		{
			name: "recently triggered - wait for processing",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTriggerTime: time.Now().Add(-10 * time.Second), // Recently triggered
			},
			expectedTrigger: false,
			expectedReason:  "Recently triggered, allowing time to process",
			expectedPhase:   InstancePhaseTriggered,
			description:     "Recently triggered instances should wait before retry",
		},
	}

	timeProvider := &MockTimeProvider{CurrentTime: time.Now()}
	engine := NewDecisionEngine(timeProvider, DefaultDecisionConfig())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision := engine.ShouldTriggerInstance(tt.state)

			assert.Equal(t, tt.expectedTrigger, decision.ShouldTrigger,
				"Expected trigger decision mismatch: %s", tt.description)
			assert.Equal(t, tt.expectedReason, decision.Reason,
				"Expected reason mismatch: %s", tt.description)
			assert.Equal(t, tt.expectedPhase, decision.NewPhase,
				"Expected phase mismatch: %s", tt.description)

			t.Logf("✅ %s: %s (trigger=%v, reason=%s, phase=%s)",
				tt.name, tt.description, decision.ShouldTrigger, decision.Reason, decision.NewPhase)
		})
	}
}

func TestRaceConditionPrevention(t *testing.T) {
	timeProvider := &MockTimeProvider{CurrentTime: time.Now()}
	engine := NewDecisionEngine(timeProvider, DefaultDecisionConfig())

	t.Run("simultaneous processing detection", func(t *testing.T) {
		// Scenario: Unleash controller just started processing, but ReleaseChannel
		// hasn't updated status yet
		state := InstanceState{
			Name:               "unleash-v7-channel",
			CurrentImage:       "", // No image yet (initial deployment)
			TargetImage:        "unleashorg/unleash-server:7",
			LastTargetImage:    "",                               // No previous target tracking yet
			LastTriggerTime:    time.Time{},                      // No trigger yet
			LastTransitionTime: time.Now().Add(-5 * time.Second), // Recent activity from Unleash controller
		}

		decision := engine.ShouldTriggerInstance(state)

		// Should NOT trigger because instance is actively being processed
		assert.False(t, decision.ShouldTrigger,
			"Should not trigger when instance is being actively processed")
		assert.Equal(t, "Instance is actively being processed by Unleash controller", decision.Reason)
		assert.Equal(t, InstancePhaseReconciling, decision.NewPhase)
	})

	t.Run("status already updated - no retrigger", func(t *testing.T) {
		// Scenario: ReleaseChannel already triggered, status tracking exists
		state := InstanceState{
			Name:            "unleash-v7-channel",
			CurrentImage:    "unleashorg/unleash-server:6",     // Old image
			TargetImage:     "unleashorg/unleash-server:7",     // Target image
			LastTargetImage: "unleashorg/unleash-server:7",     // Already triggered for this target
			LastTriggerTime: time.Now().Add(-20 * time.Second), // Triggered 20 seconds ago
		}

		decision := engine.ShouldTriggerInstance(state)

		// Should NOT retrigger because we already triggered for this target
		assert.False(t, decision.ShouldTrigger,
			"Should not retrigger when already triggered for current target")
		assert.Equal(t, "Instance already triggered for current target image", decision.Reason)
		assert.Equal(t, InstancePhaseTriggered, decision.NewPhase)
	})

	t.Run("image change during processing", func(t *testing.T) {
		// Scenario: Target image changes while instance is being processed
		state := InstanceState{
			Name:               "unleash-v7-channel",
			CurrentImage:       "unleashorg/unleash-server:6",     // Old image
			TargetImage:        "unleashorg/unleash-server:8",     // NEW target image
			LastTargetImage:    "unleashorg/unleash-server:7",     // Previously triggered for v7
			LastTriggerTime:    time.Now().Add(-30 * time.Second), // Triggered for v7
			LastTransitionTime: time.Now().Add(-10 * time.Second), // Recent activity
		}

		decision := engine.ShouldTriggerInstance(state)

		// SHOULD trigger because target image changed
		assert.True(t, decision.ShouldTrigger,
			"Should trigger when target image changes")
		assert.Equal(t, "New target image detected", decision.Reason)
		assert.Equal(t, InstancePhasePending, decision.NewPhase)
	})
}

func TestCoordinationEdgeCases(t *testing.T) {
	timeProvider := &MockTimeProvider{CurrentTime: time.Now()}
	engine := NewDecisionEngine(timeProvider, DefaultDecisionConfig())

	t.Run("very recent transition should prevent triggering", func(t *testing.T) {
		state := InstanceState{
			Name:               "test-instance",
			CurrentImage:       "app:v1.0",
			TargetImage:        "app:v2.0",
			LastTransitionTime: time.Now().Add(-5 * time.Second), // Very recent
		}

		decision := engine.ShouldTriggerInstance(state)
		assert.False(t, decision.ShouldTrigger)
		assert.Contains(t, decision.Reason, "actively being processed")
	})

	t.Run("old transition should allow triggering", func(t *testing.T) {
		state := InstanceState{
			Name:               "test-instance",
			CurrentImage:       "app:v1.0",
			TargetImage:        "app:v2.0",
			LastTransitionTime: time.Now().Add(-5 * time.Minute), // Old
		}

		decision := engine.ShouldTriggerInstance(state)
		assert.True(t, decision.ShouldTrigger)
		assert.Equal(t, "Instance needs deployment", decision.Reason)
	})

	t.Run("missing timestamps should not prevent triggering", func(t *testing.T) {
		state := InstanceState{
			Name:               "test-instance",
			CurrentImage:       "app:v1.0",
			TargetImage:        "app:v2.0",
			LastTransitionTime: time.Time{}, // Zero time
		}

		decision := engine.ShouldTriggerInstance(state)
		assert.True(t, decision.ShouldTrigger)
		assert.Equal(t, "Instance needs deployment", decision.Reason)
	})
}
