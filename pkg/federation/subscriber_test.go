package federation

import (
	"context"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/google/uuid"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/pb"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSubscriber_Subscribe(t *testing.T) {
	ctx := context.Background()
	operatorNamespace := "my-ns"
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
	subscriber := NewSubscriber(c, subscription, operatorNamespace)

	started := make(chan bool)
	received := make(chan bool)
	done := false

	// Start a goroutine to consume messages from the subscription.
	go func() {
		started <- true

		err = subscriber.Subscribe(ctx, func(remoteUnleash []client.Object, adminSecret *corev1.Secret, namespaces []string, status pb.Status) error {
			assert.Equal(t, operatorNamespace, adminSecret.GetNamespace())
			assert.Equal(t, "unleasherator-test-random", adminSecret.GetName())
			assert.Equal(t, apiToken, adminSecret.StringData["token"])

			// @todo assert remoteUnleash

			received <- true

			return nil
		})

		// Don't assert error after the test is done.
		// This is because the subscriber will return an error when the test is done due to the subscription being closed.
		if !done {
			assert.NoError(t, err)
		}
	}()

	<-started

	// Publish a message to the topic.
	go func() {
		unleash := unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name: unleashName,
			},
			Spec: unleashv1.UnleashSpec{
				Size: 1,
			},
		}

		instance := UnleashFederationInstance(&unleash, apiToken)
		payload, err := proto.Marshal(instance)
		assert.NoError(t, err)

		msg := &pubsub.Message{
			ID:          uuid.New().String(),
			Data:        payload,
			PublishTime: time.Now(),
			OrderingKey: pubsubOrderingKey,
		}

		res := topic.Publish(ctx, msg)
		_, err = res.Get(ctx)
		assert.NoError(t, err)
	}()

	// Wait for the message to be received.
	<-received
	done = true
}

func TestSubscriber_handleMessage(t *testing.T) {
	var operatorNamespace = "unleasherator-system"

	instance := &pb.Instance{
		Name:       "test-instance",
		Url:        "https://test-instance.example.com",
		Namespaces: []string{"test-namespace"},
		Status:     pb.Status_Provisioned,
	}
	payload, err := proto.Marshal(instance)
	assert.NoError(t, err)

	msg := &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: pubsubOrderingKey,
	}

	var capturedRemoteUnleashes []client.Object
	var capturedAdminSecret *corev1.Secret
	var capturedNamespaces []string
	var capturedStatus pb.Status

	mockHandler := func(remoteUnleashes []client.Object, adminSecret *corev1.Secret, namespaces []string, status pb.Status) error {
		capturedRemoteUnleashes = remoteUnleashes
		capturedAdminSecret = adminSecret
		capturedNamespaces = namespaces
		capturedStatus = status
		return nil
	}

	subscriber := &subscriber{operatorNamespace: operatorNamespace}
	err = subscriber.handleMessage(context.Background(), msg, mockHandler)

	assert.NoError(t, err)

	assert.NotNil(t, capturedRemoteUnleashes)
	assert.Equal(t, len(capturedRemoteUnleashes), len(capturedNamespaces))
	assert.Equal(t, len(capturedRemoteUnleashes), 1)

	capturedRemoteUnleash := capturedRemoteUnleashes[0].(*unleashv1.RemoteUnleash)

	assert.Equal(t, instance.Name, capturedRemoteUnleash.GetName())
	assert.Equal(t, instance.Url, capturedRemoteUnleash.URL())
	assert.Equal(t, instance.Namespaces[0], capturedRemoteUnleash.GetNamespace())

	assert.NotNil(t, capturedAdminSecret)
	assert.True(t, strings.HasPrefix(capturedAdminSecret.Name, "unleasherator-"+instance.Name+"-"))
	assert.Equal(t, operatorNamespace, capturedAdminSecret.Namespace)
	assert.Equal(t, instance.SecretToken, capturedAdminSecret.StringData["admin"])

	assert.Equal(t, instance.Namespaces, capturedNamespaces)

	assert.Equal(t, instance.Status, capturedStatus)
}