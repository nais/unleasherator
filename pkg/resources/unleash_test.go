package resources

import (
	"testing"

	featuretogglingv1 "github.com/nais/unleasherator/api/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

func TestDeploymentForUnleash(t *testing.T) {
	var err error
	var u *featuretogglingv1.Unleash

	err = featuretogglingv1.AddToScheme(scheme.Scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	u = &featuretogglingv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: featuretogglingv1.UnleashSpec{
			Size: 1,
		},
	}
	_, err = DeploymentForUnleash(u, scheme.Scheme)
	if err == nil {
		t.Error("expected error when no database is configured")
	}

	u = &featuretogglingv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: featuretogglingv1.UnleashSpec{
			Size: 1,
			Database: featuretogglingv1.UnleashDatabase{
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
	var u *featuretogglingv1.Unleash

	err = featuretogglingv1.AddToScheme(scheme.Scheme)
	if err != nil {
		t.Error("failed to add Unleash to scheme", err)
	}

	u = &featuretogglingv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "unleash",
			Namespace: "unleash",
		},
		Spec: featuretogglingv1.UnleashSpec{
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
