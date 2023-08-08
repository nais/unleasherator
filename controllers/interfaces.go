package controllers

import (
	"context"

	"github.com/nais/unleasherator/pkg/unleashclient"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type UnleashInstance interface {
	IsReady() bool
	URL() string
	AdminToken(context.Context, client.Client, string) ([]byte, error)
	ApiClient(context.Context, client.Client, string) (*unleashclient.Client, error)
}
