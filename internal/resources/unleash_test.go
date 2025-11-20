package resources

import (
	"context"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestDeploymentForUnleash(t *testing.T) {
	var err error
	var u *unleashv1.Unleash

	err = unleashv1.AddToScheme(scheme.Scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	u = &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
		},
	}
	_, err = DeploymentForUnleash(u, scheme.Scheme)
	if err == nil {
		t.Error("expected error when no database is configured")
	}

	u = &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Database: unleashv1.UnleashDatabaseConfig{
				SecretName:   "unleash-db",
				SecretURLKey: "url",
			},
		},
	}

	_, err = DeploymentForUnleash(u, scheme.Scheme)
	if err != nil {
		t.Error("expected no when database is configured, got", err)
	}

	u.Spec.PodLabels = map[string]string{
		"app": "unleash",
		"env": "dev",
	}

	u.Spec.PodAnnotations = map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/port":   "4242",
	}

	deploy, err := DeploymentForUnleash(u, scheme.Scheme)
	if err != nil {
		t.Error("unexpected error", err)
	}

	assert.Equal(t, deploy.Spec.Template.Labels["app"], "unleash")
	assert.Equal(t, deploy.Spec.Template.Labels["env"], "dev")
	assert.Equal(t, deploy.Spec.Template.Annotations["prometheus.io/scrape"], "true")
	assert.Equal(t, deploy.Spec.Template.Annotations["prometheus.io/port"], "4242")
}

func TestNetworkPolicyForUnleash(t *testing.T) {
	var err error
	var np *networkingv1.NetworkPolicy
	var u *unleashv1.Unleash

	err = unleashv1.AddToScheme(scheme.Scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	u = &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
		},
	}
	np, err = NetworkPolicyForUnleash(u, scheme.Scheme, "some-namespace")
	if err != nil {
		t.Error("unexpected error", err)
	}

	if np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "some-namespace" {
		t.Error("expected namespace selector to be set, got", np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
}

func TestIsCanaryInstance(t *testing.T) {
	releaseChannel := &unleashv1.ReleaseChannel{
		Spec: unleashv1.ReleaseChannelSpec{
			Strategy: unleashv1.ReleaseChannelStrategy{
				Canary: unleashv1.ReleaseChannelCanary{
					Enabled: true,
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"canary": "true",
						},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "env",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"staging"},
							},
						},
					},
				},
			},
		},
	}

	t.Run("should return true for instance with matching labels and expressions", func(t *testing.T) {
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"canary": "true",
					"env":    "staging",
				},
			},
		}
		assert.True(t, isCanaryInstance(unleash, releaseChannel))
	})

	t.Run("should return false for instance with non-matching labels", func(t *testing.T) {
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"canary": "false",
					"env":    "staging",
				},
			},
		}
		assert.False(t, isCanaryInstance(unleash, releaseChannel))
	})

	t.Run("should return false for instance with non-matching expression", func(t *testing.T) {
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"canary": "true",
					"env":    "production",
				},
			},
		}
		assert.False(t, isCanaryInstance(unleash, releaseChannel))
	})

	t.Run("should return false when canary is disabled", func(t *testing.T) {
		disabledReleaseChannel := releaseChannel.DeepCopy()
		disabledReleaseChannel.Spec.Strategy.Canary.Enabled = false
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"canary": "true",
					"env":    "staging",
				},
			},
		}
		assert.False(t, isCanaryInstance(unleash, disabledReleaseChannel))
	})

	t.Run("should return false for instance with missing labels", func(t *testing.T) {
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"env": "staging",
				},
			},
		}
		assert.False(t, isCanaryInstance(unleash, releaseChannel))
	})

	t.Run("should return false for instance with no labels", func(t *testing.T) {
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{},
		}
		assert.False(t, isCanaryInstance(unleash, releaseChannel))
	})
}

func TestGetImageForInstance(t *testing.T) {
	unleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{Name: "test-unleash"},
	}

	canaryUnleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-unleash-canary",
			Labels: map[string]string{"canary": "true"},
		},
	}

	releaseChannel := &unleashv1.ReleaseChannel{
		Spec: unleashv1.ReleaseChannelSpec{
			Image: "new-image:v2",
			Strategy: unleashv1.ReleaseChannelStrategy{
				Canary: unleashv1.ReleaseChannelCanary{
					Enabled: true,
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{"canary": "true"},
					},
				},
			},
		},
		Status: unleashv1.ReleaseChannelStatus{
			PreviousImage: "old-image:v1",
		},
	}

	tests := []struct {
		name             string
		unleash          *unleashv1.Unleash
		phase            unleashv1.ReleaseChannelPhase
		expectedImage    string
		previousImageSet bool
	}{
		{"IdlePhase", unleash, unleashv1.ReleaseChannelPhaseIdle, "new-image:v2", true},
		{"IdlePhaseNoPrevious", unleash, unleashv1.ReleaseChannelPhaseIdle, "new-image:v2", false},
		{"RollingBackPhase", unleash, unleashv1.ReleaseChannelPhaseRollingBack, "old-image:v1", true},
		{"CanaryPhase_CanaryInstance", canaryUnleash, unleashv1.ReleaseChannelPhaseCanary, "new-image:v2", true},
		{"CanaryPhase_NonCanaryInstance", unleash, unleashv1.ReleaseChannelPhaseCanary, "old-image:v1", true},
		{"RollingPhase", unleash, unleashv1.ReleaseChannelPhaseRolling, "new-image:v2", true},
		{"UnknownPhase", unleash, "Unknown", "new-image:v2", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := releaseChannel.DeepCopy()
			rc.Status.Phase = tt.phase
			if !tt.previousImageSet {
				rc.Status.PreviousImage = ""
			}

			image := getImageForInstance(tt.unleash, rc)
			if image != tt.expectedImage {
				t.Errorf("getImageForInstance() = %v, want %v", image, tt.expectedImage)
			}
		})
	}
}

func TestEnvVarsForUnleash(t *testing.T) {
	unleashBase := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{Name: "test-unleash"},
	}

	tests := []struct {
		name          string
		unleash       *unleashv1.Unleash
		expectedVars  map[string]string
		expectErr     bool
		expectedErr   string
		checkSecretFn func(t *testing.T, envVars []corev1.EnvVar)
	}{
		{
			name: "Database URL directly",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{Database: unleashv1.UnleashDatabaseConfig{URL: "postgres://user:pass@host/db"}},
			},
			expectedVars: map[string]string{"DATABASE_URL": "postgres://user:pass@host/db"},
		},
		{
			name: "Database URL from secret",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{Database: unleashv1.UnleashDatabaseConfig{SecretName: "db-secret", SecretURLKey: "db-url"}},
			},
			checkSecretFn: func(t *testing.T, envVars []corev1.EnvVar) {
				assert.Equal(t, "DATABASE_URL", envVars[1].Name)
				assert.Equal(t, "db-secret", envVars[1].ValueFrom.SecretKeyRef.Name)
				assert.Equal(t, "db-url", envVars[1].ValueFrom.SecretKeyRef.Key)
			},
		},
		{
			name: "Database credentials from secret",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{Database: unleashv1.UnleashDatabaseConfig{
					SecretName:            "db-creds",
					SecretUserKey:         "user",
					SecretPassKey:         "pass",
					SecretHostKey:         "host",
					SecretPortKey:         "port",
					SecretDatabaseNameKey: "dbname",
					SecretSSLKey:          "ssl",
				}},
			},
			checkSecretFn: func(t *testing.T, envVars []corev1.EnvVar) {
				expectedSecrets := map[string]string{
					"DATABASE_USER": "user",
					"DATABASE_PASS": "pass",
					"DATABASE_HOST": "host",
					"DATABASE_PORT": "port",
					"DATABASE_NAME": "dbname",
					"DATABASE_SSL":  "ssl",
				}

				foundSecrets := make(map[string]string)
				for _, envVar := range envVars {
					if envVar.ValueFrom != nil && envVar.ValueFrom.SecretKeyRef != nil {
						if _, ok := expectedSecrets[envVar.Name]; ok {
							assert.Equal(t, "db-creds", envVar.ValueFrom.SecretKeyRef.Name)
							foundSecrets[envVar.Name] = envVar.ValueFrom.SecretKeyRef.Key
						}
					}
				}

				assert.Equal(t, len(expectedSecrets), len(foundSecrets), "mismatched number of secret keys found")
				for k, v := range expectedSecrets {
					assert.Equal(t, v, foundSecrets[k], "mismatched secret key for %s", k)
				}
			},
		},
		{
			name:      "No database config",
			unleash:   unleashBase.DeepCopy(),
			expectErr: true,
		},
		{
			name: "Missing user",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{Database: unleashv1.UnleashDatabaseConfig{SecretName: "db-creds"}},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Prepend the admin token secret which is always present
			if tt.unleash.Name == "" {
				tt.unleash.Name = "test-unleash"
			}
			expectedAdminSecret := corev1.EnvVar{
				Name: EnvInitAdminAPIToken,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: tt.unleash.GetInstanceSecretName()},
						Key:                  unleashv1.UnleashSecretTokenKey,
					},
				},
			}

			vars, err := envVarsForUnleash(tt.unleash)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.expectedErr != "" {
					assert.Contains(t, err.Error(), tt.expectedErr)
				}
				return
			}

			assert.NoError(t, err)
			assert.NotEmpty(t, vars)
			assert.Equal(t, expectedAdminSecret, vars[0])

			if tt.expectedVars != nil {
				for _, v := range vars {
					if val, ok := tt.expectedVars[v.Name]; ok {
						assert.Equal(t, val, v.Value)
						delete(tt.expectedVars, v.Name)
					}
				}
				assert.Empty(t, tt.expectedVars, "Not all expected env vars were found")
			}

			if tt.checkSecretFn != nil {
				tt.checkSecretFn(t, vars)
			}
		})
	}
}

func TestResolveReleaseChannelImage(t *testing.T) {
	// Setup scheme with our custom types
	testScheme := runtime.NewScheme()
	err := unleashv1.AddToScheme(testScheme)
	assert.NoError(t, err)

	t.Run("should return default image when no ReleaseChannel specified", func(t *testing.T) {
		unleashWithoutRC := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-unleash-no-rc",
				Namespace: "test-namespace",
			},
			Spec: unleashv1.UnleashSpec{
				// No ReleaseChannel specified
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		image, modified, err := ResolveReleaseChannelImage(context.Background(), k8sClient, unleashWithoutRC)
		assert.NoError(t, err)
		assert.False(t, modified)               // No status to update
		assert.Contains(t, image, "unleash-v4") // Should be the default image
	})

	t.Run("should return error when ReleaseChannel specified (should be handled by controller)", func(t *testing.T) {
		unleashWithRC := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-unleash",
				Namespace: "test-namespace",
			},
			Spec: unleashv1.UnleashSpec{
				ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
					Name: "test-channel",
				},
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		image, modified, err := ResolveReleaseChannelImage(context.Background(), k8sClient, unleashWithRC)
		assert.Error(t, err)
		assert.False(t, modified)
		assert.Empty(t, image)
		assert.Contains(t, err.Error(), "ReleaseChannel specified but no resolved image set by ReleaseChannel controller")
	})

	t.Run("should prioritize CustomImage over ReleaseChannel and clear status", func(t *testing.T) {
		unleashWithCustom := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-unleash",
				Namespace: "test-namespace",
			},
			Spec: unleashv1.UnleashSpec{
				ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
					Name: "test-channel",
				},
				CustomImage: "custom-image:latest",
			},
			Status: unleashv1.UnleashStatus{
				// Set some existing ReleaseChannel status that should be cleared
				ResolvedReleaseChannelImage: "old-image:v1.0",
				ReleaseChannelName:          "old-channel",
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		image, modified, err := ResolveReleaseChannelImage(context.Background(), k8sClient, unleashWithCustom)
		assert.NoError(t, err)
		assert.True(t, modified) // Status should be cleared when CustomImage is set
		assert.Equal(t, "custom-image:latest", image)
		assert.Empty(t, unleashWithCustom.Status.ResolvedReleaseChannelImage)
		assert.Empty(t, unleashWithCustom.Status.ReleaseChannelName)
	})

	t.Run("should return CustomImage without modifying status when no previous ReleaseChannel status", func(t *testing.T) {
		unleashWithCustom := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-unleash",
				Namespace: "test-namespace",
			},
			Spec: unleashv1.UnleashSpec{
				CustomImage: "custom-image:latest",
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		image, modified, err := ResolveReleaseChannelImage(context.Background(), k8sClient, unleashWithCustom)
		assert.NoError(t, err)
		assert.False(t, modified) // No status changes needed
		assert.Equal(t, "custom-image:latest", image)
	})

	t.Run("should clear ReleaseChannel status when switching from ReleaseChannel to no ReleaseChannel", func(t *testing.T) {
		unleashSwitchingFromRC := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-unleash",
				Namespace: "test-namespace",
			},
			Spec: unleashv1.UnleashSpec{
				// No ReleaseChannel specified anymore
			},
			Status: unleashv1.UnleashStatus{
				// But still has old ReleaseChannel status that should be cleared
				ResolvedReleaseChannelImage: "old-image:v1.0",
				ReleaseChannelName:          "old-channel",
			},
		}

		k8sClient := fake.NewClientBuilder().WithScheme(testScheme).Build()

		image, modified, err := ResolveReleaseChannelImage(context.Background(), k8sClient, unleashSwitchingFromRC)
		assert.NoError(t, err)
		assert.True(t, modified)                // Status should be cleared
		assert.Contains(t, image, "unleash-v4") // Should be the default image
		assert.Empty(t, unleashSwitchingFromRC.Status.ResolvedReleaseChannelImage)
		assert.Empty(t, unleashSwitchingFromRC.Status.ReleaseChannelName)
	})
}
