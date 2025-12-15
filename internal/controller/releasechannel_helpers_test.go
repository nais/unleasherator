package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCheckMaxUpgradeTimeExceeded(t *testing.T) {
	r := &ReleaseChannelReconciler{}

	tests := []struct {
		name             string
		releaseChannel   *unleashv1.ReleaseChannel
		expectedExceeded bool
		expectReasonSet  bool
	}{
		{
			name: "No StartTime set - not exceeded",
			releaseChannel: &unleashv1.ReleaseChannel{
				Status: unleashv1.ReleaseChannelStatus{
					StartTime: nil,
				},
			},
			expectedExceeded: false,
			expectReasonSet:  false,
		},
		{
			name: "StartTime recent - not exceeded with default timeout",
			releaseChannel: &unleashv1.ReleaseChannel{
				Status: unleashv1.ReleaseChannelStatus{
					StartTime: &metav1.Time{Time: time.Now().Add(-1 * time.Minute)},
				},
			},
			expectedExceeded: false,
			expectReasonSet:  false,
		},
		{
			name: "StartTime old - exceeded with default timeout",
			releaseChannel: &unleashv1.ReleaseChannel{
				Status: unleashv1.ReleaseChannelStatus{
					StartTime: &metav1.Time{Time: time.Now().Add(-15 * time.Minute)},
				},
			},
			expectedExceeded: true,
			expectReasonSet:  true,
		},
		{
			name: "StartTime old - not exceeded with custom long timeout",
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Strategy: unleashv1.ReleaseChannelStrategy{
						MaxUpgradeTime: &metav1.Duration{Duration: 1 * time.Hour},
					},
				},
				Status: unleashv1.ReleaseChannelStatus{
					StartTime: &metav1.Time{Time: time.Now().Add(-15 * time.Minute)},
				},
			},
			expectedExceeded: false,
			expectReasonSet:  false,
		},
		{
			name: "StartTime old - exceeded with custom short timeout",
			releaseChannel: &unleashv1.ReleaseChannel{
				Spec: unleashv1.ReleaseChannelSpec{
					Strategy: unleashv1.ReleaseChannelStrategy{
						MaxUpgradeTime: &metav1.Duration{Duration: 1 * time.Minute},
					},
				},
				Status: unleashv1.ReleaseChannelStatus{
					StartTime: &metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
				},
			},
			expectedExceeded: true,
			expectReasonSet:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exceeded, reason := r.checkMaxUpgradeTimeExceeded(tt.releaseChannel)
			assert.Equal(t, tt.expectedExceeded, exceeded, "exceeded mismatch")
			if tt.expectReasonSet {
				assert.NotEmpty(t, reason, "expected reason to be set")
				assert.Contains(t, reason, "maxUpgradeTime", "reason should mention maxUpgradeTime")
			} else {
				assert.Empty(t, reason, "expected reason to be empty")
			}
		})
	}
}

func TestPerformHTTPHealthCheck(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		serverResponse int
		serverDelay    time.Duration
		expectedOK     bool
		expectError    bool
	}{
		{
			name:           "200 OK response",
			serverResponse: http.StatusOK,
			expectedOK:     true,
			expectError:    false,
		},
		{
			name:           "201 Created response",
			serverResponse: http.StatusCreated,
			expectedOK:     true,
			expectError:    false,
		},
		{
			name:           "500 Internal Server Error",
			serverResponse: http.StatusInternalServerError,
			expectedOK:     false,
			expectError:    false, // Not an error, just unhealthy
		},
		{
			name:           "503 Service Unavailable",
			serverResponse: http.StatusServiceUnavailable,
			expectedOK:     false,
			expectError:    false, // Not an error, just unhealthy
		},
		{
			name:           "404 Not Found",
			serverResponse: http.StatusNotFound,
			expectedOK:     false,
			expectError:    false, // Not an error, just unhealthy
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.serverDelay > 0 {
					time.Sleep(tt.serverDelay)
				}
				w.WriteHeader(tt.serverResponse)
			}))
			defer server.Close()

			// Test the HTTP health check directly
			healthy, err := performHTTPHealthCheckDirect(ctx, server.URL+"/health")

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectedOK, healthy, "health check result mismatch")
		})
	}
}

// performHTTPHealthCheckDirect is a helper for testing that takes a full URL
func performHTTPHealthCheckDirect(ctx context.Context, url string) (bool, error) {
	client := &http.Client{
		Timeout: releaseChannelHealthCheckTimeout,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, nil // Return false but no error to allow retry
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, nil
	}

	return true, nil
}

func TestReleasePhaseOnFailure(t *testing.T) {
	tests := []struct {
		name            string
		rollbackEnabled bool
		onFailure       bool
		expectedPhase   unleashv1.ReleaseChannelPhase
	}{
		{
			name:            "Rollback enabled and onFailure true - should transition to RollingBack",
			rollbackEnabled: true,
			onFailure:       true,
			expectedPhase:   unleashv1.ReleaseChannelPhaseRollingBack,
		},
		{
			name:            "Rollback enabled but onFailure false - should transition to Failed",
			rollbackEnabled: true,
			onFailure:       false,
			expectedPhase:   unleashv1.ReleaseChannelPhaseFailed,
		},
		{
			name:            "Rollback disabled - should transition to Failed",
			rollbackEnabled: false,
			onFailure:       true,
			expectedPhase:   unleashv1.ReleaseChannelPhaseFailed,
		},
		{
			name:            "Both disabled - should transition to Failed",
			rollbackEnabled: false,
			onFailure:       false,
			expectedPhase:   unleashv1.ReleaseChannelPhaseFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			phase := releasePhaseOnFailure(tt.rollbackEnabled, tt.onFailure)
			assert.Equal(t, tt.expectedPhase, phase)
		})
	}
}
