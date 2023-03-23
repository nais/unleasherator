package unleash_nais_io_v1

import (
	"testing"

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
