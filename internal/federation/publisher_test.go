package federation

import (
	"context"
	"testing"

	"cloud.google.com/go/pubsub"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPublisherPublish(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	apiToken := "test"
	unleashName := "test"
	unleashNamespace := "my-ns"

	finished := false
	received := make(chan bool)

	srv, conn, c, topic, subscription, err := newPubSub(ctx, "publisher-test")
	if err != nil {
		t.Fatal("Fatal", err)
	}
	defer srv.Close()
	defer conn.Close()
	defer c.Close()

	publisher := NewPublisher(c, topic)
	unleash := unleashv1.Unleash{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Unleash",
			APIVersion: "unleash.nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      unleashName,
			Namespace: unleashNamespace,
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			ApiIngress: unleashv1.UnleashIngressConfig{
				Host: "test",
			},
			Federation: unleashv1.UnleashFederationConfig{
				Namespaces: []string{"namespace-1", "namespace-2"},
				Clusters:   []string{"cluster-1", "cluster-2"},
			},
		},
	}

	err = publisher.Publish(ctx, &unleash, apiToken)
	assert.NoError(t, err)

	// This needs to be run in a separate goroutine, otherwise the channel will block
	go func() {
		err = subscription.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
			instance := &pb.Instance{}
			err := proto.Unmarshal(msg.Data, instance)
			assert.NoError(t, err)

			assert.Equal(t, pb.Version, int(instance.Version))
			assert.Equal(t, pb.Status_Provisioned, instance.Status)
			assert.Equal(t, unleashName, instance.Name)
			assert.Equal(t, unleash.PublicApiURL(), instance.Url)
			assert.Equal(t, apiToken, instance.SecretToken)
			assert.Equal(t, []string{"namespace-1", "namespace-2"}, instance.Namespaces)
			assert.Equal(t, []string{"cluster-1", "cluster-2"}, instance.Clusters)

			received <- true
		})

		// Prevent asserts from running after the test has finished
		if !finished {
			assert.NoError(t, err)
		}
	}()

	<-received
	finished = true
	cancel()
}
