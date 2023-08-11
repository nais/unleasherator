package federation

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/google/uuid"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const pubsubOrderingKey = "order"

type Publisher interface {
	Publish(ctx context.Context, unleash *unleashv1.Unleash, apiToken string) error
	Close() error
}

type publisher struct {
	client *pubsub.Client
	topic  *pubsub.Topic
}

// Close the pubsub client.
func (p *publisher) Close() error {
	return p.client.Close()
}

// Publish publishes the given Unleash instance to the federation topic using the provided API token.
// Returns an error if the message could not be published.
func (p *publisher) Publish(ctx context.Context, unleash *unleashv1.Unleash, apiToken string) error {
	log := log.FromContext(ctx).WithName("publisher")

	instance := UnleashFederationInstance(unleash, apiToken)
	payload, err := proto.Marshal(instance)
	if err != nil {
		log.Error(err, "failed to marshal protobuf message")
		return fmt.Errorf("marshal protobuf message: %w", err)
	}

	msg := &pubsub.Message{
		ID:          uuid.New().String(),
		Data:        payload,
		PublishTime: time.Now(),
		OrderingKey: pubsubOrderingKey,
	}
	result := p.topic.Publish(ctx, msg)
	_, err = result.Get(ctx)
	if err != nil {
		log.Error(err, "failed to publish protobuf message")
		return fmt.Errorf("publish protobuf message: %w", err)
	}

	log.Info("published message to topic")
	return nil
}

func NewPublisher(client *pubsub.Client, topic *pubsub.Topic) Publisher {
	return &publisher{client: client, topic: topic}
}
