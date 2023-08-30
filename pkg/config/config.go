package config

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/pubsub"
	"github.com/kelseyhightower/envconfig"
	"github.com/nais/unleasherator/pkg/federation"
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
	ApiTokenNameSuffix string `envconfig:"API_TOKEN_NAME_SUFFIX"`
	OperatorNamespace  string `envconfig:"OPERATOR_NAMESPACE"`
	Federation         FederationConfig
	Timeout            TimeoutConfig
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
