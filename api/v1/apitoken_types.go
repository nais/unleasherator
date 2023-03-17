package unleash_nais_io_v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApiTokenSpec defines the desired state of ApiToken
type ApiTokenSpec struct {
	// UnleashName is the name of the Unleash instance this token is for.
	UnleashName string `json:"unleashName"`

	// SecretName is the name of the secret where the token will be stored.
	SecretName string `json:"secretName"`

	// Type is the type of token to create. Valid values are "CLIENT" and "FRONTEND".
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=CLIENT;FRONTEND
	// +kubebuilder:default=CLIENT
	Type string `json:"type,omitempty"`

	// Environment is the environment to create the token for.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=default
	Environment string `json:"environment,omitempty"`

	// Projects is the list of projects to create the token for.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={"*"}
	Projects []string `json:"projects,omitempty"`
}

// ApiTokenStatus defines the observed state of ApiToken
type ApiTokenStatus struct {
	// Represents the observations of a ApiToken's current state.
	// Unleash.status.conditions.type are: "Created", "Creating", and "Failed"
	// Unleash.status.conditions.status are one of True, False, Unknown.
	// Unleash.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// Unleash.status.conditions.Message is a human readable message indicating details about the transition.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type ApiToken struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApiTokenSpec   `json:"spec,omitempty"`
	Status ApiTokenStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ApiTokenList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApiToken `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApiToken{}, &ApiTokenList{})
}