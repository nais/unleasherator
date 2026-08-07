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
			errs <- handler(ctx, []*unleashv1.RemoteUnleash{remoteUnleash}, []*corev1.Secret{secret}, []string{"test"}, pb.Status_Removed)
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
