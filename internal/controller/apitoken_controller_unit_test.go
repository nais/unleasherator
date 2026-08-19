package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// A missing Unleash instance is the single most common ApiToken state in the
// fleet (teams apply ApiTokens before federation delivers the RemoteUnleash),
// so it must not be reported as a controller error and must not log on every
// reconcile. These tests pin all four halves of that: nil error, slow requeue,
// log-on-change-only, and an event plus gauge so it stays observable.
func TestReconcileMissingUnleashInstanceIsAState(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	token := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-token",
			Namespace:  "default",
			Finalizers: []string{tokenFinalizer},
		},
		Spec: unleashv1.ApiTokenSpec{
			UnleashInstance: unleashv1.ApiTokenUnleashInstance{
				Name:       "missing-remote",
				Kind:       "RemoteUnleash",
				ApiVersion: unleashv1.GroupVersion.String(),
			},
		},
		Status: unleashv1.ApiTokenStatus{
			Conditions: []metav1.Condition{{
				Type:               unleashv1.ApiTokenStatusConditionTypeCreated,
				Status:             metav1.ConditionUnknown,
				Reason:             "Reconciling",
				Message:            "Starting reconciliation",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(token).
		WithStatusSubresource(token).
		Build()

	recorder := record.NewFakeRecorder(10)
	reconciler := &ApiTokenReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: recorder,
		Tracer:   otel.Tracer("test"),
	}

	var logged []string
	logger := funcr.New(func(prefix, args string) {
		logged = append(logged, args)
	}, funcr.Options{})
	ctx := log.IntoContext(context.Background(), logger)

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-token", Namespace: "default"}}

	apiTokenWaitingForInstance.Reset()

	result, err := reconciler.Reconcile(ctx, req)
	require.NoError(t, err, "a missing user-managed dependency must not surface as a reconciler error")
	assert.Greater(t, result.RequeueAfter, time.Duration(0), "the ApiToken must keep polling so a late RemoteUnleash is still picked up")
	assert.Less(t, result.RequeueAfter, 30*time.Minute, "requeue must stay fast enough that a team is not left waiting")

	firstLogs := countLogsContaining(logged, "Unleash instance not found")
	assert.Equal(t, 1, firstLogs, "the first observation of the missing instance should be logged once")

	gaugeVal, err := promGaugeVecVal(apiTokenWaitingForInstance, "default", "test-token")
	require.NoError(t, err)
	assert.Equal(t, 1.0, gaugeVal, "the waiting gauge must be set while the instance is missing")

	// Status condition is still the surface teams see in `kubectl get apitoken`.
	updated := &unleashv1.ApiToken{}
	require.NoError(t, fakeClient.Get(ctx, req.NamespacedName, updated))
	failed := meta.FindStatusCondition(updated.Status.Conditions, unleashv1.ApiTokenStatusConditionTypeFailed)
	require.NotNil(t, failed, "the failed condition must still be recorded")
	assert.Equal(t, "UnleashNotFound", failed.Reason)
	assert.Equal(t, metav1.ConditionTrue, failed.Status)

	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, "UnleashNotFound", "an event must be emitted so the state shows in kubectl describe")
	default:
		t.Fatal("expected an event to be recorded for the missing Unleash instance")
	}

	// Second consecutive reconcile of the unchanged state: no new log line.
	logged = nil
	result, err = reconciler.Reconcile(ctx, req)
	require.NoError(t, err)
	assert.Greater(t, result.RequeueAfter, time.Duration(0))
	assert.Equal(t, 0, countLogsContaining(logged, "Unleash instance not found"),
		"an unchanged missing-instance state must not log again; this is the log volume the change exists to remove")
	assert.Equal(t, 0, countLogsContaining(logged, "not found in namespace"),
		"updateStatusFailed must not log the message again either")
}

// The gauge must fall back to 0 once the instance appears, otherwise an alert on
// it would fire forever for ApiTokens that recovered on their own.
func TestReconcileClearsWaitingGaugeWhenInstanceAppears(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	remoteUnleash := &unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{Name: "late-remote", Namespace: "default"},
	}
	token := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "late-token",
			Namespace:  "default",
			Finalizers: []string{tokenFinalizer},
		},
		Spec: unleashv1.ApiTokenSpec{
			UnleashInstance: unleashv1.ApiTokenUnleashInstance{
				Name:       "late-remote",
				Kind:       "RemoteUnleash",
				ApiVersion: unleashv1.GroupVersion.String(),
			},
		},
		Status: unleashv1.ApiTokenStatus{
			Conditions: []metav1.Condition{{
				Type:               unleashv1.ApiTokenStatusConditionTypeCreated,
				Status:             metav1.ConditionUnknown,
				Reason:             "Reconciling",
				Message:            "Starting reconciliation",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(token, remoteUnleash).
		WithStatusSubresource(token).
		Build()

	reconciler := &ApiTokenReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
		Tracer:   otel.Tracer("test"),
	}

	apiTokenWaitingForInstance.Reset()
	apiTokenWaitingForInstance.WithLabelValues("default", "late-token").Set(1.0)

	// The RemoteUnleash exists but is not ready, so the reconcile stops shortly
	// after the lookup — which is all this assertion needs.
	_, _ = reconciler.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "late-token", Namespace: "default"},
	})

	gaugeVal, err := promGaugeVecVal(apiTokenWaitingForInstance, "default", "late-token")
	require.NoError(t, err)
	assert.Equal(t, 0.0, gaugeVal, "the waiting gauge must clear once the instance exists")
}

func countLogsContaining(logs []string, substr string) int {
	count := 0
	for _, line := range logs {
		if strings.Contains(line, substr) {
			count++
		}
	}
	return count
}

// After an operator restart the gauge must be rebuilt from the stored condition,
// otherwise a "blocked for > N minutes" alert silently resets on every rollout.
func TestMetricsInitializerSeedsWaitingForInstanceGauge(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	blocked := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{Name: "blocked-token", Namespace: "default"},
		Status: unleashv1.ApiTokenStatus{
			Conditions: []metav1.Condition{{
				Type:               unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:             metav1.ConditionTrue,
				Reason:             "UnleashNotFound",
				Message:            "RemoteUnleash resource with name gone not found in namespace default",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}
	// Failing for an unrelated reason is not the same state and must not be
	// counted as waiting.
	otherFailure := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{Name: "other-token", Namespace: "default"},
		Status: unleashv1.ApiTokenStatus{
			Conditions: []metav1.Condition{{
				Type:               unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:             metav1.ConditionTrue,
				Reason:             "TokenCreationFailed",
				Message:            "boom",
				LastTransitionTime: metav1.Now(),
			}},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(blocked, otherFailure).
		Build()

	apiTokenWaitingForInstance.Reset()

	initializer := &MetricsInitializer{Client: fakeClient}
	require.NoError(t, initializer.initApiTokenMetrics(context.Background()))

	blockedVal, err := promGaugeVecVal(apiTokenWaitingForInstance, "default", "blocked-token")
	require.NoError(t, err)
	assert.Equal(t, 1.0, blockedVal, "an ApiToken stored as UnleashNotFound must come back as waiting")

	otherVal, err := promGaugeVecVal(apiTokenWaitingForInstance, "default", "other-token")
	require.NoError(t, err)
	assert.Equal(t, 0.0, otherVal, "an unrelated failure must not be reported as waiting for an instance")
}
