package resources

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/pb"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestDeploymentForUnleash(t *testing.T) {
	var err error
	var u *unleashv1.Unleash

	err = unleashv1.AddToScheme(scheme.Scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	u = &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
		},
	}
	_, err = DeploymentForUnleash(u, scheme.Scheme)
	if err == nil {
		t.Error("expected error when no database is configured")
	}

	u = &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
			Database: unleashv1.UnleashDatabaseConfig{
				SecretName:   "unleash-db",
				SecretURLKey: "url",
			},
		},
	}

	_, err = DeploymentForUnleash(u, scheme.Scheme)
	if err != nil {
		t.Error("expected no when database is configured, got", err)
	}
}

func TestNetworkPolicyForUnleash(t *testing.T) {
	var err error
	var np *networkingv1.NetworkPolicy
	var u *unleashv1.Unleash

	err = unleashv1.AddToScheme(scheme.Scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	u = &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: unleashv1.UnleashSpec{
			Size: 1,
		},
	}
	np, err = NetworkPolicyForUnleash(u, scheme.Scheme, "some-namespace")
	if err != nil {
		t.Error("unexpected error", err)
	}

	if np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "some-namespace" {
		t.Error("expected namespace selector to be set, got", np.Spec.Ingress[0].From[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"])
	}
}

func TestRemoteUnleasheratorResources(t *testing.T) {
	const secretToken = "secrettoken"
	const url = "url"
	const name = "name"
	const secretNamespace = "secretnamespace"

	msg := &pb.Instance{
		Version:     pb.Version,
		Status:      pb.Status_Provisioned,
		Name:        name,
		Url:         url,
		SecretToken: secretToken,
		Namespaces:  []string{"foo", "bar"},
	}

	resources := RemoteUnleasheratorResources(msg, secretNamespace)

	assertPair := func(remoteUnleash *unleashv1.RemoteUnleash, secret *corev1.Secret, namespace string) {
		assert.Equal(t, name, remoteUnleash.GetName())
		assert.Equal(t, namespace, remoteUnleash.GetNamespace())
		assert.Equal(t, url, remoteUnleash.Spec.Server.URL)
		assert.Equal(t, secretNamespace, remoteUnleash.Spec.AdminSecret.Namespace)
		assert.Equal(t, unleashv1.UnleashSecretTokenKey, remoteUnleash.Spec.AdminSecret.Key)

		assert.Equal(t, secretToken, secret.StringData[unleashv1.UnleashSecretTokenKey])
		assert.Equal(t, secretNamespace, secret.GetNamespace())
	}

	assertPair(resources[0].(*unleashv1.RemoteUnleash), resources[1].(*corev1.Secret), msg.Namespaces[0])
	assertPair(resources[2].(*unleashv1.RemoteUnleash), resources[3].(*corev1.Secret), msg.Namespaces[1])
}
