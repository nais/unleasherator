package controller

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/federation"
	"github.com/nais/unleasherator/internal/resources"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var federationSecretMigrations = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "unleasherator_federation_secret_migrations_total",
		Help: "Number of RemoteUnleash admin secrets migrated from legacy to namespace-bound layout",
	},
	[]string{"result"},
)

// errMigrationSuperseded aborts a repoint when the RemoteUnleash spec changed
// while the migration was in flight (e.g. a concurrent federation rotation).
var errMigrationSuperseded = errors.New("migration superseded by spec change")

func init() {
	metrics.Registry.MustRegister(federationSecretMigrations)
}

// migrateLegacyAdminSecret moves a legacy (tenant-namespace or name-bound)
// admin secret reference to the namespace-bound layout: the secret lives in
// the operator namespace and carries the authoritative authorized-namespace
// annotation. The credential is copied verbatim; it was already verified
// against the Unleash server earlier in the reconcile.
//
// Ordering is deliberate: create the replacement first, repoint the
// RemoteUnleash, then delete the legacy secret only when no other RemoteUnleash
// still references it.
func (r *RemoteUnleashReconciler) migrateLegacyAdminSecret(ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash, log logr.Logger) (bool, error) {
	legacyKey := remoteUnleash.AdminSecretNamespacedName()

	// Read through the API directly: the migration must not act on stale
	// cached state after a restart or cache lag.
	legacySecret := &corev1.Secret{}
	if err := r.APIReader.Get(ctx, legacyKey, legacySecret); err != nil {
		return false, fmt.Errorf("getting legacy admin secret: %w", err)
	}

	// Already namespace-bound: operator namespace secret carrying the
	// authoritative annotation for this tenant namespace.
	if legacyKey.Namespace == r.OperatorNamespace &&
		legacySecret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation] == remoteUnleash.Namespace {
		return false, nil
	}

	// Only ever touch operator-managed federation secrets. A tenant-authored
	// same-namespace secret does not cross a privilege boundary and must never
	// be rewritten or deleted by the operator.
	legacyLabels := legacySecret.Labels
	if legacyLabels["app.kubernetes.io/part-of"] != "unleasherator" ||
		legacyLabels["app.kubernetes.io/created-by"] != "controller-manager" ||
		legacyLabels["app.kubernetes.io/instance"] != remoteUnleash.Name ||
		!strings.HasPrefix(legacySecret.Name, unleashv1.UnleashSecretNamePrefix+"-") {
		log.V(1).Info("Admin secret is not operator-managed; skipping migration", "secret", legacyKey)
		return false, nil
	}

	token := legacySecret.Data[remoteUnleash.Spec.AdminSecret.Key]
	if len(token) == 0 {
		federationSecretMigrations.WithLabelValues("failed").Inc()
		return false, fmt.Errorf("legacy admin secret %s is missing key %q", legacyKey, remoteUnleash.Spec.AdminSecret.Key)
	}

	// Never derive the authoritative URL binding from tenant-controlled spec:
	// pre-#746 legacy secrets carry no URL, and a tenant could otherwise point
	// the spec at an attacker server and have the grant survive the legacy
	// enforcement flip.
	//
	// The credential was just verified against the Unleash server at this URL
	// (the stats call succeeded), so filling an absent url key is a faithful
	// statement of where the credential was proven to work — no republication
	// needed. A recorded URL that disagrees is genuine drift: fail loudly.
	recordedURL := string(legacySecret.Data[unleashv1.UnleashSecretServerURLKey])
	if recordedURL == "" {
		patch := client.MergeFrom(legacySecret.DeepCopy())
		if legacySecret.Data == nil {
			legacySecret.Data = map[string][]byte{}
		}
		legacySecret.Data[unleashv1.UnleashSecretServerURLKey] = []byte(remoteUnleash.Spec.Server.URL)
		if err := r.Patch(ctx, legacySecret, patch); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil // deleted concurrently; next reconcile re-evaluates
			}
			federationSecretMigrations.WithLabelValues("failed").Inc()
			return false, fmt.Errorf("stamping url onto legacy admin secret %s: %w", legacyKey, err)
		}
	} else if recordedURL != remoteUnleash.Spec.Server.URL {
		federationSecretMigrations.WithLabelValues("failed").Inc()
		return false, fmt.Errorf("legacy admin secret %s URL %q does not match spec.server.url %q", legacyKey, recordedURL, remoteUnleash.Spec.Server.URL)
	}

	// Refuse shared legacy secrets before minting anything: each referencing
	// RemoteUnleash would otherwise receive its own annotated grant, and the
	// canary tooling already treats shared secrets as manual-review cases.
	shared, err := federationAdminSecretReferencedByOtherRemoteUnleash(
		ctx, r.APIReader, legacyKey, client.ObjectKeyFromObject(remoteUnleash))
	if err != nil {
		return false, fmt.Errorf("checking legacy admin secret references: %w", err)
	}
	if shared {
		log.Info("Refusing to migrate shared legacy admin secret; migrate referencing resources manually", "secret", legacyKey)
		if r.Recorder != nil {
			r.Recorder.Event(remoteUnleash, "Warning", "FederationSecretMigrationRefused",
				"Legacy admin secret is shared with another RemoteUnleash; manual migration required")
		}
		federationSecretMigrations.WithLabelValues("refused").Inc()
		return false, nil
	}

	// Same derivation as the federation subscriber fallback, so a later
	// republication of an instance without an explicit secret nonce converges
	// on the same secret name.
	nonce, err := federation.StableSecretNonce(remoteUnleash.Name, remoteUnleash.Spec.Server.URL, string(token))
	if err != nil {
		return false, err
	}
	secretName := fmt.Sprintf("unleasherator-%s-%s-admin-key-%s", remoteUnleash.Name, remoteUnleash.Namespace, nonce)

	newSecret := resources.OperatorSecretForUnleash(remoteUnleash.Name, secretName, r.OperatorNamespace, string(token), remoteUnleash.Spec.Server.URL)
	if newSecret.Annotations == nil {
		newSecret.Annotations = map[string]string{}
	}
	newSecret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation] = remoteUnleash.Namespace
	// Write Data directly so retries and conflict checks read back the same
	// content; StringData is only converted by the API server on write.
	newSecret.Data = map[string][]byte{
		unleashv1.UnleashSecretTokenKey:     token,
		unleashv1.UnleashSecretServerURLKey: []byte(remoteUnleash.Spec.Server.URL),
	}
	newSecret.StringData = nil

	existing := &corev1.Secret{}
	err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(newSecret), existing)
	switch {
	case apierrors.IsNotFound(err):
		if err := r.Create(ctx, newSecret); err != nil && !apierrors.IsAlreadyExists(err) {
			federationSecretMigrations.WithLabelValues("failed").Inc()
			return false, fmt.Errorf("creating namespace-bound admin secret: %w", err)
		}
	case err != nil:
		return false, fmt.Errorf("getting namespace-bound admin secret: %w", err)
	default:
		if existing.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation] != remoteUnleash.Namespace ||
			string(existing.Data[unleashv1.UnleashSecretServerURLKey]) != remoteUnleash.Spec.Server.URL ||
			string(existing.Data[unleashv1.UnleashSecretTokenKey]) != string(token) {
			if r.Recorder != nil {
				r.Recorder.Event(remoteUnleash, "Warning", "FederationSecretMigrationConflict",
					"Namespace-bound admin secret already exists with conflicting content; manual resolution required")
			}
			federationSecretMigrations.WithLabelValues("failed").Inc()
			return false, fmt.Errorf("namespace-bound admin secret %s already exists with conflicting content", client.ObjectKeyFromObject(newSecret))
		}
	}

	// Repoint the RemoteUnleash before touching the legacy secret so a failure
	// here leaves the working reference intact. Re-check the premise inside the
	// retry: a concurrent federation update (e.g. rotation) must win.
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := r.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
			return err
		}
		if remoteUnleash.AdminSecretNamespacedName() != legacyKey ||
			remoteUnleash.Spec.Server.URL != string(newSecret.Data[unleashv1.UnleashSecretServerURLKey]) {
			return errMigrationSuperseded
		}
		remoteUnleash.Spec.AdminSecret.Name = secretName
		remoteUnleash.Spec.AdminSecret.Namespace = r.OperatorNamespace
		remoteUnleash.Spec.AdminSecret.Key = unleashv1.UnleashSecretTokenKey
		return r.Update(ctx, remoteUnleash)
	}); err != nil {
		if errors.Is(err, errMigrationSuperseded) {
			log.Info("Migration superseded by concurrent spec change", "remoteUnleash", remoteUnleash.NamespacedName())
			return false, nil
		}
		federationSecretMigrations.WithLabelValues("failed").Inc()
		return false, fmt.Errorf("repointing RemoteUnleash to namespace-bound admin secret: %w", err)
	}

	// By this point the shared-secret check already refused migration, so the
	// legacy secret is solely owned and can be deleted with preconditions.
	if err := r.Delete(ctx, legacySecret, &client.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID:             &legacySecret.UID,
			ResourceVersion: &legacySecret.ResourceVersion,
		},
	}); err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
		federationSecretMigrations.WithLabelValues("cleanup_failed").Inc()
		return true, fmt.Errorf("deleting legacy admin secret: %w", err)
	}

	log.Info("Migrated RemoteUnleash admin secret to namespace-bound layout",
		"remoteUnleash", remoteUnleash.NamespacedName(), "secret", secretName)
	if r.Recorder != nil {
		r.Recorder.Event(remoteUnleash, "Normal", "FederationSecretMigrated",
			"Admin secret migrated to the namespace-bound layout")
	}
	federationSecretMigrations.WithLabelValues("migrated").Inc()
	return true, nil
}
