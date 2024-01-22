package resources

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/unleashclient"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApiTokenSecret(t *testing.T) {
	unleash := unleashv1.RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-unleash",
			Namespace: "default",
		},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{
				URL: "http://api-token-unleash.nais.io",
			},
		},
	}

	token := &unleashv1.ApiToken{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-apitoken",
			Namespace: "default",
		},
		Spec: unleashv1.ApiTokenSpec{
			SecretName:  "test-secret",
			Type:        "CLIENT",
			Environment: "development",
			Projects:    []string{"default"},
		},
	}

	apiToken := &unleashclient.ApiToken{
		Secret:      "test-secret",
		Username:    "test-apitoken-unleaasherator",
		Type:        "CLIENT",
		Environment: "development",
		Projects:    []string{"default"},
	}

	expectedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			unleashv1.ApiTokenSecretTokenEnv:    []byte("test-secret"),
			unleashv1.ApiTokenSecretServerEnv:   []byte("http://api-token-unleash.nais.io"),
			unleashv1.ApiTokenSecretEnvEnv:      []byte("development"),
			unleashv1.ApiTokenSecretTypeEnv:     []byte("CLIENT"),
			unleashv1.ApiTokenSecretProjectsEnv: []byte("default"),
		},
	}

	secret := ApiTokenSecret(&unleash, token, apiToken)

	assert.Equal(t, expectedSecret, secret)
}
