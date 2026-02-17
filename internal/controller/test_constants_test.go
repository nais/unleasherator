package controller

import "time"

// Standardized test timeouts to reduce flakiness.
// All test files should use these constants instead of defining their own.
const (
	// TestTimeoutShort is for simple operations that should complete quickly
	// (single resource creation, status update, etc.)
	TestTimeoutShort = time.Second * 2

	// TestTimeoutMedium is for operations requiring multiple reconciliation cycles
	// (resource creation + connection test, status propagation, etc.)
	TestTimeoutMedium = time.Second * 5

	// TestTimeoutLong is for complex multi-resource coordination scenarios
	// (ReleaseChannel rollouts, multi-instance synchronization, etc.)
	TestTimeoutLong = time.Second * 15

	// TestTimeoutCoordination is for operations involving multiple controllers
	// or external dependencies that may be slow in CI environments
	TestTimeoutCoordination = time.Second * 30

	// TestInterval is the polling interval for Eventually/Consistently assertions.
	// Keep this short to detect state changes quickly.
	TestInterval = time.Millisecond * 20

	// TestStableDuration is how long a resource should remain unchanged
	// before considering it "stable" (no more pending reconciliations)
	TestStableDuration = time.Millisecond * 200
)
