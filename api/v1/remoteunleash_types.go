package unleash_nais_io_v1

import (
	"context"

	"github.com/nais/unleasherator/pkg/unleash"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// Server is the Unleash instance this token is for.
	// +kubebuilder:validation:Required
	Server RemoteUnleashServer `json:"unleashInstance"`

	// Secret is the secret containing the Unleash instance's API token.
	// +kubebuilder:validation:Required
	AdminSecret RemoteUnleashSecret `json:"adminSecret"`
}

// RemoteUnleashSecret defines the secret containing the Unleash instance's API token.
type RemoteUnleashSecret struct {
	// Name is the name of the secret containing the Unleash instance's API token.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^unleasherator-.+$`
	Name string `json:"name"`

	// TokenKey is the key of the secret containing the Unleash instance's API token.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=token
	Key string `json:"key,omitempty"`

	// Namespace is the namespace of the secret containing the Unleash instance's API token.
	// +kubebuilder:validation:Optional
	Namespace string `json:"namespace,omitempty"`
}

func (u *RemoteUnleash) GetAdminSecretNamespacedName() types.NamespacedName {
	namespacedName := types.NamespacedName{
		Name:      u.Spec.AdminSecret.Name,
		Namespace: u.Spec.AdminSecret.Namespace,
	}

	if namespacedName.Namespace == "" {
		namespacedName.Namespace = u.Namespace
	}

	return namespacedName
}

// RemoteUnleashServer defines the Unleash instance this token is for.
type RemoteUnleashServer struct {
	// URL is the URL of the Unleash instance.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
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

func (s *RemoteUnleashStatus) IsReady() bool {
	for _, c := range s.Conditions {
		if c.Type == StatusConditionTypeAvailable && c.Status == metav1.ConditionTrue {
			return true
		}
	}

	return false
}

func (u *RemoteUnleash) GetURL() string {
	return u.Spec.Server.URL
}

func (u *RemoteUnleash) GetAdminToken(ctx context.Context, client client.Client, operatorNamespace string) ([]byte, error) {
	// operatorNamespace is not used, and is only here to satisfy the interface of UnleashInstance.
	_ = operatorNamespace

	secret := &v1.Secret{}
	if err := client.Get(ctx, u.GetAdminSecretNamespacedName(), secret); err != nil {
		return nil, err
	}

	return secret.Data[u.Spec.AdminSecret.Key], nil
}

func (u *RemoteUnleash) GetApiClient(ctx context.Context, client client.Client, operatorNamespace string) (*unleash.Client, error) {
	token, err := u.GetAdminToken(ctx, client, operatorNamespace)
	if err != nil {
		return nil, err
	}

	return unleash.NewClient(u.GetURL(), string(token))
}

func (u *RemoteUnleash) IsReady() bool {
	return u.Status.IsReady()
}
