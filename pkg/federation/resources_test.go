package federation

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/pb"
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
		},
	}

	token := "my-token"

	instance := UnleashFederationInstance(unleash, token)

	assert.Equal(t, int32(pb.Version), instance.Version, "unexpected version")
	assert.Equal(t, pb.Status_Provisioned, instance.Status, "unexpected status")
	assert.Equal(t, unleash.GetName(), instance.Name, "unexpected name")
	assert.Equal(t, unleash.PublicApiURL(), instance.Url, "unexpected URL")
	assert.Equal(t, token, instance.SecretToken, "unexpected token")
	assert.Equal(t, []string{unleash.GetName()}, instance.Namespaces, "unexpected namespaces")
}
