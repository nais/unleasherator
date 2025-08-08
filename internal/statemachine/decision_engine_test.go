package statemachine

import (
	"testing"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDecisionEngine_ShouldTriggerInstance(t *testing.T) {
	// Setup mock time provider
	baseTime := time.Date(2025, 8, 8, 12, 0, 0, 0, time.UTC)
	mockTime := &MockTimeProvider{CurrentTime: baseTime}

	config := DecisionConfig{
		MinTriggerInterval:    30 * time.Second,
		FailureRetryDelay:     30 * time.Second,
		StuckDetectionTimeout: 5 * time.Minute,
		MaxFailureCount:       3,
	}

	engine := NewDecisionEngine(mockTime, config)

	tests := []struct {
		name           string
		state          InstanceState
		setupTime      func(*MockTimeProvider)
		expectedResult TriggerDecision
	}{
		{
			name: "instance already has target image",
			state: InstanceState{
				Name:         "test-instance",
				CurrentImage: "app:v2.0",
				TargetImage:  "app:v2.0",
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: false,
				Reason:        "Instance already has target image",
				NewPhase:      InstancePhaseReady,
			},
		},
		{
			name: "new target image detected - force retrigger",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTargetImage: "app:v1.5", // Different from current target
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: true,
				Reason:        "New target image detected",
				NewPhase:      InstancePhasePending,
			},
		},
		{
			name: "explicit failure - allow retry after delay",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				IsExplicitlyFailed: true,
				LastFailureTime:    baseTime.Add(-35 * time.Second), // 35 seconds ago
				FailureCount:       1,
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: true,
				Reason:        "Retrying after failure",
				NewPhase:      InstancePhasePending,
			},
		},
		{
			name: "explicit failure - too soon to retry",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				IsExplicitlyFailed: true,
				LastFailureTime:    baseTime.Add(-15 * time.Second), // 15 seconds ago
				FailureCount:       1,
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: false,
				Reason:        "Waiting for failure retry delay",
				NewPhase:      InstancePhaseFailed,
				WaitDuration:  15 * time.Second, // 30s - 15s = 15s remaining
			},
		},
		{
			name: "max failure count reached",
			state: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:v1.0",
				TargetImage:        "app:v2.0",
				IsExplicitlyFailed: true,
				LastFailureTime:    baseTime.Add(-35 * time.Second),
				FailureCount:       3, // At max failure count
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: false,
				Reason:        "Maximum failure count reached",
				NewPhase:      InstancePhaseFailed,
			},
		},
		{
			name: "instance stuck - force retrigger",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTriggerTime: baseTime.Add(-6 * time.Minute), // 6 minutes ago
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: true,
				Reason:        "Instance appears stuck, forcing retrigger",
				NewPhase:      InstancePhaseStuck,
			},
		},
		{
			name: "recently triggered - wait for minimum interval",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTriggerTime: baseTime.Add(-15 * time.Second), // 15 seconds ago
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: false,
				Reason:        "Recently triggered, allowing time to process",
				NewPhase:      InstancePhaseTriggered,
				WaitDuration:  15 * time.Second, // 30s - 15s = 15s remaining
			},
		},
		{
			name: "needs initial deployment",
			state: InstanceState{
				Name:         "test-instance",
				CurrentImage: "",
				TargetImage:  "app:v2.0",
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: true,
				Reason:        "Instance needs deployment",
				NewPhase:      InstancePhasePending,
			},
		},
		{
			name: "ready for retry after minimum interval",
			state: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTriggerTime: baseTime.Add(-35 * time.Second), // 35 seconds ago
			},
			expectedResult: TriggerDecision{
				ShouldTrigger: true,
				Reason:        "Instance needs deployment",
				NewPhase:      InstancePhasePending,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset time to base
			mockTime.SetTime(baseTime)

			// Apply any time setup
			if tt.setupTime != nil {
				tt.setupTime(mockTime)
			}

			result := engine.ShouldTriggerInstance(tt.state)

			assert.Equal(t, tt.expectedResult.ShouldTrigger, result.ShouldTrigger, "ShouldTrigger mismatch")
			assert.Equal(t, tt.expectedResult.Reason, result.Reason, "Reason mismatch")
			assert.Equal(t, tt.expectedResult.NewPhase, result.NewPhase, "NewPhase mismatch")

			// Check wait duration with some tolerance for time calculations
			if tt.expectedResult.WaitDuration > 0 {
				assert.InDelta(t, tt.expectedResult.WaitDuration.Seconds(), result.WaitDuration.Seconds(), 1.0, "WaitDuration mismatch")
			}
		})
	}
}

func TestExtractInstanceStateFromUnleash(t *testing.T) {
	baseTime := time.Now()

	tests := []struct {
		name          string
		instance      *unleashv1.Unleash
		targetImage   string
		expectedState InstanceState
	}{
		{
			name: "basic instance with resolved image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-instance",
				},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: "app:v1.0",
				},
			},
			targetImage: "app:v2.0",
			expectedState: InstanceState{
				Name:         "test-instance",
				CurrentImage: "app:v1.0",
				TargetImage:  "app:v2.0",
				Phase:        InstancePhaseDeploying, // Different images = deployment in progress
			},
		},
		{
			name: "instance with annotations and trigger time",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-instance",
					Annotations: map[string]string{
						"releasechannel.unleash.nais.io/last-target-image":    "app:v1.5",
						"releasechannel.unleash.nais.io/last-rollout-trigger": baseTime.Format(time.RFC3339),
					},
				},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: "app:v1.0",
				},
			},
			targetImage: "app:v2.0",
			expectedState: InstanceState{
				Name:            "test-instance",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTargetImage: "app:v1.5",
				LastTriggerTime: baseTime,
				Phase:           InstancePhaseDeploying, // Different images = deployment in progress
			},
		},
		{
			name: "instance with failure condition",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-instance",
				},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: "app:broken",
					Conditions: []metav1.Condition{
						{
							Type:               unleashv1.UnleashStatusConditionTypeReconciled,
							Status:             metav1.ConditionFalse,
							Reason:             "Failed",
							Message:            "Deployment failed",
							LastTransitionTime: metav1.NewTime(baseTime),
						},
					},
				},
			},
			targetImage: "app:v2.0",
			expectedState: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:broken",
				TargetImage:        "app:v2.0",
				IsExplicitlyFailed: true,
				LastFailureTime:    baseTime,
				Phase:              InstancePhaseFailed,
			},
		},
		{
			name: "instance with broken image in status",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-instance",
				},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: "app:broken-image",
				},
			},
			targetImage: "app:v2.0",
			expectedState: InstanceState{
				Name:               "test-instance",
				CurrentImage:       "app:broken-image",
				TargetImage:        "app:v2.0",
				IsExplicitlyFailed: true,
				Phase:              InstancePhaseFailed,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractInstanceStateFromUnleash(tt.instance, tt.targetImage)

			assert.Equal(t, tt.expectedState.Name, result.Name)
			assert.Equal(t, tt.expectedState.CurrentImage, result.CurrentImage)
			assert.Equal(t, tt.expectedState.TargetImage, result.TargetImage)
			assert.Equal(t, tt.expectedState.LastTargetImage, result.LastTargetImage)
			assert.Equal(t, tt.expectedState.IsExplicitlyFailed, result.IsExplicitlyFailed)
			assert.Equal(t, tt.expectedState.Phase, result.Phase)

			// Check times with tolerance
			if !tt.expectedState.LastTriggerTime.IsZero() {
				assert.WithinDuration(t, tt.expectedState.LastTriggerTime, result.LastTriggerTime, time.Second)
			}
			if !tt.expectedState.LastFailureTime.IsZero() {
				assert.WithinDuration(t, tt.expectedState.LastFailureTime, result.LastFailureTime, time.Second)
			}
		})
	}
}

func TestDecisionEngine_EdgeCases(t *testing.T) {
	baseTime := time.Date(2025, 8, 8, 12, 0, 0, 0, time.UTC)
	mockTime := &MockTimeProvider{CurrentTime: baseTime}
	engine := NewDecisionEngineWithDefaults(mockTime)

	t.Run("rollback scenario - new target is previous version", func(t *testing.T) {
		state := InstanceState{
			Name:            "test-instance",
			CurrentImage:    "app:broken-v2.0", // Current broken version
			TargetImage:     "app:v1.0",        // Rolling back to v1.0
			LastTargetImage: "app:v2.0",        // Previous target was v2.0
			LastTriggerTime: baseTime.Add(-1 * time.Minute),
		}

		result := engine.ShouldTriggerInstance(state)

		assert.True(t, result.ShouldTrigger, "Should trigger rollback immediately")
		assert.Equal(t, "New target image detected", result.Reason)
		assert.Equal(t, InstancePhasePending, result.NewPhase)
	})

	t.Run("rapid fire new versions", func(t *testing.T) {
		// Simulate rapid deployment of v2, then v3, then v4
		state := InstanceState{
			Name:            "test-instance",
			CurrentImage:    "app:v1.0",
			TargetImage:     "app:v4.0",
			LastTargetImage: "app:v3.0",                     // Just deployed v3, now want v4
			LastTriggerTime: baseTime.Add(-5 * time.Second), // Very recent
		}

		result := engine.ShouldTriggerInstance(state)

		assert.True(t, result.ShouldTrigger, "Should trigger new version despite recent trigger")
		assert.Equal(t, "New target image detected", result.Reason)
	})

	t.Run("instance with empty current image but recently triggered", func(t *testing.T) {
		state := InstanceState{
			Name:            "test-instance",
			CurrentImage:    "", // Empty - still resolving
			TargetImage:     "app:v2.0",
			LastTriggerTime: baseTime.Add(-10 * time.Second), // Recent trigger
		}

		result := engine.ShouldTriggerInstance(state)

		assert.False(t, result.ShouldTrigger, "Should wait for recent trigger to complete")
		assert.Equal(t, "Recently triggered, allowing time to process", result.Reason)
	})
}

func TestDecisionEngine_BatchScenarios(t *testing.T) {
	baseTime := time.Date(2025, 8, 8, 12, 0, 0, 0, time.UTC)
	mockTime := &MockTimeProvider{CurrentTime: baseTime}
	engine := NewDecisionEngineWithDefaults(mockTime)

	t.Run("batch with mixed states", func(t *testing.T) {
		instances := []InstanceState{
			{
				Name:         "instance-1",
				CurrentImage: "app:v2.0",
				TargetImage:  "app:v2.0", // Already up to date
			},
			{
				Name:            "instance-2",
				CurrentImage:    "app:v1.0",
				TargetImage:     "app:v2.0",
				LastTriggerTime: baseTime.Add(-10 * time.Second), // Recently triggered
			},
			{
				Name:         "instance-3",
				CurrentImage: "app:v1.0",
				TargetImage:  "app:v2.0", // Needs trigger
			},
		}

		results := make([]TriggerDecision, len(instances))
		for i, instance := range instances {
			results[i] = engine.ShouldTriggerInstance(instance)
		}

		// Instance 1: Already complete
		assert.False(t, results[0].ShouldTrigger)
		assert.Equal(t, InstancePhaseReady, results[0].NewPhase)

		// Instance 2: Recently triggered, should wait
		assert.False(t, results[1].ShouldTrigger)
		assert.Equal(t, InstancePhaseTriggered, results[1].NewPhase)

		// Instance 3: Needs trigger
		assert.True(t, results[2].ShouldTrigger)
		assert.Equal(t, InstancePhasePending, results[2].NewPhase)

		// Only one instance should need triggering
		triggerCount := 0
		for _, result := range results {
			if result.ShouldTrigger {
				triggerCount++
			}
		}
		assert.Equal(t, 1, triggerCount, "Only one instance should need triggering")
	})
}

func TestTimeProvider(t *testing.T) {
	t.Run("MockTimeProvider", func(t *testing.T) {
		baseTime := time.Date(2025, 8, 8, 12, 0, 0, 0, time.UTC)
		mock := &MockTimeProvider{CurrentTime: baseTime}

		// Test Now()
		assert.Equal(t, baseTime, mock.Now())

		// Test SetTime()
		newTime := baseTime.Add(time.Hour)
		mock.SetTime(newTime)
		assert.Equal(t, newTime, mock.Now())

		// Test AdvanceTime()
		mock.AdvanceTime(30 * time.Minute)
		expected := newTime.Add(30 * time.Minute)
		assert.Equal(t, expected, mock.Now())

		// Test Since()
		pastTime := baseTime.Add(-10 * time.Minute)
		duration := mock.Since(pastTime)
		expectedDuration := expected.Sub(pastTime)
		assert.Equal(t, expectedDuration, duration)
	})

	t.Run("RealTimeProvider", func(t *testing.T) {
		provider := RealTimeProvider{}

		before := time.Now()
		now := provider.Now()
		after := time.Now()

		assert.True(t, now.After(before) || now.Equal(before))
		assert.True(t, now.Before(after) || now.Equal(after))

		// Test Since with a known past time
		pastTime := time.Now().Add(-time.Second)
		duration := provider.Since(pastTime)
		assert.True(t, duration >= time.Second)
		assert.True(t, duration <= 2*time.Second) // Allow some margin
	})
}
