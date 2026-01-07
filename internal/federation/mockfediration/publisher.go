package mockfederation

import (
	"context"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/mock"
)

type MockPublisher struct {
	mock.Mock
}

func (m *MockPublisher) Publish(ctx context.Context, unleash *unleashv1.Unleash, apiToken string) error {
	args := m.Called(ctx, unleash, apiToken)
	return args.Error(0)
}

func (m *MockPublisher) PublishRemoved(ctx context.Context, unleash *unleashv1.Unleash) error {
	args := m.Called(ctx, unleash)
	return args.Error(0)
}

func (m *MockPublisher) Close() error {
	args := m.Called()
	return args.Error(0)
}
