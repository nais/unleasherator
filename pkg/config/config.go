package config

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub"
	"github.com/kelseyhightower/envconfig"
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

func (c *Config) PubsubSubscriber(ctx context.Context) (*pubsub.Client, error) {
	return c.pubsubClient(ctx, FederationModeSubscribe)
}

func (c *Config) PubsubPublisher(ctx context.Context) (*pubsub.Client, error) {
	return c.pubsubClient(ctx, FederationModePublish)
}

func (c *Config) pubsubClient(ctx context.Context, target FederationMode) (*pubsub.Client, error) {
	if target != c.Federation.Mode {
		return nil, nil
	}
	return pubsub.NewClient(ctx, c.Federation.PubsubProjectID)
}

func (c *Config) PubsubSubscription(ctx context.Context, client *pubsub.Client) (*pubsub.Subscription, error) {
	if client == nil {
		return nil, nil
	}
	return client.CreateSubscription(
		ctx,
		c.Federation.PubsubSubscriptionID,
		pubsub.SubscriptionConfig{
			Topic:                 client.Topic(c.Federation.PubsubTopic),
			EnableMessageOrdering: true,
		},
	)
}
