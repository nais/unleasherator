package controllers

import (
	"fmt"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func remoteUnleashSecretResource(name, namespace, token string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("unleasherator-%s", name),
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"token": []byte(token),
		},
	}
}

func remoteUnleashResource(name, namespace, url string, secret *corev1.Secret) (types.NamespacedName, *unleashv1.RemoteUnleash) {
	lookupKey := types.NamespacedName{Name: name, Namespace: namespace}
	remoteUnleash := &unleashv1.RemoteUnleash{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "unleash.nais.io/v1",
			Kind:       "RemoteUnleash",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      lookupKey.Name,
			Namespace: lookupKey.Namespace,
		},
		Spec: unleashv1.RemoteUnleashSpec{
			Server: unleashv1.RemoteUnleashServer{
				URL: url,
			},
			AdminSecret: unleashv1.RemoteUnleashSecret{
				Name: secret.Name,
			},
		},
	}

	return lookupKey, remoteUnleash
}

func remoteUnleashApiTokenResource(name, namespace, secretName string, remoteUnleash *unleashv1.RemoteUnleash) *unleashv1.ApiToken {
	return &unleashv1.ApiToken{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "unleash.nais.io/v1",
			Kind:       "ApiToken",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: unleashv1.ApiTokenSpec{
			UnleashInstance: unleashv1.ApiTokenUnleashInstance{
				ApiVersion: "unleash.nais.io/v1",
				Kind:       "RemoteUnleash",
				Name:       remoteUnleash.Name,
			},
			SecretName: secretName,
		},
	}
}

func unleashResource(name, namespace string, spec unleashv1.UnleashSpec) *unleashv1.Unleash {
	return &unleashv1.Unleash{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "unleash.nais.io/v1",
			Kind:       "Unleash",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: spec,
	}
}

func unleashApiTokenResource(name, namespace, secretName string, unleash *unleashv1.Unleash) *unleashv1.ApiToken {
	return &unleashv1.ApiToken{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "unleash.nais.io/v1",
			Kind:       "ApiToken",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: unleashv1.ApiTokenSpec{
			UnleashInstance: unleashv1.ApiTokenUnleashInstance{
				ApiVersion: "unleash.nais.io/v1",
				Kind:       "Unleash",
				Name:       unleash.Name,
			},
			SecretName: secretName,
		},
	}
}

func unsetConditionLastTransitionTime(conditions []metav1.Condition) []metav1.Condition {
	for i := range conditions {
		conditions[i].LastTransitionTime = metav1.Time{}
	}

	return conditions
}

// promeGaugeVecVal returns the value of a prometheus GaugeVec
func promGaugeVecVal(gv *prometheus.GaugeVec, lvs ...string) (float64, error) {
	var m = &dto.Metric{}

	if err := gv.WithLabelValues(lvs...).Write(m); err != nil {
		return 0, err
	}

	return m.GetGauge().GetValue(), nil
}

func promCounterVecVal(cv *prometheus.CounterVec, lvs ...string) (float64, error) {
	var m = &dto.Metric{}

	if err := cv.WithLabelValues(lvs...).Write(m); err != nil {
		return 0, nil
	}

	return m.GetCounter().GetValue(), nil
}

func promCounterVecFlush(cv *prometheus.CounterVec) {
	cv.Reset()
}
