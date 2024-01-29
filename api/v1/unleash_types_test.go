package unleash_nais_io_v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestUnleashIsReady(t *testing.T) {
	unleash := Unleash{
		Status: UnleashStatus{
			Conditions: []metav1.Condition{
				{
					Type:   UnleashStatusConditionTypeReconciled,
					Status: metav1.ConditionFalse,
				},
			},
		},
	}

	assert.Equal(t, unleash.IsReady(), false, "Unleash should not be ready when available condition is false")

	unleash.Status.Conditions[0].Status = metav1.ConditionTrue
	assert.Equal(t, unleash.IsReady(), false, "Unleash should not be ready when connection condition is missing")

	unleash.Status.Conditions = append(unleash.Status.Conditions, metav1.Condition{
		Type:   UnleashStatusConditionTypeConnected,
		Status: metav1.ConditionFalse,
	})
	assert.Equal(t, unleash.IsReady(), false, "Unleash should not be ready when connection condition is false")

	unleash.Status.Conditions[1].Status = metav1.ConditionTrue
	assert.Equal(t, unleash.IsReady(), true, "Unleash should be ready when available and connection conditions are true")
}

func TestUnleashNamespacedName(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	expected := types.NamespacedName{
		Namespace: "test",
		Name:      "test",
	}

	assert.Equal(t, unleash.NamespacedName(), expected, "Unexpected NamespacedName")
}

func TestUnleashNamespacedNameWithSuffix(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	suffix := "suffix"
	expected := types.NamespacedName{
		Namespace: "test",
		Name:      "test-suffix",
	}

	assert.Equal(t, unleash.NamespacedNameWithSuffix(suffix), expected, "Unexpected NamespacedName with suffix")
}

func TestUnleashNamespacedOperatorSecretName(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	podNamespace := "operator-namespace"
	expected := types.NamespacedName{
		Namespace: podNamespace,
		Name:      unleash.GetOperatorSecretName(),
	}

	assert.Equal(t, unleash.NamespacedOperatorSecretName(podNamespace), expected, "Unexpected NamespacedName for operator secret")
}

func TestUnleashNamespacedInstanceSecretName(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	expected := types.NamespacedName{
		Namespace: "test",
		Name:      unleash.GetInstanceSecretName(),
	}

	assert.Equal(t, unleash.NamespacedInstanceSecretName(), expected, "Unexpected NamespacedName for instance secret")
}

func TestUnleashGetInstanceSecretName(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	expected := "unleasherator-test-admin-key"
	assert.Equal(t, unleash.GetInstanceSecretName(), expected, "Unexpected instance secret name")
}

func TestUnleashGetOperatorSecretName(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	expected := "unleasherator-test-test-admin-key"
	assert.Equal(t, unleash.GetOperatorSecretName(), expected, "Unexpected operator secret name")
}

func TestUnleashURL(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
	}

	expectedURL := "http://test.test"
	actualURL := unleash.URL()

	if actualURL != expectedURL {
		t.Errorf("Expected URL to be %s, got %s", expectedURL, actualURL)
	}
}

func TestUnleashPublicApiURL(t *testing.T) {
	unleash := Unleash{
		Spec: UnleashSpec{
			ApiIngress: UnleashIngressConfig{
				Host: "example.com",
			},
		},
	}

	expectedURL := "https://example.com"
	actualURL := unleash.PublicApiURL()

	assert.Equal(t, expectedURL, actualURL, "Unexpected Public API URL")
}

func TestUnleashPublicWebURL(t *testing.T) {
	unleash := Unleash{
		Spec: UnleashSpec{
			WebIngress: UnleashIngressConfig{
				Host: "example.com",
			},
		},
	}

	expectedURL := "https://example.com"
	actualURL := unleash.PublicWebURL()

	assert.Equal(t, expectedURL, actualURL, "Unexpected Public Web URL")
}
