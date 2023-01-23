/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// UnleashSpec defines the desired state of Unleash
type UnleashSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Size is the size of the unleash deployment
	Size int32 `json:"size,omitempty"`

	// Database is the database configuration
	Database UnleashDatabase `json:"database,omitempty"`

	// ExtraEnv is a list of extra environment variables to add to the deployment
	// +kubebuilder:validation:Optional
	ExtraEnvVars      []corev1.EnvVar      `json:"extraEnvVars,omitempty"`
	ExtraVolumes      []corev1.Volume      `json:"extraVolumes,omitempty"`
	ExtraVolumeMounts []corev1.VolumeMount `json:"extraVolumeMounts,omitempty"`
}

// UnleashDatabase defines the database configuration
type UnleashDatabase struct {
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

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// Unleash is the Schema for the unleashes API
type Unleash struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   UnleashSpec   `json:"spec,omitempty"`
	Status UnleashStatus `json:"status,omitempty"`
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
	return fmt.Sprintf("http://%s.%s.svc:4242", u.Name, u.Namespace)
}

//+kubebuilder:object:root=true

// UnleashList contains a list of Unleash
type UnleashList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Unleash `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Unleash{}, &UnleashList{})
}
