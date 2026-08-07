package controller

import (
	"context"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestRefreshFederationSecretURLsStampsLegacySecrets(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	labels := map[string]string{
		"app.kubernetes.io/instance":   "my-unleash",
		"app.kubernetes.io/part-of":    "unleasherator",
		"app.kubernetes.io/created-by": "controller-manager",
	}

	legacy := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unleasherator-my-unleash-abc123", Namespace: "tenant-a", Labels: labels},
		Data:       map[string][]byte{unleashv1.UnleashSecretTokenKey: []byte("the-token")},
	}
	alreadyStamped := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unleasherator-my-unleash-tenant-a-admin-key-def456", Namespace: "nais-system", Labels: labels},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey:     []byte("the-token"),
			unleashv1.UnleashSecretServerURLKey: []byte("https://unleash.example.com"),
		},
	}
	otherToken := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unleasherator-my-unleash-old789", Namespace: "tenant-b", Labels: labels},
		Data:       map[string][]byte{unleashv1.UnleashSecretTokenKey: []byte("other-token")},
	}
	unmanaged := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unleasherator-my-unleash-manual", Namespace: "tenant-c"},
		Data:       map[string][]byte{unleashv1.UnleashSecretTokenKey: []byte("the-token")},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(legacy, alreadyStamped, otherToken, unmanaged).
		Build()
	reconciler := &UnleashReconciler{Client: fakeClient, Scheme: scheme}

	unleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{Name: "my-unleash", Namespace: "bifrost"},
		Spec: unleashv1.UnleashSpec{
			ApiIngress: unleashv1.UnleashIngressConfig{Host: "unleash.example.com"},
			Federation: unleashv1.UnleashFederationConfig{Enabled: true, Namespaces: []string{"tenant-a"}},
		},
	}

	err := reconciler.refreshFederationSecretURLs(context.Background(), unleash, "the-token", ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := &corev1.Secret{}
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(legacy), updated))
	assert.Equal(t, []byte("https://unleash.example.com"), updated.Data[unleashv1.UnleashSecretServerURLKey],
		"legacy secret must gain the url key")
	assert.Equal(t, []byte("the-token"), updated.Data[unleashv1.UnleashSecretTokenKey],
		"token must be preserved")

	// Idempotent: second run makes no changes.
	require.NoError(t, reconciler.refreshFederationSecretURLs(context.Background(), unleash, "the-token", ctrl.Log.WithName("test")))
	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(alreadyStamped), updated))
	assert.Equal(t, []byte("https://unleash.example.com"), updated.Data[unleashv1.UnleashSecretServerURLKey])

	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(otherToken), updated))
	assert.Empty(t, updated.Data[unleashv1.UnleashSecretServerURLKey],
		"secrets with a different token must not be touched")

	require.NoError(t, fakeClient.Get(context.Background(), client.ObjectKeyFromObject(unmanaged), updated))
	assert.Empty(t, updated.Data[unleashv1.UnleashSecretServerURLKey],
		"secrets without operator labels must not be touched")
}

func TestRefreshFederationSecretURLsNoopWithoutURL(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &UnleashReconciler{Client: fakeClient, Scheme: scheme}

	unleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{Name: "my-unleash", Namespace: "bifrost"},
		Spec: unleashv1.UnleashSpec{
			Federation: unleashv1.UnleashFederationConfig{Enabled: true, Namespaces: []string{"tenant-a"}},
		},
	}

	// No ingress configured → PublicApiURL is empty → nothing to stamp.
	require.NoError(t, reconciler.refreshFederationSecretURLs(context.Background(), unleash, "token", ctrl.Log.WithName("test")))
}
