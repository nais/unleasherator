package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"cloud.google.com/go/pubsub"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/nais/unleasherator/internal/resources"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

// PermanentError marks a message that can never be processed successfully
// because its payload is malformed. Such messages are poison: redelivering
// them only blocks the subscription ordering key, so they are acknowledged
// and dropped with an observable metric instead of nacked.
//
// Recoverable operator-side failures (RBAC, API errors) must NOT use this:
// they cancel the subscription to force a restart, but the message is nacked
// so it is redelivered once the operator is healthy again.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so the subscriber acknowledges and drops the message.
func Permanent(err error) *PermanentError {
	return &PermanentError{Err: err}
}

var federationPoisonMessages = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "unleasherator_federation_poison_messages_total",
		Help: "Number of federation Pub/Sub messages dropped after permanent failure",
	},
	[]string{"subscription"},
)

func init() {
	metrics.Registry.MustRegister(federationPoisonMessages)
}

type Subscriber interface {
	Subscribe(ctx context.Context, handler Handler) error
	Close() error
}

type Handler func(ctx context.Context, remoteUnleashes []*unleashv1.RemoteUnleash, adminSecrets []*corev1.Secret, clusters []string, status pb.Status) error

type subscriber struct {
	client                *pubsub.Client
	subscription          *pubsub.Subscription
	namespace             string
	namespaceBoundSecrets bool
}

// Close the pubsub client.
func (s *subscriber) Close() error {
	return s.client.Close()
}

func (s *subscriber) otelSpanOptions(msg *pubsub.Message) []trace.SpanStartOption {
	// This is only ever nil in tests.
	subId := ""
	if s.subscription != nil {
		subId = s.subscription.ID()
	}

	return []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			semconv.MessagingSystemGCPPubsub,
			semconv.MessagingDestinationName(subId),
			semconv.MessagingMessageID(msg.ID),
		),
	}
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
		log.Info("received message")

		spanOpts := s.otelSpanOptions(msg)

		if msg.Attributes != nil {
			propagator := otel.GetTextMapPropagator()
			ctx = propagator.Extract(ctx, propagation.MapCarrier(msg.Attributes))
		}

		ctx, span := otel.Tracer("subscribe").Start(ctx, "Receive PubSub", spanOpts...)
		defer span.End()

		if err := s.handleMessage(ctx, msg, handler); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())

			var permanent *PermanentError
			if errors.As(err, &permanent) {
				// Count before acking so a dropped message is observable even
				// if the log line is lost.
				subID := ""
				if s.subscription != nil {
					subID = s.subscription.ID()
				}
				federationPoisonMessages.WithLabelValues(subID).Inc()
				log.Error(err, "dropping poison message")
				msg.Ack()
			} else {
				log.Error(err, "nack message")
				msg.Nack()
			}
		} else {
			log.Info("ack message")
			msg.Ack()
		}
	})
}

// handleMessage unmarshal the protobuf message, and calls the handler function
func (s *subscriber) handleMessage(ctx context.Context, msg *pubsub.Message, handler Handler) error {
	spanOps := s.otelSpanOptions(msg)
	ctx, span := otel.Tracer("subscribe").Start(ctx, "Unpack PubSub", spanOps...)
	defer span.End()

	log := log.FromContext(ctx).WithName("subscribe")

	instance := &pb.Instance{}
	if err := proto.Unmarshal(msg.Data, instance); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		log.Error(err, "unmarshal message")
		// An unparseable payload can never succeed; drop it instead of
		// redelivering forever.
		return Permanent(fmt.Errorf("unmarshal federation message: %w", err))
	}

	if instance.GetStatus() == pb.Status_Removed && instance.GetSecretToken() == "" {
		// Removal requires the credential currently bound to the resource.
		// Legacy unauthenticated events cannot delete safely, but retrying them
		// would block every later event sharing the Pub/Sub ordering key.
		log.Info("ignoring unauthenticated legacy removal message")
		return nil
	}

	secretNonce := instance.GetSecretNonce()
	if secretNonce == "" {
		nonce, err := stableNonce(instance)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			log.Error(err, "derive stable secret nonce")
			return err
		}
		secretNonce = nonce
		log.Info("secret nonce not set, derived stable nonce")
	}

	var (
		adminSecrets    []*corev1.Secret
		remoteUnleashes []*unleashv1.RemoteUnleash
	)

	if s.namespaceBoundSecrets {
		// Namespace-bound behavior: secrets live in the OPERATOR namespace (so tenants
		// cannot read them) and each is stamped with the authoritative
		// authorized-namespace annotation. The controller uses that annotation as the
		// primary confused-deputy defense, so it cannot be bypassed by crafting a
		// RemoteUnleash name.
		for _, namespace := range instance.GetNamespaces() {
			secretName := fmt.Sprintf("unleasherator-%s-%s-admin-key-%s", instance.GetName(), namespace, secretNonce)
			adminSecret := resources.OperatorSecretForUnleash(instance.GetName(), secretName, s.namespace, instance.SecretToken, instance.GetUrl())
			setAuthorizedNamespace(adminSecret, namespace)
			adminSecrets = append(adminSecrets, adminSecret)

			ru := resources.RemoteunleashInstances(instance.GetName(), instance.GetUrl(), []string{namespace}, secretName, s.namespace)
			remoteUnleashes = append(remoteUnleashes, ru...)
		}
	} else {
		// Legacy behavior: create ONE admin secret PER namespace in the TENANT's OWN
		// namespace (secretNamespace=""), matching the original pre-migration layout.
		// This keeps N secrets aligned with N RemoteUnleashes (avoiding the
		// index-out-of-range panic downstream) and avoids relocating/orphaning tenant
		// secrets in the operator namespace. Same-namespace references do not hit the
		// cross-namespace authorization path in the controller.
		secretName := fmt.Sprintf("unleasherator-%s-%s", instance.GetName(), secretNonce)
		for _, namespace := range instance.GetNamespaces() {
			adminSecret := resources.OperatorSecretForUnleash(instance.GetName(), secretName, namespace, instance.SecretToken, instance.GetUrl())
			setAuthorizedNamespace(adminSecret, namespace)
			adminSecrets = append(adminSecrets, adminSecret)
		}

		remoteUnleashes = resources.RemoteunleashInstances(instance.GetName(), instance.GetUrl(), instance.GetNamespaces(), secretName, "")
	}

	ctx, subspan := otel.Tracer("subscribe").Start(ctx, "Process PubSub", spanOps...)
	defer subspan.End()
	return handler(ctx, remoteUnleashes, adminSecrets, instance.Clusters, instance.Status)
}

func stableNonce(instance *pb.Instance) (string, error) {
	return StableSecretNonce(instance.GetName(), instance.GetUrl(), instance.GetSecretToken())
}

// StableSecretNonce derives a deterministic secret nonce from the instance
// identity and credential, so reconciles and republications converge on the
// same secret name while credential rotation produces a new one.
func StableSecretNonce(name, url, secretToken string) (string, error) {
	if secretToken == "" {
		return "", fmt.Errorf("cannot derive stable nonce without an admin token")
	}

	hash := sha256.New()
	hash.Write([]byte(name))
	hash.Write([]byte{0})
	hash.Write([]byte(url))
	hash.Write([]byte{0})
	hash.Write([]byte(secretToken))

	return hex.EncodeToString(hash.Sum(nil)[:6]), nil
}

// setAuthorizedNamespace stamps the authoritative authorized-namespace annotation
// on an admin secret, recording the single tenant namespace permitted to use it.
func setAuthorizedNamespace(secret *corev1.Secret, namespace string) {
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[unleashv1.UnleashSecretAuthorizedNamespaceAnnotation] = namespace
}

func NewSubscriber(client *pubsub.Client, subscription *pubsub.Subscription, namespace string, namespaceBoundSecrets bool) Subscriber {
	return &subscriber{
		client:                client,
		subscription:          subscription,
		namespace:             namespace,
		namespaceBoundSecrets: namespaceBoundSecrets,
	}
}
