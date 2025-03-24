package resources

import (
	"strings"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ApiTokenSecret(unleash UnleashInstance, token *unleashv1.ApiToken, apiToken *unleashclient.ApiToken) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      token.Spec.SecretName,
			Namespace: token.GetObjectMeta().GetNamespace(),
			Annotations: map[string]string{
				"reloader.stakater.com/match": "true",
			},
		},
		Data: map[string][]byte{
			unleashv1.ApiTokenSecretTokenEnv:    []byte(apiToken.Secret),
			unleashv1.ApiTokenSecretServerEnv:   []byte(unleash.URL()),
			unleashv1.ApiTokenSecretEnvEnv:      []byte(token.Spec.Environment),
			unleashv1.ApiTokenSecretTypeEnv:     []byte(token.Spec.Type),
			unleashv1.ApiTokenSecretProjectsEnv: []byte(strings.Join(token.Spec.Projects, ",")),
		},
	}
}
