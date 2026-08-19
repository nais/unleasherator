package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/federation"
	mockfederation "github.com/nais/unleasherator/internal/federation/mockfediration"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func federationTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	return scheme
}

// startFederationHandler runs the receive loop against a mock subscriber and
// hands back the handler it was given, so a test can deliver messages directly
// without repeating the subscription plumbing.
func startFederationHandler(ctx context.Context, t *testing.T, reconciler *RemoteUnleashReconciler) federation.Handler {
	t.Helper()

	handlerCh := make(chan federation.Handler, 1)
	mockSubscriber := &mockfederation.MockSubscriber{}
	mockSubscriber.On("Subscribe", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			handlerCh <- args.Get(1).(federation.Handler)
			<-args.Get(0).(context.Context).Done()
		}).
		Return(nil)
	reconciler.Federation.Subscriber = mockSubscriber

	errCh := make(chan error, 1)
	go func() { errCh <- reconciler.FederationSubscribe(ctx) }()

	select {
	case handler := <-handlerCh:
		return handler
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe was not called")
		return nil
	}
}

// federationFixture builds a RemoteUnleash together with the admin secret it
// references, with the URL and token agreeing. Anything less realistic slips
// past the URL and credential checks on the removal path for the wrong reason,
// and a test that never reaches its own assertion protects nothing.
func federationFixture(name, namespace, url, token string) (*unleashv1.RemoteUnleash, *corev1.Secret) {
	remoteUnleash := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: unleashv1.RemoteUnleashSpec{
			Server:      unleashv1.RemoteUnleashServer{URL: url},
			AdminSecret: unleashv1.RemoteUnleashSecret{Name: "unleasherator-" + name, Key: unleashv1.UnleashSecretTokenKey},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "unleasherator-" + name, Namespace: namespace},
		Data: map[string][]byte{
			unleashv1.UnleashSecretTokenKey:     []byte(token),
			unleashv1.UnleashSecretServerURLKey: []byte(url),
		},
	}
	return remoteUnleash, secret
}

func TestFederationSubscribeRetriesTransientErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var subscribeCalls atomic.Int32
	mockSubscriber := &mockfederation.MockSubscriber{}
	mockSubscriber.On("Subscribe", mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { subscribeCalls.Add(1) }).
		Return(errors.New("pubsub connection reset"))

	scheme := federationTestScheme(t)
	reconciler := &RemoteUnleashReconciler{
		Client:    fake.NewClientBuilder().WithScheme(scheme).Build(),
		APIReader: fake.NewClientBuilder().WithScheme(scheme).Build(),
		Scheme:    scheme,
		Federation: RemoteUnleashFederation{
			Enabled:     true,
			ClusterName: "test",
			Subscriber:  mockSubscriber,
		},
	}

	errCh := make(chan error, 1)
	go func() { errCh <- reconciler.FederationSubscribe(ctx) }()

	require.Eventually(t, func() bool { return subscribeCalls.Load() >= 2 }, 5*time.Second, 10*time.Millisecond,
		"transient receive errors must trigger a reconnect")
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(federationReceiveConsecutiveErrors) >= 2
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-errCh, "parent cancellation must shut down cleanly")
	assert.Zero(t, testutil.ToFloat64(federationReceiveConsecutiveErrors))
}

func TestFederationSubscribeReturnsPermanentHandlerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	forbidden := apierrors.NewForbidden(schema.GroupResource{Group: "unleash.nais.io", Resource: "remoteunleashes"}, "test", errors.New("rbac denied"))
	scheme := federationTestScheme(t)
	forbiddenClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return forbidden
			},
		}).
		Build()

	handlerCh := make(chan federation.Handler, 1)
	mockSubscriber := &mockfederation.MockSubscriber{}
	mockSubscriber.On("Subscribe", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			handlerCh <- args.Get(1).(federation.Handler)
			<-args.Get(0).(context.Context).Done()
		}).
		Return(nil)

	reconciler := &RemoteUnleashReconciler{
		Client:    forbiddenClient,
		APIReader: forbiddenClient,
		Scheme:    scheme,
		Federation: RemoteUnleashFederation{
			Enabled:     true,
			ClusterName: "test",
			Subscriber:  mockSubscriber,
		},
	}

	errCh := make(chan error, 1)
	go func() { errCh <- reconciler.FederationSubscribe(ctx) }()

	var handler federation.Handler
	select {
	case handler = <-handlerCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe was not called")
	}

	remoteUnleash := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "tenant"},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "secret", Namespace: "nais-system"}}

	// Concurrent callbacks hitting the same permanent failure must not race on
	// shared subscriber state; the first cancellation decides the cause.
	errs := make(chan error, 4)
	for range 4 {
		go func() {
			errs <- handler(ctx, []*unleashv1.RemoteUnleash{remoteUnleash}, []*corev1.Secret{secret}, []string{"test"}, pb.Status_Removed, time.Now())
		}()
	}
	for range 4 {
		err := <-errs
		assert.ErrorIs(t, err, forbidden)
		var permanent *federation.PermanentError
		assert.NotErrorAs(t, err, &permanent, "RBAC failures must be nacked, not dropped")
	}

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, forbidden, "permanent handler error must terminate the subscription")
	case <-time.After(5 * time.Second):
		t.Fatal("permanent handler error did not stop the subscription")
	}
}

func TestFederationSubscribeDisabled(t *testing.T) {
	reconciler := &RemoteUnleashReconciler{}
	require.NoError(t, reconciler.FederationSubscribe(context.Background()))
}

// A removal must reach every cluster, not only those on the message's cluster
// list. That list is the instance's federation config at the moment it was
// deleted, so a cluster dropped from it earlier would never learn the instance
// is gone — leaving a RemoteUnleash pointing at a server that no longer exists
// and alerting forever, with no counter or event explaining why.
func TestFederationRemovalReachesClustersNotOnTheMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	// The message names only cluster-b; this operator runs cluster-a.
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Removed,
		time.Now(),
	))

	err := c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{})
	assert.True(t, apierrors.IsNotFound(err),
		"a removal must delete the RemoteUnleash even when this cluster is not on the message")
}

// Taking a cluster out of a team's federation config must remove the resource
// there. Ignoring the message because this cluster is no longer named is what
// leaves a RemoteUnleash behind pointing at a server it may not reach.
func TestFederationProvisioningDeprovisionsDroppedCluster(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	// Still provisioned, but cluster-a has been taken off the list.
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		time.Now(),
	))

	err := c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{})
	assert.True(t, apierrors.IsNotFound(err),
		"a cluster dropped from the federation list must remove its RemoteUnleash")
}

// A message naming no clusters asserts nothing about placement. Treating it as
// "remove everywhere" would turn one malformed publish into fleet-wide deletion.
//
// The fixture is deliberately realistic: with an admin secret that matches, the
// deletion this test guards against would actually go through, so removing the
// guard fails this assertion rather than erroring out earlier for an unrelated
// reason.
func TestFederationEmptyClusterListIsIgnored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		nil,
		pb.Status_Provisioned,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"an empty cluster list must not delete anything")
}

// A cluster list whose entries are blank, or that names this cluster with stray
// whitespace, is a typo in a team's federation config — not a statement that the
// instance must leave. Exact comparison reads all of these as "not this cluster"
// and deletes.
func TestFederationBlankAndPaddedClusterEntriesDoNotDeprovision(t *testing.T) {
	for _, tt := range []struct {
		name     string
		clusters []string
	}{
		{name: "padded name", clusters: []string{" cluster-a"}},
		{name: "trailing whitespace", clusters: []string{"cluster-a\t"}},
		{name: "single empty entry", clusters: []string{""}},
		{name: "whitespace only entry", clusters: []string{"   "}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

			scheme := federationTestScheme(t)
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

			reconciler := &RemoteUnleashReconciler{
				Client:     c,
				APIReader:  c,
				Scheme:     scheme,
				Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
			}
			handler := startFederationHandler(ctx, t, reconciler)

			incoming := existing.DeepCopy()
			incoming.ResourceVersion = ""

			require.NoError(t, handler(ctx,
				[]*unleashv1.RemoteUnleash{incoming},
				[]*corev1.Secret{secret.DeepCopy()},
				tt.clusters,
				pb.Status_Provisioned,
				time.Now(),
			))

			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
				"cluster list %q must not delete this cluster's RemoteUnleash", tt.clusters)
		})
	}
}

// A cluster list that matches this cluster only when case is ignored is
// ambiguous. The comparison stays case-sensitive, so the instance is not
// provisioned here either, but ambiguity must never resolve to deletion.
func TestFederationCaseMismatchDoesNotDeprovision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"Cluster-A"},
		pb.Status_Provisioned,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"a cluster list differing only in case must not delete this cluster's RemoteUnleash")
}

// An operator that does not know its own cluster name matches no cluster list at
// all, so every provisioning message would read as "no longer federated here".
// Config validation rejects this at startup; the receive path must refuse it
// again, because one check standing between a dropped chart value and fleet-wide
// deletion is not enough.
func TestFederationEmptyOperatorClusterNameDoesNotDeprovision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: ""},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-a", "cluster-b"},
		pb.Status_Provisioned,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"an operator without a cluster name must not delete anything")
}

// Status_Unknown is the proto3 zero value, so an absent status field arrives as
// one. It is ignored on the clusters a message names; reading it as a removal on
// the clusters it does not name would make the least understood message the most
// destructive one.
func TestFederationUnknownStatusIsIgnored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Unknown,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"an unknown status must not be rewritten into a removal")
}

// Provisioning stays cluster-scoped for creation. Both halves matter: a message
// for another cluster must not create resources here, and a message naming this
// cluster must. Without the second half an implementation that creates nothing —
// or deletes everything — passes.
func TestFederationProvisioningStaysClusterScoped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	elsewhere, elsewhereSecret := federationFixture("other", "tenant", "https://other.example.com", "token")
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{elsewhere},
		[]*corev1.Secret{elsewhereSecret},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		time.Now(),
	))

	err := c.Get(ctx, client.ObjectKeyFromObject(elsewhere), &unleashv1.RemoteUnleash{})
	assert.True(t, apierrors.IsNotFound(err),
		"a provisioning message for another cluster must not create resources here")

	here, hereSecret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{here},
		[]*corev1.Secret{hereSecret},
		[]string{"cluster-a"},
		pb.Status_Provisioned,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(here), &unleashv1.RemoteUnleash{}),
		"a provisioning message naming this cluster must create the RemoteUnleash")
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(hereSecret), &corev1.Secret{}),
		"a provisioning message naming this cluster must create the admin secret")
}

// The anti-hijack checks must hold on the deprovision path too. It reaches every
// cluster, so a message that disagrees with what is stored locally is exactly
// where an attacker would aim: rotating the token is enough to delete a resource
// they cannot otherwise touch.
func TestFederationDeprovisionRefusesTokenRotation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""
	rotatedSecret := secret.DeepCopy()
	rotatedSecret.ResourceVersion = ""
	rotatedSecret.Data[unleashv1.UnleashSecretTokenKey] = []byte("rotated")

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{rotatedSecret},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"a deprovision carrying a token that does not match the stored one must be refused")
}

// The same for the URL: a message claiming a different server than the one the
// local resource points at is not talking about this resource, whether it asks
// for a removal or arrives as a provisioning message that drops this cluster.
func TestFederationDeprovisionRefusesURLMismatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""
	incoming.Spec.Server.URL = "https://attacker.example.com"

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		time.Now(),
	))

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"a deprovision naming a different server must be refused")
}

// Pub/Sub redelivers, and an ordering key only orders what it hands over in one
// go: a delayed copy of an older provisioning message can arrive after a newer
// one was applied, carrying the cluster list as it stood back then. Acting on it
// deletes a RemoteUnleash the newer message legitimately created, and nothing
// brings it back — the publisher skips republishing while its instance hash is
// unchanged, and there is no periodic federation resync.
func TestFederationStaleMessageDoesNotDeprovision(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	remoteUnleash, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	// The current state of the world: federated here, published just now.
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{remoteUnleash.DeepCopy()},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-a"},
		pb.Status_Provisioned,
		newer,
	))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(remoteUnleash), &unleashv1.RemoteUnleash{}))

	// A redelivered copy of the message from before this cluster was added.
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{remoteUnleash.DeepCopy()},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		older,
	))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(remoteUnleash), &unleashv1.RemoteUnleash{}),
		"a message older than the one already applied must not delete the RemoteUnleash")

	// Replaying the older provisioning message must not roll the recorded time
	// back either, or the window it closes simply reopens.
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{remoteUnleash.DeepCopy()},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-a"},
		pb.Status_Provisioned,
		older,
	))
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{remoteUnleash.DeepCopy()},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		newer.Add(-time.Minute),
	))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(remoteUnleash), &unleashv1.RemoteUnleash{}),
		"a replayed provisioning message must not roll back the last applied publish time")

	// The guard is about ordering, not about refusing deletions: a genuinely
	// newer message still takes the cluster out.
	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{remoteUnleash.DeepCopy()},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Provisioned,
		newer.Add(time.Minute),
	))
	err := c.Get(ctx, client.ObjectKeyFromObject(remoteUnleash), &unleashv1.RemoteUnleash{})
	assert.True(t, apierrors.IsNotFound(err),
		"a message newer than the one already applied must still deprovision")
}

// A resource that has never had a federation message applied to it carries no
// recorded publish time. That is unknown, not "newer than everything": refusing
// removals for it would make every resource predating the annotation
// undeletable by federation, which is the orphan bug this path exists to fix.
func TestFederationRemovalWithoutRecordedPublishTime(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing, secret).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Removed,
		time.Now(),
	))

	err := c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{})
	assert.True(t, apierrors.IsNotFound(err),
		"a resource with no recorded publish time must still be removable")
}

// A RemoteUnleash whose admin secret is gone cannot have a removal authenticated
// against it. Erroring out nacks the message forever — the secret is not coming
// back — and blocks every later message sharing the ordering key, which since
// removals reach every cluster means the whole fleet rather than the clusters the
// message names. Skip the resource instead, and leave it standing: deleting what
// cannot be verified is the hijack the credential check exists to stop.
func TestFederationRemovalWithMissingAdminSecretIsSkipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	existing, secret := federationFixture("aura", "tenant", "https://unleash.example.com", "token")

	scheme := federationTestScheme(t)
	// The RemoteUnleash exists; the admin secret it references does not.
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()

	reconciler := &RemoteUnleashReconciler{
		Client:     c,
		APIReader:  c,
		Scheme:     scheme,
		Federation: RemoteUnleashFederation{Enabled: true, ClusterName: "cluster-a"},
	}
	handler := startFederationHandler(ctx, t, reconciler)

	incoming := existing.DeepCopy()
	incoming.ResourceVersion = ""

	require.NoError(t, handler(ctx,
		[]*unleashv1.RemoteUnleash{incoming},
		[]*corev1.Secret{secret.DeepCopy()},
		[]string{"cluster-b"},
		pb.Status_Removed,
		time.Now(),
	), "a missing admin secret must not nack the message forever")

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), &unleashv1.RemoteUnleash{}),
		"a RemoteUnleash whose token could not be verified must not be deleted")
}
