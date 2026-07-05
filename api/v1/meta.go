// Package v1 contains API Schema definitions for the unleash.nais.io v1 API group
// +kubebuilder:object:generate=true
// +groupName=unleash.nais.io
// +versionName=v1
package unleash_nais_io_v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "unleash.nais.io", Version: "v1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

const (
	UnleashStatusConditionTypeReconciled = "Reconciled"
	UnleashStatusConditionTypeConnected  = "Connected"
	UnleashStatusConditionTypeDegraded   = "Degraded"

	ApiTokenStatusConditionTypeCreated = "Created"
	ApiTokenStatusConditionTypeFailed  = "Failed"
	ApiTokenStatusConditionTypeDeleted = "Deleted"

	ReleaseChannelStatusConditionTypeReconciled = "Reconciled"
	ReleaseChannelStatusConditionTypeUpgrading  = "Upgrading"
	ReleaseChannelStatusConditionTypeCompleted  = "Completed"

	UnleashSecretNamePrefix = "unleasherator"
	UnleashSecretTokenKey   = "token"
	// UnleashSecretServerURLKey is the data key in an admin secret holding the
	// authorized Unleash server URL. A rename here must break the build in both
	// the writer (resources) and the reader (controller), never silently disable
	// the URL validation.
	UnleashSecretServerURLKey = "url"

	// UnleashSecretAuthorizedNamespaceAnnotation records the single tenant
	// namespace that an operator-managed admin secret is authorized for. It is the
	// authoritative, non-forgeable authorization control for cross-namespace
	// (operator namespace) admin secret references, replacing fragile secret-name
	// parsing as the primary confused-deputy defense.
	UnleashSecretAuthorizedNamespaceAnnotation = "unleash.nais.io/authorized-namespace"

	ApiTokenSecretTokenEnv    = "UNLEASH_SERVER_API_TOKEN"
	ApiTokenSecretServerEnv   = "UNLEASH_SERVER_API_URL"
	ApiTokenSecretEnvEnv      = "UNLEASH_SERVER_API_ENV"
	ApiTokenSecretTypeEnv     = "UNLEASH_SERVER_API_TYPE"
	ApiTokenSecretProjectsEnv = "UNLEASH_SERVER_API_PROJECTS"
)
