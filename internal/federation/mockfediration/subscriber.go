package mockfederation

import (
	"context"

	"github.com/nais/unleasherator/internal/federation"
	"github.com/stretchr/testify/mock"
)

type MockSubscriber struct {
	mock.Mock
}

func (m *MockSubscriber) Subscribe(ctx context.Context, handler federation.Handler) error {
	args := m.Called(ctx, handler)
	return args.Error(0)
}

func (m *MockSubscriber) Close() error {
	args := m.Called()
	return args.Error(0)
}
