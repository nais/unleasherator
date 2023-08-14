package controllers

import (
	"context"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/mock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/unleashclient"
)

func getUnleash(k8sClient client.Client, ctx context.Context, unleash *unleashv1.Unleash) ([]metav1.Condition, error) {
	if err := k8sClient.Get(ctx, unleash.NamespacedName(), unleash); err != nil {
		return nil, err
	}

	return unsetConditionLastTransitionTime(unleash.Status.Conditions), nil
}

var _ = Describe("Unleash controller", func() {
	const (
		UnleashNamespace = "default"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a Unleash", func() {
		It("Should fail for missing database config", func() {
			ctx := context.Background()

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-fail", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
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
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-success", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
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
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), deployment)).Should(Succeed())

			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), service)).Should(Succeed())

			instanceSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedInstanceSecretName(), instanceSecret)).Should(Succeed())

			operatorSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedOperatorSecretName(operatorNamespace), operatorSecret)).Should(Succeed())

			networkPolicy := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), networkPolicy)).Should(Succeed())

			serviceMonitor := &monitoringv1.ServiceMonitor{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), serviceMonitor)).Should(Succeed())

			val, err := promGaugeVecVal(unleashStatus, createdUnleash.Namespace, createdUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			val, err = promGaugeVecVal(unleashStatus, createdUnleash.Namespace, createdUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should publish Unleash instance when federation is enabled", func() {
			ctx := context.Background()

			By("By mocking Unleash API")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
				httpmock.NewStringResponder(200, `{"health": "OK"}`))
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))

			By("By mocking Unleash Publisher")
			matcher := func(unleash *unleashv1.Unleash) bool {
				return unleash.Name == "test-unleash-federate"
			}
			mockPublisher.On("Publish", mock.AnythingOfType("*context.valueCtx"), mock.MatchedBy(matcher), mock.AnythingOfType("string")).Return(nil)

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-federate", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
				Federation: unleashv1.UnleashFederationConfig{
					Enabled:    true,
					Namespaces: []string{"namespace1", "namespace2"},
					Clusters:   []string{"cluster1", "cluster2"},
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())
			promCounterVecFlush(unleashPublished)

			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			Expect(mockPublisher.AssertCalled(GinkgoT(), "Publish", mock.AnythingOfType("*context.valueCtx"), mock.AnythingOfType("*unleash_nais_io_v1.Unleash"), mock.AnythingOfType("string"))).To(BeTrue())
			Expect(mockPublisher.AssertNumberOfCalls(GinkgoT(), "Publish", 1)).To(BeTrue())

			val, err := promCounterVecVal(unleashPublished, "provisioned", "success")
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})
	})
})
