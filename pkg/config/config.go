package config

import (
	"context"
	"fmt"

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
}

type FederationConfig struct {
	Enabled              bool           `envconfig:"FEDERATION_ENABLED"`
	Mode                 FederationMode `envconfig:"FEDERATION_PUBSUB_MODE"`
	PubsubProjectID      string         `envconfig:"FEDERATION_PUBSUB_GCP_PROJECT_ID"`
	PubsubTopic          string         `envconfig:"FEDERATION_PUBSUB_TOPIC"`
	PubsubSubscriptionID string         `envconfig:"FEDERATION_PUBSUB_SUBSCRIPTION_ID"`
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

	subscription, err := c.pubsubSubscription(ctx, client)
	if err != nil {
		return nil, err
	}

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
	return client.Topic(c.Federation.PubsubTopic)
}

func (c *Config) pubsubSubscription(ctx context.Context, client *pubsub.Client) (*pubsub.Subscription, error) {
	topic := c.pubsubTopic(client)

	return client.CreateSubscription(
		ctx,
		c.Federation.PubsubSubscriptionID,
		pubsub.SubscriptionConfig{
			Topic:                 topic,
			EnableMessageOrdering: true,
		},
	)
}
