package statemachine

import (
	"time"
)

// InstancePhase represents the phase of an instance's rollout lifecycle
type InstancePhase string

const (
	// Initial states
	InstancePhaseUnknown InstancePhase = "Unknown" // No conditions set yet
	InstancePhasePending InstancePhase = "Pending" // Waiting to trigger deployment

	// Preparation states
	InstancePhaseValidating InstancePhase = "Validating" // Validating spec/configuration
	InstancePhaseResolving  InstancePhase = "Resolving"  // Resolving image from release channel

	// Deployment states
	InstancePhaseTriggered   InstancePhase = "Triggered"   // Deployment triggered
	InstancePhaseReconciling InstancePhase = "Reconciling" // K8s resources being created/updated
	InstancePhaseDeploying   InstancePhase = "Deploying"   // Deployment rollout in progress
	InstancePhaseConnecting  InstancePhase = "Connecting"  // Testing API connection
	InstancePhaseHealthCheck InstancePhase = "HealthCheck" // Running health checks

	// Canary/Rollout coordination states
	InstancePhaseCanaryWait InstancePhase = "CanaryWait" // Waiting for canary validation
	InstancePhaseBlocked    InstancePhase = "Blocked"    // Blocked by policy or other instance

	// Success states
	InstancePhaseReady      InstancePhase = "Ready"      // Fully operational (reconciled + connected)
	InstancePhaseFederating InstancePhase = "Federating" // Publishing to federation

	// Failure states
	InstancePhaseFailed   InstancePhase = "Failed"   // Explicit failure condition
	InstancePhaseDegraded InstancePhase = "Degraded" // Partial failure or performance issues
	InstancePhaseTimeout  InstancePhase = "Timeout"  // Deployment rollout timed out
	InstancePhaseStuck    InstancePhase = "Stuck"    // No progress for extended time

	// Rollback states
	InstancePhaseRollback InstancePhase = "Rollback" // Explicitly rolling back to previous version

	// Cleanup states
	InstancePhaseFinalizing InstancePhase = "Finalizing" // Being deleted (finalizer running)
)

// InstanceState represents the state of an instance in the rollout process
type InstanceState struct {
	Name               string
	CurrentImage       string
	TargetImage        string
	LastTargetImage    string
	LastTriggerTime    time.Time
	Phase              InstancePhase
	FailureCount       int
	LastFailureTime    time.Time
	IsCanary           bool
	IsExplicitlyFailed bool

	// Enhanced status information
	Version            string    // Current reported version
	IsDegraded         bool      // Has Degraded condition
	IsReady            bool      // All pods ready and healthy
	FailureReason      string    // Specific failure reason
	Generation         int64     // Resource generation for change detection
	LastTransitionTime time.Time // When status last changed

	// Health and validation state
	IsHealthy          bool     // Health checks passing
	HealthChecksFailed int      // Number of failed health checks
	ValidationErrors   []string // Configuration validation errors

	// Rollout coordination
	IsBlocked      bool   // Blocked by policy or dependencies
	BlockingReason string // Why the instance is blocked
	BatchIndex     int    // Which batch this instance belongs to
}

// TriggerDecision represents the decision of whether to trigger an instance
type TriggerDecision struct {
	ShouldTrigger     bool
	Reason            string
	NewPhase          InstancePhase
	WaitDuration      time.Duration
	BlockingInstances []string // Names of instances that are blocking this one
}

// DecisionConfig contains configuration for the decision engine
type DecisionConfig struct {
	MinTriggerInterval    time.Duration // Minimum time between triggers
	FailureRetryDelay     time.Duration // Delay before retrying after failure
	StuckDetectionTimeout time.Duration // Time before marking as stuck
	HealthCheckTimeout    time.Duration // Timeout for health checks
	CanaryWaitTimeout     time.Duration // Maximum time to wait for canary validation
	MaxFailureCount       int           // Maximum number of failures before giving up
}
