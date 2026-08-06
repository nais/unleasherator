package controller

import (
	"errors"
	"testing"

	"github.com/nais/unleasherator/internal/unleashclient"
)

func TestIsTerminalTokenCleanupError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		terminal bool
	}{
		{
			name:     "unauthorized is retried",
			err:      &unleashclient.UnleashAPIError{StatusCode: 401},
			terminal: false,
		},
		{
			name:     "forbidden is retried",
			err:      &unleashclient.UnleashAPIError{StatusCode: 403},
			terminal: false,
		},
		{
			name:     "not found is terminal",
			err:      &unleashclient.UnleashAPIError{StatusCode: 404},
			terminal: true,
		},
		{
			name:     "method not allowed is terminal",
			err:      &unleashclient.UnleashAPIError{StatusCode: 405},
			terminal: true,
		},
		{
			name:     "server error is retried",
			err:      &unleashclient.UnleashAPIError{StatusCode: 500},
			terminal: false,
		},
		{
			name:     "network error is retried",
			err:      errors.New("connection refused"),
			terminal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTerminalTokenCleanupError(tt.err); got != tt.terminal {
				t.Fatalf("isTerminalTokenCleanupError() = %t, want %t", got, tt.terminal)
			}
		})
	}
}
