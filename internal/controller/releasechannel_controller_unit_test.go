package controller

import (
	"context"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetExpectedImageForInstance(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	tests := []struct {
		name                 string
		instance             *unleashv1.Unleash
		targetImage          string
		releaseChannel       *unleashv1.ReleaseChannel
		expectedImage        string
		releaseChannelExists bool
	}{
		{
			name: "no release channel label - returns target image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "default",
					Labels:    map[string]string{},
				},
				Spec: unleashv1.UnleashSpec{
					// No ReleaseChannel specified
				},
			},
			targetImage:   "test:v2",
			expectedImage: "test:v2",
		},
		{
			name: "release channel not found - returns target image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "default",
					Labels: map[string]string{
						"release-channel": "nonexistent-channel",
					},
				},
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "nonexistent-channel",
					},
				},
			},
			targetImage:          "test:v2",
			expectedImage:        "test:v2",
			releaseChannelExists: false,
		},
		{
			name: "no rollout in progress - returns target image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "default",
					Labels: map[string]string{
						"release-channel": "test-channel",
					},
				},
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
				},
			},
			targetImage: "test:v2",
			releaseChannel: &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "default",
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
				},
				Status: unleashv1.ReleaseChannelStatus{
					Phase:         unleashv1.ReleaseChannelPhaseIdle,
					PreviousImage: "", // No previous image means no rollout
				},
			},
			expectedImage:        "test:v2",
			releaseChannelExists: true,
		},
		{
			name: "canary phase - canary instance gets target image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "canary-unleash",
					Namespace: "default",
					Labels: map[string]string{
						"release-channel": "test-channel",
						"stage":           "canary", // Matches canary selector
					},
				},
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
				},
			},
			targetImage: "test:v2",
			releaseChannel: &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "default",
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"stage": "canary",
								},
							},
						},
					},
				},
				Status: unleashv1.ReleaseChannelStatus{
					Phase:         unleashv1.ReleaseChannelPhaseCanary,
					PreviousImage: "test:v1",
				},
			},
			expectedImage:        "test:v2",
			releaseChannelExists: true,
		},
		{
			name: "canary phase - production instance gets previous image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "production-unleash",
					Namespace: "default",
					Labels: map[string]string{
						"release-channel": "test-channel",
						"stage":           "production", // Does not match canary selector
					},
				},
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
				},
			},
			targetImage: "test:v2",
			releaseChannel: &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "default",
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"stage": "canary",
								},
							},
						},
					},
				},
				Status: unleashv1.ReleaseChannelStatus{
					Phase:         unleashv1.ReleaseChannelPhaseCanary,
					PreviousImage: "test:v1",
				},
			},
			expectedImage:        "test:v1",
			releaseChannelExists: true,
		},
		{
			name: "rolling phase - all instances get target image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "production-unleash",
					Namespace: "default",
					Labels: map[string]string{
						"release-channel": "test-channel",
						"stage":           "production",
					},
				},
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
				},
			},
			targetImage: "test:v2",
			releaseChannel: &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "default",
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"stage": "canary",
								},
							},
						},
					},
				},
				Status: unleashv1.ReleaseChannelStatus{
					Phase:         unleashv1.ReleaseChannelPhaseRolling,
					PreviousImage: "test:v1",
				},
			},
			expectedImage:        "test:v2",
			releaseChannelExists: true,
		},
		{
			name: "rollback phase - all instances get previous image",
			instance: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash",
					Namespace: "default",
					Labels: map[string]string{
						"release-channel": "test-channel",
					},
				},
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "test-channel",
					},
				},
			},
			targetImage: "test:v2",
			releaseChannel: &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-channel",
					Namespace: "default",
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
				},
				Status: unleashv1.ReleaseChannelStatus{
					Phase:         unleashv1.ReleaseChannelPhaseRollingBack,
					PreviousImage: "test:v1",
				},
			},
			expectedImage:        "test:v1",
			releaseChannelExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []runtime.Object
			if tt.releaseChannelExists && tt.releaseChannel != nil {
				objects = append(objects, tt.releaseChannel)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			reconciler := &ReleaseChannelReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: &record.FakeRecorder{},
			}

			result := reconciler.getExpectedImageForInstance(context.Background(), tt.instance, tt.targetImage)
			assert.Equal(t, tt.expectedImage, result)
		})
	}
}

func TestGetCanaryInstances(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	reconciler := &ReleaseChannelReconciler{
		Scheme:   scheme,
		Recorder: &record.FakeRecorder{},
	}

	tests := []struct {
		name           string
		instances      []unleashv1.Unleash
		releaseChannel *unleashv1.ReleaseChannel
		expectedCount  int
		expectedNames  []string
	}{
		{
			name:      "canary disabled - returns empty",
			instances: []unleashv1.Unleash{},
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: false,
						},
					},
				},
			},
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name: "canary enabled - filters by label selector",
			instances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "canary-instance",
						Labels: map[string]string{
							"stage": "canary",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "production-instance",
						Labels: map[string]string{
							"stage": "production",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "another-canary-instance",
						Labels: map[string]string{
							"stage": "canary",
						},
					},
				},
			},
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"stage": "canary",
								},
							},
						},
					},
				},
			},
			expectedCount: 2,
			expectedNames: []string{"canary-instance", "another-canary-instance"},
		},
		{
			name: "no instances match selector",
			instances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "production-instance",
						Labels: map[string]string{
							"stage": "production",
						},
					},
				},
			},
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"stage": "canary",
								},
							},
						},
					},
				},
			},
			expectedCount: 0,
			expectedNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.getCanaryInstances(tt.instances, tt.releaseChannel)

			assert.Equal(t, tt.expectedCount, len(result))

			if tt.expectedCount > 0 {
				actualNames := make([]string, len(result))
				for i, instance := range result {
					actualNames[i] = instance.Name
				}
				assert.ElementsMatch(t, tt.expectedNames, actualNames)
			}
		})
	}
}

func TestGetInstancesToUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	reconciler := &ReleaseChannelReconciler{
		Scheme:   scheme,
		Recorder: &record.FakeRecorder{},
	}

	tests := []struct {
		name           string
		instances      []unleashv1.Unleash
		releaseChannel *unleashv1.ReleaseChannel
		expectedCount  int
		expectedNames  []string
	}{
		{
			name: "all instances up to date",
			instances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "instance1"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "instance2"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2",
					},
				},
			},
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
				},
			},
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name: "some instances need updates",
			instances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "outdated-instance1"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v1",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "up-to-date-instance"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "outdated-instance2"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v1",
					},
				},
			},
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
				},
			},
			expectedCount: 2,
			expectedNames: []string{"outdated-instance1", "outdated-instance2"},
		},
		{
			name: "empty resolved image needs update",
			instances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "unresolved-instance"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "",
					},
				},
			},
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v1",
				},
			},
			expectedCount: 1,
			expectedNames: []string{"unresolved-instance"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.getInstancesToUpdate(tt.instances, tt.releaseChannel)

			assert.Equal(t, tt.expectedCount, len(result))

			if tt.expectedCount > 0 {
				actualNames := make([]string, len(result))
				for i, instance := range result {
					actualNames[i] = instance.Name
				}
				assert.ElementsMatch(t, tt.expectedNames, actualNames)
			}
		})
	}
}

func TestMatchesLabelSelector(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	reconciler := &ReleaseChannelReconciler{
		Scheme:   scheme,
		Recorder: &record.FakeRecorder{},
	}

	tests := []struct {
		name     string
		instance unleashv1.Unleash
		selector metav1.LabelSelector
		expected bool
	}{
		{
			name: "exact match",
			instance: unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"stage": "canary",
						"app":   "unleash",
					},
				},
			},
			selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"stage": "canary",
				},
			},
			expected: true,
		},
		{
			name: "no match",
			instance: unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"stage": "production",
						"app":   "unleash",
					},
				},
			},
			selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"stage": "canary",
				},
			},
			expected: false,
		},
		{
			name: "multiple labels match",
			instance: unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"stage":       "canary",
						"app":         "unleash",
						"environment": "test",
					},
				},
			},
			selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"stage": "canary",
					"app":   "unleash",
				},
			},
			expected: true,
		},
		{
			name: "partial match fails",
			instance: unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"stage": "canary",
					},
				},
			},
			selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"stage": "canary",
					"app":   "unleash",
				},
			},
			expected: false,
		},
		{
			name: "empty labels don't match",
			instance: unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{},
				},
			},
			selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"stage": "canary",
				},
			},
			expected: false,
		},
		{
			name: "nil labels don't match",
			instance: unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Labels: nil,
				},
			},
			selector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					"stage": "canary",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.matchesLabelSelector(tt.instance, tt.selector)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsInstanceReady(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	reconciler := &ReleaseChannelReconciler{
		Scheme:   scheme,
		Recorder: &record.FakeRecorder{},
	}

	tests := []struct {
		name     string
		instance *unleashv1.Unleash
		expected bool
	}{
		{
			name: "ready instance",
			instance: &unleashv1.Unleash{
				Status: unleashv1.UnleashStatus{
					Conditions: []metav1.Condition{
						{
							Type:   unleashv1.UnleashStatusConditionTypeReconciled,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "not ready instance",
			instance: &unleashv1.Unleash{
				Status: unleashv1.UnleashStatus{
					Conditions: []metav1.Condition{
						{
							Type:   unleashv1.UnleashStatusConditionTypeReconciled,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "unknown condition status",
			instance: &unleashv1.Unleash{
				Status: unleashv1.UnleashStatus{
					Conditions: []metav1.Condition{
						{
							Type:   unleashv1.UnleashStatusConditionTypeReconciled,
							Status: metav1.ConditionUnknown,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "no reconciled condition",
			instance: &unleashv1.Unleash{
				Status: unleashv1.UnleashStatus{
					Conditions: []metav1.Condition{
						{
							Type:   unleashv1.UnleashStatusConditionTypeConnected,
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "no conditions",
			instance: &unleashv1.Unleash{
				Status: unleashv1.UnleashStatus{
					Conditions: []metav1.Condition{},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.isInstanceReady(tt.instance)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUpdateInstanceCounts(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	reconciler := &ReleaseChannelReconciler{
		Scheme:   scheme,
		Recorder: &record.FakeRecorder{},
	}

	tests := []struct {
		name                            string
		releaseChannel                  *unleashv1.ReleaseChannel
		targetInstances                 []unleashv1.Unleash
		expectedInstances               int
		expectedInstancesUpToDate       int
		expectedCanaryInstances         int
		expectedCanaryInstancesUpToDate int
		expectedProgress                int
	}{
		{
			name: "all instances up to date without canary",
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: false,
						},
					},
				},
			},
			targetInstances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "instance1"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "instance2"},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2",
					},
				},
			},
			expectedInstances:               2,
			expectedInstancesUpToDate:       2,
			expectedCanaryInstances:         0,
			expectedCanaryInstancesUpToDate: 0,
			expectedProgress:                100,
		},
		{
			name: "mixed state with canary deployment",
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"stage": "canary",
								},
							},
						},
					},
				},
			},
			targetInstances: []unleashv1.Unleash{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "canary-instance",
						Labels: map[string]string{
							"stage": "canary",
						},
					},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2", // Up to date
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "production-instance1",
						Labels: map[string]string{
							"stage": "production",
						},
					},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v1", // Not up to date
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "production-instance2",
						Labels: map[string]string{
							"stage": "production",
						},
					},
					Status: unleashv1.UnleashStatus{
						ResolvedReleaseChannelImage: "test:v2", // Up to date
					},
				},
			},
			expectedInstances:               3,
			expectedInstancesUpToDate:       2, // canary + production-instance2
			expectedCanaryInstances:         1,
			expectedCanaryInstancesUpToDate: 1,
			expectedProgress:                66, // 2/3 * 100 = 66
		},
		{
			name: "no instances",
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test:v2",
				},
			},
			targetInstances:                 []unleashv1.Unleash{},
			expectedInstances:               0,
			expectedInstancesUpToDate:       0,
			expectedCanaryInstances:         0,
			expectedCanaryInstancesUpToDate: 0,
			expectedProgress:                100, // 100% when no instances
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset status
			tt.releaseChannel.Status = unleashv1.ReleaseChannelStatus{}

			reconciler.updateInstanceCounts(tt.releaseChannel, tt.targetInstances)

			assert.Equal(t, tt.expectedInstances, tt.releaseChannel.Status.Instances)
			assert.Equal(t, tt.expectedInstancesUpToDate, tt.releaseChannel.Status.InstancesUpToDate)
			assert.Equal(t, tt.expectedCanaryInstances, tt.releaseChannel.Status.CanaryInstances)
			assert.Equal(t, tt.expectedCanaryInstancesUpToDate, tt.releaseChannel.Status.CanaryInstancesUpToDate)
			assert.Equal(t, tt.expectedProgress, tt.releaseChannel.Status.Progress)
		})
	}
}
