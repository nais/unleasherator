package controller

import (
	"context"
	"fmt"

	"github.com/jarcoal/httpmock"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
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

func releaseChannelResource(name, namespace, image string) *unleashv1.ReleaseChannel {
	return &unleashv1.ReleaseChannel{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "unleash.nais.io/v1",
			Kind:       "ReleaseChannel",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: unleashv1.ReleaseChannelSpec{
			Image: unleashv1.UnleashImage(image),
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

func apiTokenEventually(ctx context.Context, apiTokenLookup types.NamespacedName, apiTokenCreated *unleashv1.ApiToken) func() ([]metav1.Condition, error) {
	return func() ([]metav1.Condition, error) {
		err := k8sClient.Get(ctx, apiTokenLookup, apiTokenCreated)
		if err != nil {
			return nil, err
		}

		unsetConditionLastTransitionTime(apiTokenCreated.Status.Conditions)

		return apiTokenCreated.Status.Conditions, nil
	}
}

func apiTokenSuccessCondition() metav1.Condition {
	return metav1.Condition{
		Type:    unleashv1.ApiTokenStatusConditionTypeCreated,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "API token successfully created",
	}
}

func apiTokenSecretEventually(ctx context.Context, name types.NamespacedName, secret *corev1.Secret) func() (map[string][]byte, error) {
	return func() (map[string][]byte, error) {
		err := k8sClient.Get(ctx, name, secret)
		if err != nil {
			return nil, err
		}

		return secret.Data, err
	}
}

func remoteUnleashEventually(ctx context.Context, remoteUNleashLookup types.NamespacedName, remoteUnleashCreated *unleashv1.RemoteUnleash) func() ([]metav1.Condition, error) {
	return func() ([]metav1.Condition, error) {
		err := k8sClient.Get(ctx, remoteUNleashLookup, remoteUnleashCreated)
		if err != nil {
			return nil, err
		}

		unsetConditionLastTransitionTime(remoteUnleashCreated.Status.Conditions)

		return remoteUnleashCreated.Status.Conditions, nil
	}
}

func remoteUnleashSuccessCondition() metav1.Condition {
	return metav1.Condition{
		Type:    unleashv1.UnleashStatusConditionTypeConnected,
		Status:  metav1.ConditionTrue,
		Reason:  "Reconciling",
		Message: "Successfully connected to Unleash",
	}
}

// mockRemoteUnleashURL generates a unique URL for RemoteUnleash based on name and namespace.
// This mirrors the Unleash.URL() pattern: http://<name>.<namespace>
// Using unique URLs per test ensures httpmock isolation between concurrent tests.
func mockRemoteUnleashURL(name, namespace string) string {
	return fmt.Sprintf("http://%s.%s", name, namespace)
}

// registerHTTPMocksForRemoteUnleash registers httpmock responders for a RemoteUnleash instance.
// Each RemoteUnleash should use a unique URL (via mockRemoteUnleashURL) to prevent cross-test interference.
func registerHTTPMocksForRemoteUnleash(remoteUnleash *unleashv1.RemoteUnleash, version string) {
	url := remoteUnleash.URL()
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s%s", url, unleashclient.HealthEndpoint),
		httpmock.NewStringResponder(200, `{"health": "OK"}`))
	httpmock.RegisterResponder("GET", fmt.Sprintf("%s%s", url, unleashclient.InstanceAdminStatsEndpoint),
		httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, version)))
}
