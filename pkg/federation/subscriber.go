package federation

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub"
	"github.com/nais/unleasherator/pkg/pb"
	"github.com/nais/unleasherator/pkg/resources"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

type Subscriber interface {
	Subscribe(ctx context.Context, handler Handler) error
	Close() error
}

type Handler func(ctx context.Context, remoteUnleash []*unleashv1.RemoteUnleash, adminSecret *corev1.Secret, clusters []string, status pb.Status) error

type subscriber struct {
	client            *pubsub.Client
	subscription      *pubsub.Subscription
	operatorNamespace string
}

// Close the pubsub client.
func (s *subscriber) Close() error {
	return s.client.Close()
}

// Subscribe to a pubsub subscription, and call the handler function for each
// message received. To acknowledge a message, the handler function must return
// nil. To nack a message, the handler function must return an error.
// To stop receiving messages, cancel the context.
func (s *subscriber) Subscribe(ctx context.Context, handler Handler) error {
	log := log.FromContext(ctx).WithName("subscribe")
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	log.Info("waiting for messages")
	return s.subscription.Receive(cctx, func(ctx context.Context, msg *pubsub.Message) {
		log.Info("received message", "message", msg)
		if err := s.handleMessage(ctx, msg, handler); err != nil {
			log.Error(err, "nack message")
			msg.Nack()
		} else {
			log.Info("ack message")
			msg.Ack()
		}
	})
}

// handleMessage unmarshal the protobuf message, and calls the handler function
func (s *subscriber) handleMessage(ctx context.Context, msg *pubsub.Message, handler Handler) error {
	log := log.FromContext(ctx).WithName("subscribe")

	instance := &pb.Instance{}
	if err := proto.Unmarshal(msg.Data, instance); err != nil {
		log.Error(err, "unmarshal message")
		return err
	}

	secretNonce := instance.GetSecretNonce()
	if secretNonce == "" {
		secretNonce = "default"
		log.Info("secret nonce not set, using default")
	}

	secretName := fmt.Sprintf("unleasherator-%s-%s", instance.GetName(), secretNonce)
	adminSecret := resources.OperatorSecretForUnleash(instance.GetName(), secretName, s.operatorNamespace, instance.SecretToken)
	remoteUnleashes := resources.RemoteunleashInstances(instance.GetName(), instance.GetUrl(), instance.GetNamespaces(), adminSecret.GetName(), adminSecret.GetNamespace())

	return handler(ctx, remoteUnleashes, adminSecret, instance.Clusters, instance.Status)
}

func NewSubscriber(client *pubsub.Client, subscription *pubsub.Subscription, operatorNamespace string) Subscriber {
	return &subscriber{
		client:            client,
		subscription:      subscription,
		operatorNamespace: operatorNamespace,
	}
}
