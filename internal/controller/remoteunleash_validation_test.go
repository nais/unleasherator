package controller

import (
	"context"
	"errors"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestValidatedRemoteUnleashAdminTokenRejectsURLSubstitution(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	assert.NoError(t, unleashv1.AddToScheme(scheme))

	const (
		operatorNamespace = "unleasherator-system"
		tenantNamespace   = "tenant"
		authorizedURL     = "https://authorized.example.com"
	)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "admin-secret",
			Namespace: operatorNamespace,
			Annotations: map[string]string{
				unleashv1.UnleashSecretAuthorizedNamespaceAnnotation: tenantNamespace,
			},
		},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey:     []byte("admin-token"),
			unleashv1.UnleashSecretServerURLKey: []byte(authorizedURL),
		},
	}
	remoteUnleash := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "remote", Namespace: tenantNamespace},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{URL: "https://attacker.example.com"},
			AdminSecret: unleashv1.RemoteUnleashSecret{
				Name:      secret.Name,
				Namespace: secret.Namespace,
				Key:       unleashv1.UnleashSecretTokenKey,
			},
		},
	}
	k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

	_, err := validatedRemoteUnleashAdminToken(
		context.Background(),
		k8sClient,
		remoteUnleash,
		operatorNamespace,
		false,
	)
	assert.ErrorIs(t, err, errRemoteUnleashServerURL)

	remoteUnleash.Spec.Server.URL = authorizedURL
	token, err := validatedRemoteUnleashAdminToken(
		context.Background(),
		k8sClient,
		remoteUnleash,
		operatorNamespace,
		false,
	)
	assert.NoError(t, err)
	assert.Equal(t, []byte("admin-token"), token)

	secret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation] = "other-tenant"
	assert.NoError(t, k8sClient.Update(context.Background(), secret))
	_, err = validatedRemoteUnleashAdminToken(
		context.Background(),
		k8sClient,
		remoteUnleash,
		operatorNamespace,
		false,
	)
	assert.True(t, errors.Is(err, errRemoteUnleashAuthorization))
}
