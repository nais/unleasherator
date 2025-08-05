package unleash_nais_io_v1

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// ReleaseChannelSpec defines the desired state of ReleaseChannel
type ReleaseChannelSpec struct {
	// Image is the Docker image to deploy for the release channel.
	// +kubebuilder:validation:Required
	Image UnleashImage `json:"image,omitempty"`

	// Strategy defines the deployment strategy.
	Strategy ReleaseChannelStrategy `json:"strategy,omitempty"`

	// HealthChecks defines health validation configuration
	HealthChecks HealthCheckConfig `json:"healthChecks,omitempty"`

	// Rollback defines rollback configuration
	Rollback RollbackConfig `json:"rollback,omitempty"`
}

type ReleaseChannelStrategy struct {
	// Canary defines the canary strategy.
	Canary ReleaseChannelCanary `json:"canary,omitempty"`

	// MaxParallel defines the maximum number of instances to deploy in parallel.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	MaxParallel int `json:"maxParallel,omitempty"`

	// BatchInterval defines wait time between deployment batches
	// +kubebuilder:default="30s"
	BatchInterval *metav1.Duration `json:"batchInterval,omitempty"`

	// MaxUpgradeTime defines maximum time to wait for all upgrades to complete
	// +kubebuilder:default="10m"
	MaxUpgradeTime *metav1.Duration `json:"maxUpgradeTime,omitempty"`
}

type HealthCheckConfig struct {
	// Enabled determines if health checks are performed after deployment
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// InitialDelay is the wait time after deployment before starting health checks
	// +kubebuilder:default="30s"
	InitialDelay *metav1.Duration `json:"initialDelay,omitempty"`

	// Timeout is the maximum time to wait for health check to pass
	// +kubebuilder:default="5m"
	Timeout *metav1.Duration `json:"timeout,omitempty"`

	// Endpoint is a custom health check endpoint (optional)
	Endpoint string `json:"endpoint,omitempty"`
}

type RollbackConfig struct {
	// Enabled determines if automatic rollback is enabled on failure
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// PreviousImage is the image to rollback to (auto-populated)
	PreviousImage string `json:"previousImage,omitempty"`

	// OnFailure determines if rollback should trigger on deployment failure
	// +kubebuilder:default=true
	OnFailure bool `json:"onFailure,omitempty"`
}

type ReleaseChannelCanary struct {
	// Enabled defines if canary is enabled.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// LabelSelector is the label selector for the canary instances.
	// Unleash instances matching this selector will be considered canary instances.
	LabelSelector metav1.LabelSelector `json:"podSelector,omitempty"`
}

// ReleaseChannelStatus defines the observed state of ReleaseChannel
type ReleaseChannelStatus struct {
	// Conditions is a list of conditions for the release channel.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase represents the current phase of the rollout
	Phase ReleaseChannelPhase `json:"phase,omitempty"`

	// Version is the version of the release channel.
	// +kubebuilder:default="unknown"
	Version string `json:"version,omitempty"`

	// Rollout is true if the release channel has completed rollout successfully.
	// +kubebuilder:default=false
	Rollout bool `json:"completed,omitempty"`

	// Progress represents rollout progress as a percentage (0-100)
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Progress int `json:"progress,omitempty"`

	// Instances is the number of instances for the release channel.
	// +kubebuilder:default=0
	Instances int `json:"instances,omitempty"`

	// CanaryInstances is the number of canary instances for the release channel.
	// +kubebuilder:default=0
	CanaryInstances int `json:"canaryInstances,omitempty"`

	// InstancesUpToDate is the number of instances that are up to date.
	// +kubebuilder:default=0
	InstancesUpToDate int `json:"instancesUpToDate,omitempty"`

	// CanaryInstancesUpToDate is the number of canary instances that are up to date.
	// +kubebuilder:default=0
	CanaryInstancesUpToDate int `json:"canaryInstancesUpToDate,omitempty"`

	// LastReconcileTime is the last time the release channel was reconciled.
	LastReconcileTime *metav1.Time `json:"lastReconcileTime,omitempty"`

	// StartTime is when the current rollout started
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EstimatedCompletion is the estimated completion time
	EstimatedCompletion *metav1.Time `json:"estimatedCompletion,omitempty"`

	// InstanceStatus provides detailed status for each instance
	InstanceStatus []InstanceStatus `json:"instanceStatus,omitempty"`

	// FailureReason contains the reason for rollout failure
	FailureReason string `json:"failureReason,omitempty"`
}

// ReleaseChannelPhase represents the current phase of rollout
type ReleaseChannelPhase string

const (
	ReleaseChannelPhaseIdle        ReleaseChannelPhase = "Idle"
	ReleaseChannelPhaseValidating  ReleaseChannelPhase = "Validating"
	ReleaseChannelPhaseCanary      ReleaseChannelPhase = "Canary"
	ReleaseChannelPhaseRolling     ReleaseChannelPhase = "Rolling"
	ReleaseChannelPhaseCompleted   ReleaseChannelPhase = "Completed"
	ReleaseChannelPhaseFailed      ReleaseChannelPhase = "Failed"
	ReleaseChannelPhaseRollingBack ReleaseChannelPhase = "RollingBack"
)

// InstanceStatus provides detailed status for individual instances
type InstanceStatus struct {
	// Name of the Unleash instance
	Name string `json:"name,omitempty"`

	// Phase of this instance's upgrade
	Phase string `json:"phase,omitempty"`

	// StartTime when upgrade started for this instance
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// EndTime when upgrade completed for this instance
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// Message with additional details
	Message string `json:"message,omitempty"`

	// Ready indicates if instance is ready after upgrade
	Ready bool `json:"ready,omitempty"`
}

type ReleaseChannelCondition struct {
	// Type is the type of the condition.
	Type string `json:"type,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.status.version`
// +kubebuilder:printcolumn:name="Instances",type=integer,JSONPath=`.status.instances`
// +kubebuilder:printcolumn:name="InstancesUpToDate",type=integer,JSONPath=`.status.instancesUpToDate`
// +kubebuilder:printcolumn:name="Canaries",type=integer,JSONPath=`.status.canaryInstances`
// +kubebuilder:printcolumn:name="CanariesUpToDate",type=integer,JSONPath=`.status.canaryInstancesUpToDate`

// ReleaseChannel is the Schema for the releasechannels API
type ReleaseChannel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReleaseChannelSpec   `json:"spec,omitempty"`
	Status ReleaseChannelStatus `json:"status,omitempty"`
}

// IsCandidate checks if the release channel is a candidate for the given Unleash instance.
func (rc *ReleaseChannel) IsCandidate(u *Unleash) bool {
	return rc.Name == u.Spec.ReleaseChannel.Name && rc.Namespace == u.Namespace
}

// ShouldUpdate checks if the release channel should update the given Unleash instance.
func (rc *ReleaseChannel) ShouldUpdate(u *Unleash) bool {
	return rc.IsCandidate(u) && rc.Spec.Image != UnleashImage(u.Spec.CustomImage)
}

// ValidateCreate validates ReleaseChannel on creation
func (rc *ReleaseChannel) ValidateCreate() error {
	return rc.validate()
}

// ValidateUpdate validates ReleaseChannel on update
func (rc *ReleaseChannel) ValidateUpdate(old runtime.Object) error {
	oldRC := old.(*ReleaseChannel)

	// Prevent dangerous changes during active rollout
	if oldRC.Status.Phase != ReleaseChannelPhaseIdle &&
		oldRC.Status.Phase != ReleaseChannelPhaseCompleted &&
		oldRC.Status.Phase != ReleaseChannelPhaseFailed &&
		rc.Spec.Image != oldRC.Spec.Image {
		return fmt.Errorf("cannot change image during active rollout (current phase: %s)", oldRC.Status.Phase)
	}

	return rc.validate()
}

// ValidateDelete validates ReleaseChannel on deletion
func (rc *ReleaseChannel) ValidateDelete() error {
	// Allow deletion but warn about in-progress rollouts
	if rc.Status.Phase != ReleaseChannelPhaseIdle &&
		rc.Status.Phase != ReleaseChannelPhaseCompleted &&
		rc.Status.Phase != ReleaseChannelPhaseFailed {
		// This is just a warning - deletion is still allowed
		return fmt.Errorf("warning: deleting ReleaseChannel with active rollout (phase: %s)", rc.Status.Phase)
	}
	return nil
}

// validate performs common validation
func (rc *ReleaseChannel) validate() error {
	// Validate image format (basic validation)
	if string(rc.Spec.Image) == "" {
		return fmt.Errorf("image cannot be empty")
	}

	// Validate maxParallel bounds
	if rc.Spec.Strategy.MaxParallel < 1 || rc.Spec.Strategy.MaxParallel > 50 {
		return fmt.Errorf("maxParallel must be between 1 and 50")
	}

	// Validate canary configuration
	if rc.Spec.Strategy.Canary.Enabled {
		if rc.Spec.Strategy.Canary.LabelSelector.MatchLabels == nil &&
			len(rc.Spec.Strategy.Canary.LabelSelector.MatchExpressions) == 0 {
			return fmt.Errorf("canary label selector must be specified when canary is enabled")
		}
	}

	return nil
}

// NamespacedName returns the namespaced name of the release channel resource.
func (rc *ReleaseChannel) NamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: rc.Namespace,
		Name:      rc.Name,
	}
}

//+kubebuilder:object:root=true

// ReleaseChannelList contains a list of ReleaseChannel
type ReleaseChannelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReleaseChannel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ReleaseChannel{}, &ReleaseChannelList{})
}
