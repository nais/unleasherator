package unleash_nais_io_v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUnleashURL(t *testing.T) {
	unleash := Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: UnleashSpec{
			Size: 1,
		},
	}

	if unleash.GetURL() != "http://test.test" {
		t.Errorf("Expected URL to be http://test.test, got %s", unleash.GetURL())
	}
}

func TestUnleashIsReady(t *testing.T) {
	unleash := Unleash{
		Status: UnleashStatus{
			Conditions: []metav1.Condition{
				{
					Type:   UnleashStatusConditionTypeAvailable,
					Status: metav1.ConditionFalse,
				},
			},
		},
	}

	assert.Equal(t, unleash.IsReady(), false, "Unleash should not be ready when available condition is false")

	unleash.Status.Conditions[0].Status = metav1.ConditionTrue
	assert.Equal(t, unleash.IsReady(), false, "Unleash should not be ready when connection condition is missing")

	unleash.Status.Conditions = append(unleash.Status.Conditions, metav1.Condition{
		Type:   UnleashStatusConditionTypeConnection,
		Status: metav1.ConditionFalse,
	})
	assert.Equal(t, unleash.IsReady(), false, "Unleash should not be ready when connection condition is false")

	unleash.Status.Conditions[1].Status = metav1.ConditionTrue
	assert.Equal(t, unleash.IsReady(), true, "Unleash should be ready when available and connection conditions are true")
}
