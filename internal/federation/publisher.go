package federation

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/google/uuid"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/pb"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const pubsubOrderingKey = "order"

type Publisher interface {
	Publish(ctx context.Context, unleash *unleashv1.Unleash, apiToken string) error
	PublishRemoved(ctx context.Context, unleash *unleashv1.Unleash) error
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

func (p *publisher) otelSpanOptions() []trace.SpanStartOption {
	return []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemGCPPubsub,
			semconv.MessagingDestinationName(p.topic.ID()),
		),
	}
}

// Publish publishes the given Unleash instance to the federation topic using the provided API token.
// Returns an error if the message could not be published.
func (p *publisher) Publish(ctx context.Context, unleash *unleashv1.Unleash, apiToken string) error {
	instance := UnleashFederationInstance(unleash, apiToken)
	return p.publish(ctx, instance)
}

// PublishRemoved publishes a removal message for the given Unleash instance.
// This notifies federated clusters to delete their RemoteUnleash resources.
func (p *publisher) PublishRemoved(ctx context.Context, unleash *unleashv1.Unleash) error {
	instance := UnleashFederationInstanceRemoved(unleash)
	return p.publish(ctx, instance)
}

func (p *publisher) publish(ctx context.Context, instance *pb.Instance) error {
	log := log.FromContext(ctx).WithName("publish")

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
		Attributes:  make(map[string]string),
	}

	// Set pubsub information on span
	spanOpts := p.otelSpanOptions()

	// Inject trace context into message attributes
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.Attributes))

	ctx, span := otel.Tracer("publish").Start(ctx, "Send PubSub", spanOpts...)
	defer span.End()

	msgId, err := p.topic.Publish(ctx, msg).Get(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.Error(err, "failed to publish protobuf message")
		return fmt.Errorf("publish protobuf message: %w", err)
	}

	// Add message ID to span attributes
	span.SetAttributes(semconv.MessagingMessageIDKey.String(msgId))

	log.Info("published message to topic")
	return nil
}

func NewPublisher(client *pubsub.Client, topic *pubsub.Topic) Publisher {
	// Fix for the following pubsub error, this clears the ordering key for the topic when the publisher is created
	// pubsub: Publishing for ordering key, order, paused due to previous error. Call topic.ResumePublish(orderingKey) before resuming publishing
	topic.ResumePublish(pubsubOrderingKey)
	return &publisher{client: client, topic: topic}
}
