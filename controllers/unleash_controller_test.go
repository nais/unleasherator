package controllers

import (
	"context"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/mock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

var _ = Describe("Unleash controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		UnleashNamespace = "default"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a Unleash", func() {
		It("Should fail for missing database config", func() {
			ctx := context.Background()

			UnleashName := "test-unleash-fail"

			By("By creating a new Unleash")
			unleash := &unleashv1.Unleash{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "Unleash",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      UnleashName,
					Namespace: UnleashNamespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{},
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			unleashLookupKey := unleash.NamespacedName()
			createdUnleash := &unleashv1.Unleash{}

			// We'll need to retry getting this newly created Unleash, given that creation may not immediately happen.
			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, unleashLookupKey, createdUnleash)
				if err != nil {
					return nil, err
				}

				// unset condition.LastTransitionTime to make comparison easier
				unsetConditionLastTransitionTime(createdUnleash.Status.Conditions)

				return createdUnleash.Status.Conditions, nil
			}, timeout, interval).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
				Message: "Failed to reconcile Deployment: validation failed for Deployment (either database.url or database.secretName must be set)",
			}))

			Expect(createdUnleash.IsReady()).To(BeFalse())

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should succeed when it can connect to Unleash", func() {
			ctx := context.Background()

			By("By mocking Unleash API")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", "http://test-unleash-success.default/api/admin/instance-admin/statistics",
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))

			By("By creating a new Unleash")
			unleash := &unleashv1.Unleash{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "Unleash",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash-success",
					Namespace: UnleashNamespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
					},
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			unleashLookupKey := unleash.NamespacedName()
			createdUnleash := &unleashv1.Unleash{}

			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, unleashLookupKey, createdUnleash)
				if err != nil {
					return nil, err
				}

				// unset condition.LastTransitionTime to make comparison easier
				unsetConditionLastTransitionTime(createdUnleash.Status.Conditions)

				return createdUnleash.Status.Conditions, nil
			}, timeout, interval).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			Expect(createdUnleash.Status.Version).To(Equal("v4.0.0"))
			Expect(createdUnleash.Status.Reconciled).To(BeTrue())
			Expect(createdUnleash.Status.Connected).To(BeTrue())

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, unleashLookupKey, deployment)).Should(Succeed())

			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, unleashLookupKey, service)).Should(Succeed())

			instanceSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedInstanceSecretName(), instanceSecret)).Should(Succeed())

			operatorSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedOperatorSecretName(operatorNamespace), operatorSecret)).Should(Succeed())

			networkPolicy := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, unleashLookupKey, networkPolicy)).Should(Succeed())

			serviceMonitor := &monitoringv1.ServiceMonitor{}
			Expect(k8sClient.Get(ctx, unleashLookupKey, serviceMonitor)).Should(Succeed())

			var m1 = &dto.Metric{}
			Expect(unleashStatus.WithLabelValues(unleashLookupKey.Namespace, unleashLookupKey.Name, unleashv1.UnleashStatusConditionTypeConnected).Write(m1)).Should(Succeed())
			Expect(m1.GetGauge().GetValue()).To(Equal(float64(1)))

			var m2 = &dto.Metric{}
			Expect(unleashStatus.WithLabelValues(unleashLookupKey.Namespace, unleashLookupKey.Name, unleashv1.UnleashStatusConditionTypeReconciled).Write(m2)).Should(Succeed())
			Expect(m2.GetGauge().GetValue()).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should publish Unleash instance when federation is enabled", func() {
			ctx := context.Background()

			By("By mocking Unleash API")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", "http://test-unleash-federate.default/api/admin/instance-admin/statistics",
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))

			By("By mocking Unleash Publisher")
			mockPublisher.On("Publish", mock.AnythingOfType("*context.valueCtx"), mock.AnythingOfType("*unleash_nais_io_v1.Unleash"), mock.AnythingOfType("string")).Return(nil)

			By("By creating a new Unleash")
			unleash := &unleashv1.Unleash{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "Unleash",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-unleash-federate",
					Namespace: UnleashNamespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
					},
					Federation: unleashv1.UnleashFederationConfig{
						Enabled:    true,
						Namespaces: []string{"namespace1", "namespace2"},
						Clusters:   []string{"cluster1", "cluster2"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			unleashLookupKey := unleash.NamespacedName()
			createdUnleash := &unleashv1.Unleash{}

			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, unleashLookupKey, createdUnleash)
				if err != nil {
					return nil, err
				}

				// unset condition.LastTransitionTime to make comparison easier
				unsetConditionLastTransitionTime(createdUnleash.Status.Conditions)

				return createdUnleash.Status.Conditions, nil
			}, timeout, interval).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			Expect(mockPublisher.AssertCalled(GinkgoT(), "Publish", mock.AnythingOfType("*context.valueCtx"), mock.AnythingOfType("*unleash_nais_io_v1.Unleash"), mock.AnythingOfType("string"))).To(BeTrue())

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})
	})
})
