package unleash_nais_io_v1

import (
	"context"
	"fmt"

	unleashclient "github.com/nais/unleasherator/pkg/unleash"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// +kubebuilder:default=1
	Size int32 `json:"size,omitempty"`

	// CustomImage points to a customImage, this overrides all other version settings
	// Use at your own risk
	// +kubebuilder:validation:Optional
	CustomImage string `json:"customImage,omitempty"`

	// Prometheus defines the prometheus configuration
	// +kubebuilder:validation:Optional
	Prometheus UnleashPrometheusConfig `json:"prometheus,omitempty"`

	// Database is the database configuration
	// +kubebuilder:validation:Required
	Database UnleashDatabaseConfig `json:"database,omitempty"`

	// WebIngress defines the ingress configuration for the web interface
	// +kubebuilder:validation:Optional
	WebIngress UnleashIngressConfig `json:"webIngress,omitempty"`

	// ApiIngress defines the ingress for the endpoints of Unleash
	// +kubebuilder:validation:Optional
	ApiIngress UnleashIngressConfig `json:"apiIngress,omitempty"`

	// NetworkPolicy defines the network policy configuration
	// +kubebuilder:validation:Optional
	NetworkPolicy UnleashNetworkPolicyConfig `json:"networkPolicy,omitempty"`

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

	// Resources are the resource requests and limits for the unleash deployment
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:={requests: {cpu: "300m", memory: "256Mi"}, limits: { memory: "512Mi"}}
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// UnleashPrometheusConfig defines the prometheus configuration
type UnleashPrometheusConfig struct {
	// Enable enables the prometheus metrics endpoint
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`
}

// UnleashNetworkPolicyConfig defines the network policy configuration
type UnleashNetworkPolicyConfig struct {
	// Enable enables the network policy
	// +kubebuilder:default=true
	Enabled bool `json:"enabled,omitempty"`

	// AllowDNS enables DNS traffic
	// +kubebuilder:default=true
	AllowDNS bool `json:"allowDNS,omitempty"`

	// AllowAllFromCluster enables all ingress traffic from the same cluster
	// +kubebuilder:default=false
	AllowAllFromCluster bool `json:"allowAll,omitempty"`

	// AllowAllFromSameNamespace enables all ingress traffic from the same namespace
	// +kubebuilder:default=false
	AllowAllFromSameNamespace bool `json:"allowAllSameNamespace,omitempty"`

	// AllowAllFromNamespaces is a list of namespaces to allow ingress traffic from
	// +kubebuilder:validation:Optional
	AllowAllFromNamespaces []string `json:"allowFromNamespaces,omitempty"`

	// ExtraIngressRules is a list of extra ingress rules to add to the network policy
	// +kubebuilder:validation:Optional
	ExtraIngressRules []networkingv1.NetworkPolicyIngressRule `json:"extraIngressRules,omitempty"`

	// ExtraEgressRules is a list of extra egress rules to add to the network policy
	// +kubebuilder:validation:Optional
	ExtraEgressRules []networkingv1.NetworkPolicyEgressRule `json:"extraEgressRules,omitempty"`
}

// UnleashIngressConfig defines the ingress configuration
type UnleashIngressConfig struct {
	// Enable enables the ingress
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// Host is the hostname to use for the ingress
	// +kubebuilder:validation:Optional
	Host string `json:"host,omitempty"`

	// Path is the path to use for the ingress
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="/"
	Path string `json:"path,omitempty"`

	// TLS is the TLS configuration to use for the ingress
	// +kubebuilder:validation:Optional
	TLS *UnleashIngressTLSConfig `json:"tls,omitempty"`

	// Annotations is a map of annotations to add to the ingress
	// +kubebuilder:validation:Optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Class is the ingress class to use for the ingress
	// +kubebuilder:validation:Optional
	Class string `json:"class,omitempty"`
}

// UnleashIngressTLSConfig defines the TLS configuration for the ingress
type UnleashIngressTLSConfig struct {
	// SecretName is the name of the secret containing the TLS certificate
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName,omitempty"`

	// SecretCertKey is the key in the secret containing the TLS certificate
	// +kubebuilder:validation:Required
	SecretCertKey string `json:"secretCertKey,omitempty"`

	// SecretKeyKey is the key in the secret containing the TLS key
	// +kubebuilder:validation:Required
	SecretKeyKey string `json:"secretKeyKey,omitempty"`
}

// UnleashDatabaseConfig defines the database configuration
type UnleashDatabaseConfig struct {
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
	// Unleash.status.conditions.type are: "Available", "Connection", and "Degraded"
	// Unleash.status.conditions.status are one of True, False, Unknown.
	// Unleash.status.conditions.reason the value should be a CamelCase string and producers of specific
	// condition types may define expected values and meanings for this field, and whether the values
	// are considered a guaranteed API.
	// Unleash.status.conditions.Message is a human readable message indicating details about the transition.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (u *Unleash) NamespacedName() types.NamespacedName {
	return types.NamespacedName{
		Namespace: u.Namespace,
		Name:      u.Name,
	}
}

func (u *Unleash) NamespacedNameWithSuffix(suffix string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: u.Namespace,
		Name:      fmt.Sprintf("%s-%s", u.Name, suffix),
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
	return fmt.Sprintf("%s-%s-admin-key", UnleashSecretNamePrefix, u.Name)
}

func (u *Unleash) GetOperatorSecretName() string {
	return fmt.Sprintf("%s-%s-%s-admin-key", UnleashSecretNamePrefix, u.Namespace, u.Name)
}

func (u *Unleash) GetURL() string {
	return fmt.Sprintf("http://%s.%s", u.Name, u.Namespace)
}

func (u *Unleash) GetAdminToken(ctx context.Context, client client.Client, operatorNamespace string) ([]byte, error) {
	secret := &corev1.Secret{}

	err := client.Get(ctx, u.NamespacedOperatorSecretName(operatorNamespace), secret)
	if err != nil {
		return nil, err
	}

	return secret.Data[UnleashSecretTokenKey], nil
}

func (u *Unleash) GetApiClient(ctx context.Context, client client.Client, operatorNamespace string) (*unleashclient.Client, error) {
	token, err := u.GetAdminToken(ctx, client, operatorNamespace)
	if err != nil {
		return nil, err
	}

	return unleashclient.NewClient(u.GetURL(), string(token))
}

// IsReady returns true if the Unleash instance is ready.
// We define ready as having both the Available and Connection conditions set to true.
func (u *Unleash) IsReady() bool {
	return conditionStatusIsReady(u.Status.Conditions)
}
