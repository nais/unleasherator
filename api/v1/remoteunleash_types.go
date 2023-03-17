package unleash_nais_io_v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&RemoteUnleash{}, &RemoteUnleashList{})
}

// RemoteUnleashList contains a list of RemoteUnleash
// +kubebuilder:object:root=true
type RemoteUnleashList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RemoteUnleash `json:"items"`
}

// RemoteUnleash defines an RemoteUnleash instance
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type RemoteUnleash struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RemoteUnleashSpec   `json:"spec,omitempty"`
	Status RemoteUnleashStatus `json:"status,omitempty"`
}

// RemoteUnleashSpec defines the desired state of RemoteUnleash
type RemoteUnleashSpec struct {
	SecretName string `json:"secretName"`
	URL        string `json:"url"`
}

// RemoteUnleashStatus defines the observed state of RemoteUnleash
type RemoteUnleashStatus struct {
	// Represents the observations of a RemoteUnleash's current state.
	// RemoteUnleash.status.conditions.type are: "Available", "Progressing", and "Degraded"
	// RemoteUnleash.status.conditions.status are one of True, False, Unknown.
	// RemoteUnleash.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// RemoteUnleash.status.conditions.Message is a human readable message indicating details about the transition.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}
