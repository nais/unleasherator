package resources

import (
	unleashv1 "github.com/nais/unleasherator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func RemoteunleashInstance(name, url, namespace, secretName, secretNamespace string) *unleashv1.RemoteUnleash {
	return &unleashv1.RemoteUnleash{
		TypeMeta: metav1.TypeMeta{
			Kind:       "RemoteUnleash",
			APIVersion: "unleash.nais.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{
				URL: url,
			},
			AdminSecret: unleashv1.RemoteUnleashSecret{
				Name:      secretName,
				Key:       unleashv1.UnleashSecretTokenKey,
				Namespace: secretNamespace,
			},
		},
	}
}

func RemoteunleashInstances(name, url string, namespaces []string, secretName, secretNamespace string) []*unleashv1.RemoteUnleash {
	resources := make([]*unleashv1.RemoteUnleash, 0, len(namespaces))
	for _, namespace := range namespaces {
		resources = append(resources, RemoteunleashInstance(name, url, namespace, secretName, secretNamespace))
	}
	return resources
}
