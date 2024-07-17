package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ReleaseChannelSpec defines the desired state of ReleaseChannel
type ReleaseChannelSpec struct {
	// Image is the Docker image to deploy for the release channel.
	// +kubebuilder:validation:Required
	Image string `json:"image,omitempty"`

	// Strategy defines the deployment strategy.
	Strategy ReleaseChannelStrategy `json:"strategy,omitempty"`
}

type ReleaseChannelStrategy struct {
	// Canary defines the canary strategy.
	Canary ReleaseChannelCanary `json:"canary,omitempty"`

	// MaxParallel defines the maximum number of instances to deploy in parallel.
	// +kubebuilder:default=1
	MaxParallel int `json:"maxParallel,omitempty"`
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

	// Version is the version of the release channel.
	// +kubebuilder:default="unknown"
	Version string `json:"version,omitempty"`

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
