package resources

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
)

// test the RemoteunleashInstance function
func TestRemoteunleashInstance(t *testing.T) {
	object := RemoteunleashInstance("name", "url", "namespace", "secretName", "secretNamespace")
	remoteUnleash := object.(*unleashv1.RemoteUnleash)

	assert.Equal(t, "name", remoteUnleash.GetName())
	assert.Equal(t, "namespace", remoteUnleash.GetNamespace())
	assert.Equal(t, "url", remoteUnleash.Spec.Server.URL)
	assert.Equal(t, "secretNamespace", remoteUnleash.Spec.AdminSecret.Namespace)
	assert.Equal(t, unleashv1.UnleashSecretTokenKey, remoteUnleash.Spec.AdminSecret.Key)
}

func TestRemoteunleashInstances(t *testing.T) {
	const name = "name"
	const url = "url"
	var namespaces = []string{"namespace1", "namespace2"}
	const secretName = "secretName"
	const secretNamespace = "secretNamespace"

	resources := RemoteunleashInstances(name, url, namespaces, secretName, secretNamespace)

	assertPair := func(remoteUnleash *unleashv1.RemoteUnleash, namespace string) {
		assert.Equal(t, name, remoteUnleash.GetName())
		assert.Equal(t, namespace, remoteUnleash.GetNamespace())
		assert.Equal(t, url, remoteUnleash.Spec.Server.URL)
		assert.Equal(t, secretNamespace, remoteUnleash.Spec.AdminSecret.Namespace)
		assert.Equal(t, unleashv1.UnleashSecretTokenKey, remoteUnleash.Spec.AdminSecret.Key)
	}

	assert.Equal(t, len(namespaces), len(resources))

	for i := 0; i < len(namespaces); i++ {
		assertPair(resources[i].(*unleashv1.RemoteUnleash), namespaces[i])
	}
}
