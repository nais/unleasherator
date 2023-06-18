package unleash_nais_io_v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ApiTokenUnleashInstance defines the Unleash instance this token is for.
type ApiTokenUnleashInstance struct {
	// ApiVersion is the API version of the Unleash instance.
	// +kubebuilder:validation:Required
	// +kubebuilder:default=unleash.nais.io/v1
	ApiVersion string `json:"apiVersion"`

	// Kind is the API kind of the Unleash instance.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Unleash;RemoteUnleash
	Kind string `json:"kind"`

	// Name is the name of the Unleash instance.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// ApiTokenSpec defines the desired state of ApiToken
type ApiTokenSpec struct {
	// UnleashInstance is the Unleash instance this token is for.
	// +kubebuilder:validation:Required
	UnleashInstance ApiTokenUnleashInstance `json:"unleashInstance"`

	// SecretName is the name of the secret where the token will be stored.
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName"`

	// Type is the type of token to create. Valid values are "CLIENT" and "FRONTEND".
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=CLIENT;FRONTEND
	// +kubebuilder:default=CLIENT
	Type string `json:"type,omitempty"`

	// Environment is the environment to create the token for.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=development
	Environment string `json:"environment,omitempty"`

	// Projects is the list of projects to create the token for.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default={"*"}
	Projects []string `json:"projects,omitempty"`
}

// ApiTokenStatus defines the observed state of ApiToken
type ApiTokenStatus struct {
	// Represents the observations of a ApiToken's current state.
	// Unleash.status.conditions.type are: "Created", and "Failed"
	// Unleash.status.conditions.status are one of True, False, Unknown.
	// Unleash.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// Unleash.status.conditions.Message is a human readable message indicating details about the transition.
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Created is true when the Unleash API token has been created successfully
	// in the Unleash instance.
	// This is used for kubectl printing purposes. Rather than relying on this
	// value, check the conditions instead.
	// +kubebuilder:default=false
	Created bool `json:"created,omitempty"`

	// Failed is true when the Unleash API token reconcile has failed
	// This is used for kubectl printing purposes. Rather than relying on this
	// value, check the conditions instead.
	// +kubebuilder:default=false
	Failed bool `json:"failed,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Created",type=boolean,JSONPath=`.status.created`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.spec.secretName`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Failed",type=boolean,JSONPath=`.status.failed`
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

// UnleashClientName returns the name of the Unleash client for this token.
// The name is the same as the token name, but with a suffix. This is to avoid
// name collisions between multiple Unleasherator instances operating on the same
// Unleash instance.
func (t *ApiToken) UnleashClientName(suffix string) string {
	return t.Name + "-" + suffix
}

func (t *ApiToken) NamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: t.Namespace,
		Name:      t.Name,
	}
}

func init() {
	SchemeBuilder.Register(&ApiToken{}, &ApiTokenList{})
}
