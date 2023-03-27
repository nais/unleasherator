package unleash_nais_io_v1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func init() {
	SchemeBuilder.Register(&Unleash{}, &UnleashList{})
}

// UnleashList contains a list of Unleash
// +kubebuilder:object:root=true
type UnleashList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Unleash `json:"items"`
}

// Unleash defines an Unleash instance
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Unleash struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UnleashSpec   `json:"spec,omitempty"`
	Status UnleashStatus `json:"status,omitempty"`
}

// UnleashSpec defines the desired state of Unleash
type UnleashSpec struct {
	// Size is the size of the unleash deployment
	Size int32 `json:"size,omitempty"`

	// Database is the database configuration
	// +kubebuilder:validation:Required
	Database DatabaseConfig `json:"database,omitempty"`

	// WebIngress defines the ingress configuration for the web interface
	// +kubebuilder:validation:Optional
	WebIngress []IngressConfig `json:"webIngress,omitempty"`

	// ApiIngress defines the ingress for the endpoints of Unleash
	// +kubebuilder:validation:Optional
	ApiIngress []IngressConfig `json:"apiIngress,omitempty"`

	// ExtraEnv is a list of extra environment variables to add to the deployment
	// +kubebuilder:validation:Optional
	ExtraEnvVars []corev1.EnvVar `json:"extraEnvVars,omitempty"`

	// ExtraVolumeMounts is a list of extra volume mounts to add to the deployment
	// +kubebuilder:validation:Optional
	ExtraVolumes []corev1.Volume `json:"extraVolumes,omitempty"`

	// ExtraVolumeMounts is a list of extra volume mounts to add to the deployment
	// +kubebuilder:validation:Optional
	ExtraVolumeMounts []corev1.VolumeMount `json:"extraVolumeMounts,omitempty"`

	// ExtraContainers is a list of extra containers to add to the deployment
	// +kubebuilder:validation:Optional
	ExtraContainers []corev1.Container `json:"extraContainers,omitempty"`

	// ExistingServiceAccountName is the name of an already existing Kubernetes service account
	// +kubebuilder:validation:Optional
	ExistingServiceAccountName string `json:"existingServiceAccountName,omitempty"`
}

// IngressConfig defines the ingress configuration
type IngressConfig struct {
	// Enable enables the ingress
	// +kubebuilder:default=false
	Enable bool `json:"enable,omitempty"`
	// Host is the hostname to use for the ingress
	Host string `json:"host,omitempty"`

	// Path is the path to use for the ingress
	Path string `json:"path,omitempty"`

	// TLS is the TLS configuration to use for the ingress
	TLS *IngressTLSConfig `json:"tls,omitempty"`

	// Annotations is a map of annotations to add to the ingress
	Annotations map[string]string `json:"annotations,omitempty"`

	// Class is the ingress class to use for the ingress
	Class string `json:"class,omitempty`
}

// IngressTLSConfig defines the TLS configuration for the ingress
type IngressTLSConfig struct {
	// SecretName is the name of the secret containing the TLS certificate
	SecretName string `json:"secretName,omitempty"`

	// SecretCertKey is the key in the secret containing the TLS certificate
	SecretCertKey string `json:"secretCertKey,omitempty"`

	// SecretKeyKey is the key in the secret containing the TLS key
	SecretKeyKey string `json:"secretKeyKey,omitempty"`
}

// DatabaseConfig defines the database configuration
type DatabaseConfig struct {
	// SecretName is the name of the secret containing the database credentials
	SecretName string `json:"secretName,omitempty"`

	// SecretURLKey is the key in the secret containing the database URL
	SecretURLKey string `json:"secretURLKey,omitempty"`

	// SecretUserKey is the key in the secret containing the database user
	SecretUserKey string `json:"secretUserKey,omitempty"`

	// SecretPassKey is the key in the secret containing the database password
	SecretPassKey string `json:"secretPassKey,omitempty"`

	// SecretPortKey is the key in the secret containing the database port
	SecretPortKey string `json:"secretPortKey,omitempty"`

	// SecretHostKey is the key in the secret containing the database host
	SecretHostKey string `json:"secretHostKey,omitempty"`

	// SecretDatabaseNameKey is the key in the secret containing the database name
	SecretDatabaseNameKey string `json:"secretDatabaseNameKey,omitempty"`

	// SecretSSLKey is the key in the secret containing the database SSL configuration
	SecretSSLKey string `json:"secretSSLKey,omitempty"`

	// URL defines the database URL
	URL string `json:"url,omitempty"`

	// DatabaseName defines the name of the database to be used
	DatabaseName string `json:"databaseName,omitempty"`

	// Host defines the host of the database to be used
	Host string `json:"host,omitempty"`

	// Port defines the port of the database to be used
	Port string `json:"port,omitempty"`

	// Username defines the username of the database to be used
	User string `json:"user,omitempty"`

	// SSL defines if the database connection should use SSL
	SSL string `json:"ssl,omitempty"`
}

// UnleashStatus defines the observed state of Unleash
type UnleashStatus struct {
	// Represents the observations of a Unleash's current state.
	// Unleash.status.conditions.type are: "Available", "Progressing", and "Degraded"
	// Unleash.status.conditions.status are one of True, False, Unknown.
	// Unleash.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// Unleash.status.conditions.Message is a human readable message indicating details about the transition.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (u *UnleashStatus) IsReady() bool {
	for _, condition := range u.Conditions {
		if condition.Type == "Available" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func (u *Unleash) NamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: u.Namespace,
		Name:      u.Name,
	}
}

func (u *Unleash) NamespacedOperatorSecretName(operatorNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: operatorNamespace,
		Name:      u.GetOperatorSecretName(),
	}
}

func (u *Unleash) NamespacedInstanceSecretName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: u.Namespace,
		Name:      u.GetInstanceSecretName(),
	}
}

func (u *Unleash) GetInstanceSecretName() string {
	return fmt.Sprintf("%s-admin-key", u.Name)
}

func (u *Unleash) GetOperatorSecretName() string {
	return fmt.Sprintf("%s-%s-admin-key", u.Namespace, u.Name)
}

func (u *Unleash) GetURL() string {
	return fmt.Sprintf("http://%s.%s", u.Name, u.Namespace)
}
