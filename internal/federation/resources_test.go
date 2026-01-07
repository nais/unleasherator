package federation

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/pb"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUnleashFederationInstance(t *testing.T) {
	unleash := &unleashv1.Unleash{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Unleash",
			APIVersion: "unleash.nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Federation: unleashv1.UnleashFederationConfig{
				Namespaces:  []string{"namespace-1", "namespace-2"},
				Clusters:    []string{"cluster-1", "cluster-2"},
				SecretNonce: "not-a-secret",
			},
		},
	}

	instance := UnleashFederationInstance(unleash, "my-token")

	assert.Equal(t, int32(pb.Version), instance.Version, "unexpected version")
	assert.Equal(t, pb.Status_Provisioned, instance.Status, "unexpected status")
	assert.Equal(t, unleash.GetName(), instance.Name, "unexpected name")
	assert.Equal(t, unleash.PublicApiURL(), instance.Url, "unexpected URL")
	assert.Equal(t, "my-token", instance.SecretToken, "unexpected token")
	assert.Equal(t, "not-a-secret", instance.SecretNonce, "unexpected secret nonce")
	assert.Equal(t, []string{"namespace-1", "namespace-2"}, instance.Namespaces, "unexpected namespaces")
	assert.Equal(t, []string{"cluster-1", "cluster-2"}, instance.Clusters, "unexpected namespaces")
}

func TestUnleashFederationInstanceRemoved(t *testing.T) {
	unleash := &unleashv1.Unleash{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Unleash",
			APIVersion: "unleash.nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Federation: unleashv1.UnleashFederationConfig{
				Namespaces:  []string{"namespace-1", "namespace-2"},
				Clusters:    []string{"cluster-1", "cluster-2"},
				SecretNonce: "not-a-secret",
			},
		},
	}

	instance := UnleashFederationInstanceRemoved(unleash)

	assert.Equal(t, int32(pb.Version), instance.Version, "unexpected version")
	assert.Equal(t, pb.Status_Removed, instance.Status, "unexpected status")
	assert.Equal(t, unleash.GetName(), instance.Name, "unexpected name")
	assert.Equal(t, unleash.PublicApiURL(), instance.Url, "unexpected URL")
	assert.Empty(t, instance.SecretToken, "token should be empty for removal")
	assert.Equal(t, "not-a-secret", instance.SecretNonce, "unexpected secret nonce")
	assert.Equal(t, []string{"namespace-1", "namespace-2"}, instance.Namespaces, "unexpected namespaces")
	assert.Equal(t, []string{"cluster-1", "cluster-2"}, instance.Clusters, "unexpected clusters")
}
