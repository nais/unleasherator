package federation

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"github.com/google/uuid"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSubscriber_Subscribe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	namespace := "my-ns"
	apiToken := "test"
	unleashName := "test"

	srv, conn, c, topic, subscription, err := newPubSub(ctx, "subscriber-test-topic")
	if err != nil {
		t.Fatal("Fatal", err)
	}
	defer srv.Close()
	defer conn.Close()
	defer c.Close()

	// Create a new subscriber.
	subscriber := NewSubscriber(c, subscription, namespace, true)

	received := make(chan bool)
	finished := false

	unleash := unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name: unleashName,
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Federation: unleashv1.UnleashFederationConfig{
				Namespaces:  []string{"namespace-1", "namespace-2"},
				Clusters:    []string{"cluster-1", "cluster-2"},
				SecretNonce: "not-a-real-nonce",
			},
		},
	}

	instance := UnleashFederationInstance(&unleash, apiToken)
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)

	msg := &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: unleashName,
	}

	res := topic.Publish(ctx, msg)
	_, err = res.Get(ctx)
	assert.NoError(t, err)

	// Start a goroutine to consume messages from the subscription.
	go func() {
		err = subscriber.Subscribe(ctx, func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecrets []*corev1.Secret, clusters []string, status pb.Status, publishTime time.Time) error {
			assert.Equal(t, 2, len(adminSecrets))
			assert.Equal(t, namespace, adminSecrets[0].GetNamespace())
			assert.Equal(t, namespace, adminSecrets[1].GetNamespace())
			assert.Equal(t, "unleasherator-test-namespace-1-admin-key-not-a-real-nonce", adminSecrets[0].GetName())
			assert.Equal(t, "unleasherator-test-namespace-2-admin-key-not-a-real-nonce", adminSecrets[1].GetName())
			assert.Equal(t, apiToken, adminSecrets[0].StringData["token"])
			assert.Equal(t, clusters, []string{"cluster-1", "cluster-2"})

			assert.Equal(t, 2, len(remoteUnleashes))
			assert.Equal(t, "namespace-1", remoteUnleashes[0].GetNamespace())
			assert.Equal(t, "namespace-2", remoteUnleashes[1].GetNamespace())

			received <- true

			return nil
		})

		// Don't assert error after the test is finished.
		// This is because the subscriber will return an error when the test is finished due to the subscription being closed.
		if !finished {
			assert.NoError(t, err)
		}
	}()

	// Wait for the message to be received.
	<-received
	finished = true
	cancel()
}

func TestSubscriber_handleMessage(t *testing.T) {
	var namespace = "unleasherator-system"

	instance := &pb.Instance{
		Name:        "test-instance",
		Url:         "https://test-instance.example.com",
		SecretToken: "admin-token",
		Namespaces:  []string{"namespace-a"},
		Clusters:    []string{"cluster-a"},
		Status:      pb.Status_Provisioned,
	}
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)

	msg := &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: instance.Name,
	}

	var capturedRemoteUnleashes []*unleashv1.RemoteUnleash
	var capturedAdminSecrets []*corev1.Secret
	var capturedClusters []string
	var capturedStatus pb.Status

	mockHandler := func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecrets []*corev1.Secret, clusters []string, status pb.Status, publishTime time.Time) error {
		capturedRemoteUnleashes = remoteUnleashes
		capturedAdminSecrets = adminSecrets
		capturedClusters = clusters
		capturedStatus = status
		return nil
	}

	subscriber := &subscriber{namespace: namespace, namespaceBoundSecrets: true}
	err = subscriber.handleMessage(context.Background(), msg, mockHandler)

	assert.NoError(t, err)

	assert.NotNil(t, capturedRemoteUnleashes)
	assert.Equal(t, len(capturedRemoteUnleashes), 1)
	assert.Equal(t, len(capturedClusters), 1)

	capturedRemoteUnleash := capturedRemoteUnleashes[0]

	assert.Equal(t, instance.Name, capturedRemoteUnleash.GetName())
	assert.Equal(t, instance.Url, capturedRemoteUnleash.URL())
	assert.Equal(t, instance.Namespaces[0], capturedRemoteUnleash.GetNamespace())

	assert.NotNil(t, capturedAdminSecrets)
	assert.Equal(t, 1, len(capturedAdminSecrets))
	// The empty-nonce path derives a stable nonce, so assert on the generated
	// name shape here; stableNonce has exact-value coverage separately.
	assert.True(t, strings.HasPrefix(capturedAdminSecrets[0].Name, "unleasherator-test-instance-namespace-a-admin-key-"),
		"unexpected secret name %q", capturedAdminSecrets[0].Name)
	assert.Greater(t, len(capturedAdminSecrets[0].Name), len("unleasherator-test-instance-namespace-a-admin-key-"))
	assert.Equal(t, "unleasherator-system", capturedAdminSecrets[0].Namespace)
	// Namespace-bound secrets carry the authoritative authorized-namespace annotation.
	assert.Equal(t, "namespace-a", capturedAdminSecrets[0].Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation])
	assert.Equal(t, instance.SecretToken, capturedAdminSecrets[0].StringData[unleashv1.UnleashSecretTokenKey])
	assert.Equal(t, instance.Clusters, capturedClusters)
	assert.Equal(t, instance.Status, capturedStatus)
}

func TestSubscriber_handleMessage_Legacy(t *testing.T) {
	var namespace = "unleasherator-system"

	instance := &pb.Instance{
		Name:        "test-instance-legacy",
		Url:         "https://test-instance.example.com",
		SecretToken: "admin-token",
		Namespaces:  []string{"namespace-a"},
		Clusters:    []string{"cluster-a"},
		Status:      pb.Status_Provisioned,
	}
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)

	msg := &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: instance.Name,
	}

	var capturedRemoteUnleashes []*unleashv1.RemoteUnleash
	var capturedAdminSecrets []*corev1.Secret

	mockHandler := func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecrets []*corev1.Secret, clusters []string, status pb.Status, publishTime time.Time) error {
		capturedRemoteUnleashes = remoteUnleashes
		capturedAdminSecrets = adminSecrets
		return nil
	}

	subscriber := &subscriber{namespace: namespace, namespaceBoundSecrets: false}
	err = subscriber.handleMessage(context.Background(), msg, mockHandler)

	assert.NoError(t, err)
	assert.Equal(t, 1, len(capturedRemoteUnleashes))

	// Legacy mode creates one admin secret PER namespace in the TENANT's own namespace,
	// keeping N secrets aligned with N RemoteUnleashes (no orphaning/relocation, no panic).
	assert.Equal(t, 1, len(capturedAdminSecrets))
	assert.Equal(t, "namespace-a", capturedAdminSecrets[0].Namespace)
	assert.True(t, strings.HasPrefix(capturedAdminSecrets[0].Name, "unleasherator-test-instance-legacy-"),
		"unexpected secret name %q", capturedAdminSecrets[0].Name)
	assert.Equal(t, "namespace-a", capturedAdminSecrets[0].Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation])

	// Legacy mode references the secret in the RemoteUnleash's own namespace (empty = same namespace).
	assert.Equal(t, "", capturedRemoteUnleashes[0].Spec.AdminSecret.Namespace)
}

func TestStableNonce(t *testing.T) {
	instance := &pb.Instance{
		Name:        "test-instance",
		Url:         "https://test-instance.example.com",
		SecretToken: "admin-token",
	}

	first, err := stableNonce(instance)
	assert.NoError(t, err)
	second, err := stableNonce(instance)
	assert.NoError(t, err)
	assert.Equal(t, first, second, "redelivery must produce the same secret name")

	rotated := proto.Clone(instance).(*pb.Instance)
	rotated.SecretToken = "different-token"
	rotatedNonce, err := stableNonce(rotated)
	assert.NoError(t, err)
	assert.NotEqual(t, first, rotatedNonce)

	instance.SecretToken = ""
	_, err = stableNonce(instance)
	assert.Error(t, err)
}

func TestSubscriberIgnoresUnauthenticatedLegacyRemoval(t *testing.T) {
	payload, err := proto.Marshal(&pb.Instance{
		Name:       "test-instance",
		Url:        "https://test-instance.example.com",
		Namespaces: []string{"namespace-a"},
		Status:     pb.Status_Removed,
	})
	assert.NoError(t, err)

	handlerCalled := false
	subscriber := &subscriber{namespace: "unleasherator-system", namespaceBoundSecrets: true}
	err = subscriber.handleMessage(context.Background(), &pubsub.Message{Data: payload}, func(
		context.Context,
		[]*unleashv1.RemoteUnleash,
		[]*corev1.Secret,
		[]string,
		pb.Status,
		time.Time,
	) error {
		handlerCalled = true
		return nil
	})

	assert.NoError(t, err)
	assert.False(t, handlerCalled)
}

func TestSubscriberDropsPoisonMessage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	srv, conn, c, topic, subscription, err := newPubSub(ctx, "poison-test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer conn.Close()
	defer c.Close()
	// LIFO: Receive is cancelled before the test server is torn down.
	defer cancel()

	subscriber := NewSubscriber(c, subscription, "unleasherator-system", true)

	handlerCalls := make(chan struct{}, 10)
	go func() {
		_ = subscriber.Subscribe(ctx, func(context.Context, []*unleashv1.RemoteUnleash, []*corev1.Secret, []string, pb.Status, time.Time) error {
			handlerCalls <- struct{}{}
			return nil
		})
	}()

	// Poison message: bytes that can never unmarshal into pb.Instance.
	res := topic.Publish(ctx, &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        []byte("not-a-protobuf"),
		PublishTime: time.Now(),
		OrderingKey: "poison-ordering",
	})
	_, err = res.Get(ctx)
	assert.NoError(t, err)

	// A valid message with the same ordering key must not be blocked by the
	// poison message: if the poison were nacked, redelivery would starve it.
	instance := &pb.Instance{
		Name:        "after-poison",
		Url:         "https://after.example.com",
		SecretToken: "token",
		Namespaces:  []string{"namespace-a"},
		Clusters:    []string{"cluster-a"},
		Status:      pb.Status_Provisioned,
	}
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)
	res = topic.Publish(ctx, &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: "poison-ordering",
	})
	_, err = res.Get(ctx)
	assert.NoError(t, err)

	select {
	case <-handlerCalls:
	case <-time.After(10 * time.Second):
		t.Fatal("valid message was not processed; poison message blocked the ordering key")
	}

	assert.Eventually(t, func() bool {
		return testutil.ToFloat64(federationPoisonMessages.WithLabelValues(subscription.ID())) >= 1
	}, 5*time.Second, 10*time.Millisecond, "poison message must be counted before being dropped")
}

func TestSubscriberAcksPermanentHandlerError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	srv, conn, c, topic, subscription, err := newPubSub(ctx, "permanent-test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer conn.Close()
	defer c.Close()
	// LIFO: Receive is cancelled before the test server is torn down.
	defer cancel()

	subscriber := NewSubscriber(c, subscription, "unleasherator-system", true)

	var calls atomic.Int32
	go func() {
		_ = subscriber.Subscribe(ctx, func(context.Context, []*unleashv1.RemoteUnleash, []*corev1.Secret, []string, pb.Status, time.Time) error {
			calls.Add(1)
			return Permanent(errors.New("authorization denied"))
		})
	}()

	instance := &pb.Instance{
		Name:        "permanent-instance",
		Url:         "https://permanent.example.com",
		SecretToken: "token",
		Namespaces:  []string{"namespace-a"},
		Clusters:    []string{"cluster-a"},
		Status:      pb.Status_Provisioned,
	}
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)
	res := topic.Publish(ctx, &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: "permanent-ordering",
	})
	_, err = res.Get(ctx)
	assert.NoError(t, err)

	assert.Eventually(t, func() bool { return calls.Load() == 1 }, 10*time.Second, 10*time.Millisecond)
	// A nacked message would be redelivered promptly; an acked poison message
	// must not be handled again.
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load(), "permanent error must be acked, not redelivered")
	assert.GreaterOrEqual(t, testutil.ToFloat64(federationPoisonMessages.WithLabelValues(subscription.ID())), float64(1))
}

func TestSubscriberRedeliversTransientHandlerError(t *testing.T) {
	// pstest redelivers nacked messages only after the subscription ack
	// deadline; the default 10s makes this test slow and flaky.
	pstest.SetMinAckDeadline(2 * time.Second)
	defer pstest.ResetMinAckDeadline()

	ctx, cancel := context.WithCancel(context.Background())

	srv, conn, c, topic, subscription, err := newPubSub(ctx, "transient-test")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	defer conn.Close()
	defer c.Close()
	// LIFO: Receive is cancelled before the test server is torn down.
	defer cancel()

	subscriber := NewSubscriber(c, subscription, "unleasherator-system", true)

	_, err = subscription.Update(ctx, pubsub.SubscriptionConfigToUpdate{AckDeadline: 2 * time.Second})
	assert.NoError(t, err)

	var calls atomic.Int32
	go func() {
		_ = subscriber.Subscribe(ctx, func(context.Context, []*unleashv1.RemoteUnleash, []*corev1.Secret, []string, pb.Status, time.Time) error {
			calls.Add(1)
			return errors.New("temporary API server failure")
		})
	}()

	instance := &pb.Instance{
		Name:        "transient-instance",
		Url:         "https://transient.example.com",
		SecretToken: "token",
		Namespaces:  []string{"namespace-a"},
		Clusters:    []string{"cluster-a"},
		Status:      pb.Status_Provisioned,
	}
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)
	res := topic.Publish(ctx, &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: "transient-ordering",
	})
	_, err = res.Get(ctx)
	assert.NoError(t, err)

	assert.Eventually(t, func() bool { return calls.Load() >= 2 }, 30*time.Second, 50*time.Millisecond,
		"transient errors must be nacked and redelivered")
}
