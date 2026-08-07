package controller

import (
	"context"
	"errors"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCleanupTokenInUnleashRetriesWhenInstanceNotReady(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	remoteUnleash := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "test-remote", Namespace: "default"},
	}
	token := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{Name: "test-token", Namespace: "default"},
		Spec: unleashv1.ApiTokenSpec{
			UnleashInstance: unleashv1.ApiTokenUnleashInstance{
				Name:       remoteUnleash.Name,
				Kind:       "RemoteUnleash",
				ApiVersion: "unleash.nais.io/v1",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(remoteUnleash).
		Build()
	reconciler := &ApiTokenReconciler{Client: fakeClient, Scheme: scheme}

	err := reconciler.cleanupTokenInUnleash(context.Background(), token, ctrl.Log.WithName("test"))
	require.Error(t, err, "not-ready instance must retain the finalizer instead of silently orphaning the token")
	assert.Contains(t, err.Error(), "not ready")
}

func TestCleanupTokenInUnleashSkipsWhenInstanceIsAbsent(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	token := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{Name: "test-token", Namespace: "default"},
		Spec: unleashv1.ApiTokenSpec{
			UnleashInstance: unleashv1.ApiTokenUnleashInstance{
				Name:       "missing-remote",
				Kind:       "RemoteUnleash",
				ApiVersion: "unleash.nais.io/v1",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := &ApiTokenReconciler{Client: fakeClient, Scheme: scheme}

	require.NoError(t, reconciler.cleanupTokenInUnleash(context.Background(), token, ctrl.Log.WithName("test")))
}

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
