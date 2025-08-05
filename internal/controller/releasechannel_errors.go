package controller

import (
	"fmt"
	"time"
)

// ReleaseChannelError represents a structured error from ReleaseChannel operations
type ReleaseChannelError struct {
	Phase     string
	Instance  string
	Reason    string
	Retryable bool
	Inner     error
}

func (e *ReleaseChannelError) Error() string {
	if e.Instance != "" {
		return fmt.Sprintf("ReleaseChannel error in %s phase for instance %s: %s", e.Phase, e.Instance, e.Reason)
	}
	return fmt.Sprintf("ReleaseChannel error in %s phase: %s", e.Phase, e.Reason)
}

func (e *ReleaseChannelError) Unwrap() error {
	return e.Inner
}

// IsRetryable returns true if the error should trigger a retry
func (e *ReleaseChannelError) IsRetryable() bool {
	return e.Retryable
}

// NewReleaseChannelError creates a new structured error
func NewReleaseChannelError(phase, instance, reason string, retryable bool, inner error) *ReleaseChannelError {
	return &ReleaseChannelError{
		Phase:     phase,
		Instance:  instance,
		Reason:    reason,
		Retryable: retryable,
		Inner:     inner,
	}
}

// CircuitState represents the state of a circuit breaker
type CircuitState int

const (
	CircuitStateClosed CircuitState = iota
	CircuitStateOpen
	CircuitStateHalfOpen
)

// CircuitBreaker implements circuit breaker pattern for upgrade operations
type CircuitBreaker struct {
	FailureThreshold int
	SuccessThreshold int
	TimeoutDuration  time.Duration
	State            CircuitState
	FailureCount     int
	SuccessCount     int
	LastFailure      time.Time
}

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(failureThreshold, successThreshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		FailureThreshold: failureThreshold,
		SuccessThreshold: successThreshold,
		TimeoutDuration:  timeout,
		State:            CircuitStateClosed,
	}
}

// ShouldBlock returns true if the circuit breaker should block the operation
func (cb *CircuitBreaker) ShouldBlock() bool {
	switch cb.State {
	case CircuitStateOpen:
		// Check if timeout has passed to move to half-open
		if time.Since(cb.LastFailure) > cb.TimeoutDuration {
			cb.State = CircuitStateHalfOpen
			cb.SuccessCount = 0
			return false
		}
		return true
	case CircuitStateHalfOpen:
		// Allow limited operations in half-open state
		return false
	case CircuitStateClosed:
		return false
	default:
		return false
	}
}

// RecordResult records the result of an operation
func (cb *CircuitBreaker) RecordResult(success bool) {
	if success {
		cb.FailureCount = 0

		if cb.State == CircuitStateHalfOpen {
			cb.SuccessCount++
			if cb.SuccessCount >= cb.SuccessThreshold {
				cb.State = CircuitStateClosed
			}
		}
	} else {
		cb.SuccessCount = 0
		cb.FailureCount++
		cb.LastFailure = time.Now()

		if cb.FailureCount >= cb.FailureThreshold {
			cb.State = CircuitStateOpen
		}
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitState {
	return cb.State
}
