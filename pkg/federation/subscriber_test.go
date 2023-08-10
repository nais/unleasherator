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
	ctx, cancel := context.WithCancel(context.Background())
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

	received := make(chan bool)
	finished := false

	unleash := unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name: unleashName,
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Federation: unleashv1.UnleashFederationConfig{
				Namespaces: []string{"namespace-1", "namespace-2"},
				Clusters:   []string{"cluster-1", "cluster-2"},
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
		OrderingKey: pubsubOrderingKey,
	}

	res := topic.Publish(ctx, msg)
	_, err = res.Get(ctx)
	assert.NoError(t, err)

	// Start a goroutine to consume messages from the subscription.
	go func() {
		err = subscriber.Subscribe(ctx, func(ctx context.Context, remoteUnleash []client.Object, adminSecret *corev1.Secret, clusters []string, status pb.Status) error {
			assert.Equal(t, operatorNamespace, adminSecret.GetNamespace())
			assert.Equal(t, "unleasherator-test-random", adminSecret.GetName())
			assert.Equal(t, apiToken, adminSecret.StringData["token"])
			assert.Equal(t, clusters, []string{"cluster-1", "cluster-2"})

			// @todo assert remoteUnleash

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
	var operatorNamespace = "unleasherator-system"

	instance := &pb.Instance{
		Name:       "test-instance",
		Url:        "https://test-instance.example.com",
		Namespaces: []string{"namespace-a"},
		Clusters:   []string{"cluster-a"},
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
	var capturedClusters []string
	var capturedStatus pb.Status

	mockHandler := func(ctx context.Context, remoteUnleashes []client.Object, adminSecret *corev1.Secret, clusters []string, status pb.Status) error {
		capturedRemoteUnleashes = remoteUnleashes
		capturedAdminSecret = adminSecret
		capturedClusters = clusters
		capturedStatus = status
		return nil
	}

	subscriber := &subscriber{operatorNamespace: operatorNamespace}
	err = subscriber.handleMessage(context.Background(), msg, mockHandler)

	assert.NoError(t, err)

	assert.NotNil(t, capturedRemoteUnleashes)
	assert.Equal(t, len(capturedRemoteUnleashes), 1)
	assert.Equal(t, len(capturedClusters), 1)

	capturedRemoteUnleash := capturedRemoteUnleashes[0].(*unleashv1.RemoteUnleash)

	assert.Equal(t, instance.Name, capturedRemoteUnleash.GetName())
	assert.Equal(t, instance.Url, capturedRemoteUnleash.URL())
	assert.Equal(t, instance.Namespaces[0], capturedRemoteUnleash.GetNamespace())

	assert.NotNil(t, capturedAdminSecret)
	assert.True(t, strings.HasPrefix(capturedAdminSecret.Name, "unleasherator-"+instance.Name+"-"))
	assert.Equal(t, operatorNamespace, capturedAdminSecret.Namespace)
	assert.Equal(t, instance.SecretToken, capturedAdminSecret.StringData["admin"])
	assert.Equal(t, instance.Clusters, capturedClusters)
	assert.Equal(t, instance.Status, capturedStatus)
}
