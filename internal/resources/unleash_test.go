package resources

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
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

func TestImageForUnleash(t *testing.T) {
	const (
		customImage   = "my-custom-image:latest"
		resolvedImage = "resolved-channel-image:v1"
		envImage      = "env-var-image:v2"
		defaultImage  = "europe-north1-docker.pkg.dev/nais-io/nais/images/unleash-v4:v4.23.1"
	)

	unleashBase := &unleashv1.Unleash{
		Spec: unleashv1.UnleashSpec{
			ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
				Name: "stable",
			},
		},
	}

	tests := []struct {
		name          string
		unleash       *unleashv1.Unleash
		envVarSet     bool
		expectedImage string
	}{
		{
			name: "CustomImage priority",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{CustomImage: customImage},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: resolvedImage,
					ReleaseChannelName:          "stable",
				},
			},
			envVarSet:     true,
			expectedImage: customImage,
		},
		{
			name: "ResolvedReleaseChannelImage priority",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{Name: "stable"},
				},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: resolvedImage,
					ReleaseChannelName:          "stable",
				},
			},
			envVarSet:     true,
			expectedImage: resolvedImage,
		},
		{
			name: "ResolvedReleaseChannelImage ignored for different channel",
			unleash: &unleashv1.Unleash{
				Spec: unleashv1.UnleashSpec{
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{Name: "beta"},
				},
				Status: unleashv1.UnleashStatus{
					ResolvedReleaseChannelImage: resolvedImage,
					ReleaseChannelName:          "stable",
				},
			},
			envVarSet:     true,
			expectedImage: envImage,
		},
		{
			name:          "Environment variable priority",
			unleash:       unleashBase.DeepCopy(),
			envVarSet:     true,
			expectedImage: envImage,
		},
		{
			name:          "Default image priority",
			unleash:       unleashBase.DeepCopy(),
			envVarSet:     false,
			expectedImage: defaultImage,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVarSet {
				t.Setenv("UNLEASH_IMAGE", envImage)
			} else {
				t.Setenv("UNLEASH_IMAGE", "")
			}

			image := ImageForUnleash(tt.unleash)
			if image != tt.expectedImage {
				t.Errorf("ImageForUnleash() = %v, want %v", image, tt.expectedImage)
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
					"DATABASE_USER":     "user",
					"DATABASE_PASS":     "pass",
					"DATABASE_HOST":     "host",
					"DATABASE_PORT":     "port",
					"DATABASE_NAME":     "dbname",
					"DATABASE_SSL":      "ssl",
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
