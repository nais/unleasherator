package resources

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestDeploymentForUnleash(t *testing.T) {
	unleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Database: unleashv1.UnleashDatabaseConfig{
				URL: "postgres://user:pass@host:port/dbname",
			},
		},
	}

	scheme := runtime.NewScheme()
	err := unleashv1.AddToScheme(scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	port := unleashv1.UnleashPort(8080)
	image := unleashv1.UnleashImage("unleash:latest")

	dep, err := DeploymentForUnleash(unleash, scheme, port, image)
	if err != nil {
		t.Error("unexpected error", err)
	}

	assert.Equal(t, "unleash", dep.Name)
	assert.Equal(t, "unleash", dep.Namespace)
	assert.Equal(t, int32(1), *dep.Spec.Replicas)
	assert.Equal(t, "unleash:latest", dep.Spec.Template.Spec.Containers[0].Image)
	assert.Equal(t, int32(8080), dep.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
}

func TestNetworkPolicyForUnleash(t *testing.T) {
	tests := []struct {
		name               string
		namespace          string
		expectedMatchLabel string
	}{
		{
			name:               "Namespace selector set",
			namespace:          "some-namespace",
			expectedMatchLabel: "some-namespace",
		},
		// Add more test cases here
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

			np, err = NetworkPolicyForUnleash(u, scheme.Scheme, tt.namespace)
			if err != nil {
				t.Error("unexpected error", err)
			}

			assert.Equal(t, tt.expectedMatchLabel, np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
		})
	}
}

func TestEnvVarsForUnleash(t *testing.T) {
	tests := []struct {
		name            string
		unleash         *unleashv1.Unleash
		expectedEnvVars []corev1.EnvVar
		expectedError   bool
	}{
		{
			name: "No database configured",
			unleash: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleash",
					Namespace: "unleash",
				},
				Spec: unleashv1.UnleashSpec{},
			},
			expectedError: true,
		},
		{
			name: "Database URL configured",
			unleash: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleash",
					Namespace: "unleash",
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://user:pass@host:port/dbname",
					},
				},
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name: "INIT_ADMIN_API_TOKENS",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleasherator-unleash-admin-key",
							},
							Key: "token",
						},
					},
				},
				{
					Name:  "DATABASE_URL",
					Value: "postgres://user:pass@host:port/dbname",
				},
			},
			expectedError: false,
		},
		{
			name: "Database URL from secret",
			unleash: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleash",
					Namespace: "unleash",
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						SecretName:   "unleash-db",
						SecretURLKey: "url",
					},
				},
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name: "INIT_ADMIN_API_TOKENS",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleasherator-unleash-admin-key",
							},
							Key: "token",
						},
					},
				},
				{
					Name: "DATABASE_URL",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "url",
						},
					},
				},
			},
			expectedError: false,
		},
		{
			name: "Database parameters from secret",
			unleash: &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleash",
					Namespace: "unleash",
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						SecretName:            "unleash-db",
						SecretUserKey:         "user",
						SecretPassKey:         "pass",
						SecretPortKey:         "port",
						SecretHostKey:         "host",
						SecretDatabaseNameKey: "dbname",
						SecretSSLKey:          "ssl",
					},
				},
			},
			expectedEnvVars: []corev1.EnvVar{
				{
					Name: "INIT_ADMIN_API_TOKENS",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleasherator-unleash-admin-key",
							},
							Key: "token",
						},
					},
				},
				{
					Name: "DATABASE_PASS",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "pass",
						},
					},
				},
				{
					Name: "DATABASE_USER",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "user",
						},
					},
				},
				{
					Name: "DATABASE_NAME",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "dbname",
						},
					},
				},
				{
					Name: "DATABASE_HOST",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "host",
						},
					},
				},
				{
					Name: "DATABASE_PORT",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "port",
						},
					},
				},
				{
					Name: "DATABASE_SSL",
					ValueFrom: &corev1.EnvVarSource{
						SecretKeyRef: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: "unleash-db",
							},
							Key: "ssl",
						},
					},
				},
				{
					Name:  "DATABASE_URL",
					Value: "postgres://$(DATABASE_USER):$(DATABASE_PASS)@$(DATABASE_HOST):$(DATABASE_PORT)/$(DATABASE_NAME)",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envVars, err := envVarsForUnleash(tt.unleash)
			if tt.expectedError {
				assert.Error(t, err, "expected error when no database is configured")
			} else {
				assert.NoError(t, err, "unexpected error")
				assert.Equal(t, tt.expectedEnvVars, envVars, "incorrect environment variables")
			}
		})
	}
}
