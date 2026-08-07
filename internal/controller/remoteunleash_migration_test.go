package controller

import (
	"context"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/federation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	migrationOperatorNamespace = "nais-system"
	migrationTenantNamespace   = "tenant-a"
	migrationToken             = "admin-token-value"
	migrationURL               = "https://unleash.example.com"
)

func migrationTestSetup(t *testing.T, objects ...client.Object) *RemoteUnleashReconciler {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &RemoteUnleashReconciler{
		Client:            fakeClient,
		APIReader:         fakeClient,
		Scheme:            scheme,
		OperatorNamespace: migrationOperatorNamespace,
	}
}

func legacyRemoteUnleash() *unleashv1.RemoteUnleash {
	return &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "test-unleash", Namespace: migrationTenantNamespace},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{URL: migrationURL},
			AdminSecret: unleashv1.RemoteUnleashSecret{
				Name: "unleasherator-test-unleash-abc123",
				Key:  unleashv1.UnleashSecretTokenKey,
			},
		},
	}
}

func legacySecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleasherator-test-unleash-abc123",
			Namespace: migrationTenantNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/instance":   "test-unleash",
				"app.kubernetes.io/part-of":    "unleasherator",
				"app.kubernetes.io/created-by": "controller-manager",
			},
		},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey:     []byte(migrationToken),
			unleashv1.UnleashSecretServerURLKey: []byte(migrationURL),
		},
	}
}

func namespaceBoundSecretKey(t *testing.T) types.NamespacedName {
	t.Helper()
	nonce, err := federation.StableSecretNonce("test-unleash", migrationURL, migrationToken)
	require.NoError(t, err)
	return types.NamespacedName{
		Name:      "unleasherator-test-unleash-" + migrationTenantNamespace + "-admin-key-" + nonce,
		Namespace: migrationOperatorNamespace,
	}
}

func TestMigrateLegacyAdminSecretMigratesTenantSecret(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.True(t, migrated)

	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	assert.Equal(t, namespaceBoundSecretKey(t), updated.AdminSecretNamespacedName())

	migratedSecret := &corev1.Secret{}
	require.NoError(t, reconciler.Get(context.Background(), namespaceBoundSecretKey(t), migratedSecret))
	assert.Equal(t, migrationTenantNamespace,
		migratedSecret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation])
	assert.Equal(t, []byte(migrationToken), migratedSecret.Data[unleashv1.UnleashSecretTokenKey])
	assert.Equal(t, []byte(migrationURL), migratedSecret.Data[unleashv1.UnleashSecretServerURLKey])

	legacyKey := types.NamespacedName{Name: "unleasherator-test-unleash-abc123", Namespace: migrationTenantNamespace}
	err = reconciler.Get(context.Background(), legacyKey, &corev1.Secret{})
	assert.True(t, apierrors.IsNotFound(err), "legacy secret should be deleted after migration")
}

func TestMigrateLegacyAdminSecretPreservesSharedSecret(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()

	other := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "other-unleash", Namespace: "tenant-b"},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{URL: migrationURL},
			AdminSecret: unleashv1.RemoteUnleashSecret{
				Name:      secret.Name,
				Namespace: migrationTenantNamespace,
				Key:       unleashv1.UnleashSecretTokenKey,
			},
		},
	}
	reconciler := migrationTestSetup(t, remoteUnleash, secret, other)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.False(t, migrated, "shared legacy secrets are refused before any mutation")

	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	assert.Equal(t, "unleasherator-test-unleash-abc123", updated.Spec.AdminSecret.Name,
		"reference must stay on the shared legacy secret")

	legacyKey := types.NamespacedName{Name: secret.Name, Namespace: migrationTenantNamespace}
	require.NoError(t, reconciler.Get(context.Background(), legacyKey, &corev1.Secret{}),
		"shared legacy secret must be preserved")

	require.True(t, apierrors.IsNotFound(reconciler.Get(context.Background(), namespaceBoundSecretKey(t), &corev1.Secret{})),
		"no replacement grant may be minted for a shared secret")
}

func TestMigrateLegacyAdminSecretNoopWhenNamespaceBound(t *testing.T) {
	boundKey := namespaceBoundSecretKey(t)
	remoteUnleash := legacyRemoteUnleash()
	remoteUnleash.Spec.AdminSecret.Name = boundKey.Name
	remoteUnleash.Spec.AdminSecret.Namespace = migrationOperatorNamespace

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      boundKey.Name,
			Namespace: migrationOperatorNamespace,
			Annotations: map[string]string{
				unleashv1.UnleashSecretAuthorizedNamespaceAnnotation: migrationTenantNamespace,
			},
		},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey:     []byte(migrationToken),
			unleashv1.UnleashSecretServerURLKey: []byte(migrationURL),
		},
	}
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.False(t, migrated)
}

func TestMigrateLegacyAdminSecretMigratesNameBoundOperatorSecret(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	remoteUnleash.Spec.AdminSecret.Namespace = migrationOperatorNamespace

	secret := legacySecret()
	secret.Namespace = migrationOperatorNamespace
	// Cross-namespace legacy secrets keep their publisher-asserted URL.
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.True(t, migrated, "name-bound operator secret without annotation is legacy and must migrate")

	require.NoError(t, reconciler.Get(context.Background(), namespaceBoundSecretKey(t), &corev1.Secret{}))
}

func TestMigrateLegacyAdminSecretRefusesCrossNamespaceUnassertedURL(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	remoteUnleash.Spec.AdminSecret.Namespace = migrationOperatorNamespace

	secret := legacySecret()
	secret.Namespace = migrationOperatorNamespace
	delete(secret.Data, unleashv1.UnleashSecretServerURLKey)
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.False(t, migrated, "cross-namespace url-less secrets assert nothing about the URL")

	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	assert.Equal(t, "unleasherator-test-unleash-abc123", updated.Spec.AdminSecret.Name)
	assert.True(t, apierrors.IsNotFound(reconciler.Get(context.Background(), namespaceBoundSecretKey(t), &corev1.Secret{})),
		"no grant may be minted from a url-less cross-namespace secret")
}

func TestMigrateLegacyAdminSecretDoesNotMutateSharedSecret(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	delete(secret.Data, unleashv1.UnleashSecretServerURLKey) // url-less: stamp must not fire

	other := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "other-unleash", Namespace: "tenant-b"},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{URL: migrationURL},
			AdminSecret: unleashv1.RemoteUnleashSecret{
				Name:      secret.Name,
				Namespace: migrationTenantNamespace,
				Key:       unleashv1.UnleashSecretTokenKey,
			},
		},
	}
	reconciler := migrationTestSetup(t, remoteUnleash, secret, other)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.False(t, migrated)

	fresh := &corev1.Secret{}
	legacyKey := types.NamespacedName{Name: secret.Name, Namespace: migrationTenantNamespace}
	require.NoError(t, reconciler.Get(context.Background(), legacyKey, fresh))
	assert.Empty(t, fresh.Data[unleashv1.UnleashSecretServerURLKey],
		"refused shared secrets must not be mutated")
}

func TestMigrateLegacyAdminSecretRejectsConflictingSecret(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()

	boundKey := namespaceBoundSecretKey(t)
	conflicting := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      boundKey.Name,
			Namespace: migrationOperatorNamespace,
			Annotations: map[string]string{
				unleashv1.UnleashSecretAuthorizedNamespaceAnnotation: "other-tenant",
			},
		},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey: []byte("other-token"),
		},
	}
	reconciler := migrationTestSetup(t, remoteUnleash, secret, conflicting)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.Error(t, err)
	assert.False(t, migrated)
	assert.Contains(t, err.Error(), "conflicting content")

	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	assert.Equal(t, "unleasherator-test-unleash-abc123", updated.Spec.AdminSecret.Name,
		"reference must not change when the replacement secret conflicts")
}

func TestMigrateLegacyAdminSecretRequiresTokenKey(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	secret.Data = map[string][]byte{}
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.Error(t, err)
	assert.False(t, migrated)
	assert.Contains(t, err.Error(), "missing key")
}

func TestMigrateLegacyAdminSecretSkipsTenantOwnedSecret(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	secret.Labels = nil // tenant-authored secret without operator labels
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.False(t, migrated, "tenant-owned secrets must never be migrated or deleted")

	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	assert.Equal(t, "unleasherator-test-unleash-abc123", updated.Spec.AdminSecret.Name)

	legacyKey := types.NamespacedName{Name: secret.Name, Namespace: migrationTenantNamespace}
	require.NoError(t, reconciler.Get(context.Background(), legacyKey, &corev1.Secret{}),
		"tenant-owned secret must be preserved")
}

func TestMigrateLegacyAdminSecretAbortsOnConcurrentSpecChange(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	// Simulate a concurrent federation rotation repointing the resource before
	// the migration's spec update lands.
	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	updated.Spec.AdminSecret.Name = "unleasherator-test-unleash-tenant-a-admin-key-rotated"
	updated.Spec.AdminSecret.Namespace = migrationOperatorNamespace
	require.NoError(t, reconciler.Update(context.Background(), updated))

	// Reset the in-memory object to the pre-rotation reference, as the
	// reconciler would have read it before the rotation landed.
	incoming := legacyRemoteUnleash()
	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), incoming, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.False(t, migrated, "migration must abort when the reference changed concurrently")

	current := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), current))
	assert.Equal(t, "unleasherator-test-unleash-tenant-a-admin-key-rotated", current.Spec.AdminSecret.Name,
		"concurrent repoint must not be overwritten by a stale migration")
}

func TestMigrateLegacyAdminSecretRefusesURLDrift(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	secret.Data[unleashv1.UnleashSecretServerURLKey] = []byte("https://attacker.example.com")
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.Error(t, err, "a recorded URL that mismatches the spec is genuine drift and must fail")
	assert.False(t, migrated)
	assert.Contains(t, err.Error(), "does not match")
}

func TestMigrateLegacyAdminSecretStampsAbsentURLAndMigrates(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	delete(secret.Data, unleashv1.UnleashSecretServerURLKey)
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	// The credential was verified against the spec URL by the stats check
	// earlier in the reconcile, so filling the absent url key is a faithful
	// statement and the migration proceeds.
	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.True(t, migrated, "absent url is stamped from the verified spec URL, then migrated")

	updated := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), updated))
	assert.Equal(t, namespaceBoundSecretKey(t), updated.AdminSecretNamespacedName())
}

func TestMigrateLegacyAdminSecretIsIdempotentOnRetry(t *testing.T) {
	remoteUnleash := legacyRemoteUnleash()
	secret := legacySecret()
	reconciler := migrationTestSetup(t, remoteUnleash, secret)

	// Simulate a crash after the replacement was created but before repoint:
	// the namespace-bound secret already exists with identical content.
	boundKey := namespaceBoundSecretKey(t)
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      boundKey.Name,
			Namespace: migrationOperatorNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/instance":   "test-unleash",
				"app.kubernetes.io/part-of":    "unleasherator",
				"app.kubernetes.io/created-by": "controller-manager",
			},
			Annotations: map[string]string{
				unleashv1.UnleashSecretAuthorizedNamespaceAnnotation: migrationTenantNamespace,
			},
		},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey:     []byte(migrationToken),
			unleashv1.UnleashSecretServerURLKey: []byte(migrationURL),
		},
	}
	require.NoError(t, reconciler.Create(context.Background(), existing))

	migrated, err := reconciler.migrateLegacyAdminSecret(context.Background(), remoteUnleash, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.True(t, migrated, "retry with an existing identical replacement must converge")

	final := &unleashv1.RemoteUnleash{}
	require.NoError(t, reconciler.Get(context.Background(), remoteUnleash.NamespacedName(), final))
	assert.Equal(t, boundKey, final.AdminSecretNamespacedName())
}
