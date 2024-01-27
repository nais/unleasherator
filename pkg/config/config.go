package config

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/kelseyhightower/envconfig"
	"github.com/nais/unleasherator/pkg/federation"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	envVarPrefix            = ""
	FederationModeDisabled  = ""
	FederationModePublish   = "publish"
	FederationModeSubscribe = "subscribe"
)

type FederationMode string

func (p *FederationMode) Set(value string) error {
	switch value {
	case FederationModeDisabled:
	case FederationModePublish:
	case FederationModeSubscribe:
	default:
		return fmt.Errorf("unsupported federation mode %q", value)
	}
	*p = FederationMode(value)
	return nil
}

type Config struct {
	ApiTokenNameSuffix         string `envconfig:"API_TOKEN_NAME_SUFFIX"`
	Federation                 FederationConfig
	HealthProbeBindAddress     string `envconfig:"HEALTH_PROBE_BIND_ADDRESS" default:":8081"`
	LeaderElectionEnabled      bool   `envconfig:"LEADER_ELECTION_ENABLED" default:"true"`
	LeaderElectionResourceName string `envconfig:"LEADER_ELECTION_RESOURCE_NAME" default:"509984d3.nais.io"`
	MetricsBindAddress         string `envconfig:"METRICS_BIND_ADDRESS" default:"127.0.0.1:8080"`
	OperatorNamespace          string `envconfig:"OPERATOR_NAMESPACE"`
	Timeout                    TimeoutConfig
	WebhookPort                int `envconfig:"WEBHOOK_PORT" default:"9443"`
	Features                   Features
	OpenTelemetry              OpenTelemetryConfig
}

type OpenTelemetryConfig struct {
	TracesExporter        string `envconfig:"OTEL_TRACES_EXPORTER" default:"stdout"`
	ExporterOtelpEndpoint string `envconfig:"OTEL_EXPORTER_OTLP_ENDPOINT" default:"http://tempo-distributor.nais-system:4317"`
	ExporterOtelpProtocol string `envconfig:"OTEL_EXPORTER_OTLP_PROTOCOL" default:"grpc"`
}

type Features struct {
	// UnleashTokenUpdate enables updating tokens in Unleash.
	ApiTokenUpdateEnabled bool `envconfig:"FEATURE_API_TOKEN_UPDATE_ENABLED" default:"false"`
}

func (c *Config) ManagerOptions(scheme *runtime.Scheme) manager.Options {
	return manager.Options{
		Scheme:                  scheme,
		LeaderElection:          c.LeaderElectionEnabled,
		LeaderElectionNamespace: c.OperatorNamespace,
		LeaderElectionID:        c.LeaderElectionResourceName,
		Metrics: server.Options{
			BindAddress: c.MetricsBindAddress,
		},
		HealthProbeBindAddress: c.HealthProbeBindAddress,
	}
}

type TimeoutConfig struct {
	// WriteSeconds is the maximum number of seconds to wait for a write operation to complete.
	Write time.Duration `envconfig:"TIMEOUT_WRITE" default:"10s"`
}

func (t *TimeoutConfig) WriteContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, t.Write)
}

type FederationConfig struct {
	ClusterName        string         `envconfig:"FEDERATION_CLUSTER_NAME"`
	Mode               FederationMode `envconfig:"FEDERATION_PUBSUB_MODE"`
	PubsubProjectID    string         `envconfig:"FEDERATION_PUBSUB_GCP_PROJECT_ID"`
	PubsubTopic        string         `envconfig:"FEDERATION_PUBSUB_TOPIC"`
	PubsubSubscription string         `envconfig:"FEDERATION_PUBSUB_SUBSCRIPTION"`
}

func (f *FederationConfig) IsEnabled() bool {
	return f.Mode != FederationModeDisabled
}

func LoadFromEnv() (*Config, error) {
	cfg := &Config{}
	err := envconfig.Process(envVarPrefix, cfg)
	return cfg, err
}

func (c *Config) PubsubSubscriber(ctx context.Context) (federation.Subscriber, error) {
	if c.Federation.Mode != FederationModeSubscribe {
		return nil, nil
	}

	client, err := c.pubsubClient(ctx)
	if err != nil {
		return nil, err
	}

	subscription := c.pubsubSubscription(ctx, client)

	return federation.NewSubscriber(client, subscription, c.OperatorNamespace), nil
}

func (c *Config) PubsubPublisher(ctx context.Context) (federation.Publisher, error) {
	if c.Federation.Mode != FederationModePublish {
		return nil, nil
	}

	client, err := c.pubsubClient(ctx)
	if err != nil {
		return nil, err
	}

	topic := c.pubsubTopic(client)

	return federation.NewPublisher(client, topic), nil
}

func (c *Config) pubsubClient(ctx context.Context) (*pubsub.Client, error) {
	return pubsub.NewClient(ctx, c.Federation.PubsubProjectID)
}

func (c *Config) pubsubTopic(client *pubsub.Client) *pubsub.Topic {
	topic := client.Topic(c.Federation.PubsubTopic)
	topic.EnableMessageOrdering = true

	return topic
}

func (c *Config) pubsubSubscription(ctx context.Context, client *pubsub.Client) *pubsub.Subscription {
	return client.Subscription(c.Federation.PubsubSubscription)
}
