package resources

import (
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
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

	u.Spec.PodLabels = map[string]string{
		"app": "unleash",
		"env": "dev",
	}

	u.Spec.PodAnnotations = map[string]string{
		"prometheus.io/scrape": "true",
		"prometheus.io/port":   "4242",
	}

	deploy, err := DeploymentForUnleash(u, scheme.Scheme)
	if err != nil {
		t.Error("unexpected error", err)
	}

	assert.Equal(t, deploy.Spec.Template.Labels["app"], "unleash")
	assert.Equal(t, deploy.Spec.Template.Labels["env"], "dev")
	assert.Equal(t, deploy.Spec.Template.Annotations["prometheus.io/scrape"], "true")
	assert.Equal(t, deploy.Spec.Template.Annotations["prometheus.io/port"], "4242")
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
