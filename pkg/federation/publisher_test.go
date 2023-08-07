package federation

import (
	"context"
	"fmt"
	"testing"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/pubsub/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestPublisherPublish(t *testing.T) {
	ctx := context.Background()
	// Start a fake server running locally.
	srv := pstest.NewServer()
	defer srv.Close()

	// Connect to the server without using TLS.
	conn, err := grpc.Dial(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatal("Fatal", err)
	}
	defer conn.Close()

	// Use the connection when creating a pubsub client.
	client, err := pubsub.NewClient(ctx, "project", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatal("Fatal", err)
	}
	defer client.Close()

	topic, err := client.CreateTopic(ctx, "test-topic")
	if err != nil {
		t.Fatal("Fatal", err)
	}

	topic.EnableMessageOrdering = true

	sub, err := client.CreateSubscription(ctx, "test-sub", pubsub.SubscriptionConfig{
		Topic:                 topic,
		EnableMessageOrdering: true,
	})
	if err != nil {
		t.Fatal("Fatal", err)
	}

	fmt.Println("Publishing message")
	res := topic.Publish(ctx, &pubsub.Message{
		Data:        []byte("test"),
		OrderingKey: "test",
	})
	_, err = res.Get(ctx)
	if err != nil {
		t.Fatal("Fatal", err)
	}

	done := false
	recieved := make(chan bool)

	fmt.Println("Receiving message")
	go func() {
		fmt.Println("Starting subscriber, listening for messages")
		err = sub.Receive(ctx, func(ctx context.Context, msg *pubsub.Message) {
			fmt.Printf("Got message: %q\n", string(msg.Data))
			msg.Ack()
			recieved <- true
		})

		if err != nil && !done {
			fmt.Printf("Error: %v\n", err)
			t.Error("Error", err)
		}
	}()

	fmt.Println("Waiting for message")
	<-recieved

	fmt.Print("Done")
	done = true
}
