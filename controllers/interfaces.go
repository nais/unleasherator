package controllers

import (
	"context"

	"github.com/nais/unleasherator/pkg/unleash"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type UnleashInstance interface {
	IsReady() bool
	GetURL() string
	GetAdminToken(context.Context, client.Client, string) ([]byte, error)
	GetApiClient(context.Context, client.Client, string) (*unleash.Client, error)
}
