package utils

import (
	"math/rand"
	"time"
)

// RequeueAfterWithJitter returns a duration with random jitter applied.
// The returned value is in the range [base - jitter, base + jitter].
// This helps spread out reconciliations to avoid thundering herd problems.
func RequeueAfterWithJitter(base, jitter time.Duration) time.Duration {
	if jitter <= 0 {
		return base
	}
	// Generate random offset in range [-jitter, +jitter]
	offset := time.Duration(rand.Int63n(int64(2*jitter))) - jitter
	result := base + offset
	// Ensure we don't return a negative or zero duration
	if result <= 0 {
		return base
	}
	return result
}
