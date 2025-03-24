package federation

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newPubSub(ctx context.Context, namePrefix string) (srv *pstest.Server, conn *grpc.ClientConn, c *pubsub.Client, topic *pubsub.Topic, sub *pubsub.Subscription, err error) {
	projectName := fmt.Sprintf("%s-project", namePrefix)
	topicName := fmt.Sprintf("%s-topic", namePrefix)
	subName := fmt.Sprintf("%s-sub", namePrefix)

	// Start a fake server running locally.
	srv = pstest.NewServer()

	// Connect to the server without using TLS.
	conn, err = grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return
	}

	// Use the connection when creating a pubsub client.
	c, err = pubsub.NewClient(ctx, projectName, option.WithGRPCConn(conn))
	if err != nil {
		return
	}

	// Create a new topic.
	topic, err = c.CreateTopic(ctx, topicName)
	if err != nil {
		return
	}

	// Enable message ordering.
	topic.EnableMessageOrdering = true

	// Create a new subscription.
	sub, err = c.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{
		EnableMessageOrdering: true,
		Topic:                 topic,
	})
	if err != nil {
		return
	}

	return
}
