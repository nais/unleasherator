package controllers

import (
	"context"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
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
				Type:    unleashv1.UnleashStatusConditionTypeAvailable,
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
			httpmock.RegisterResponder("GET", "http://test-unleash-success.default/health",
				httpmock.NewStringResponder(200, `{"health": "GOOD"}`))

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
				Type:    unleashv1.UnleashStatusConditionTypeConnection,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			var m = &dto.Metric{}

			Expect(unleashStatus.WithLabelValues(unleashLookupKey.Namespace, unleashLookupKey.Name, unleashv1.UnleashStatusConditionTypeConnection).Write(m)).Should(Succeed())

			Expect(m.GetGauge().GetValue()).To(Equal(float64(1)))

			Expect(unleashStatus.WithLabelValues(unleashLookupKey.Namespace, unleashLookupKey.Name, unleashv1.UnleashStatusConditionTypeAvailable).Write(m)).Should(Succeed())
			Expect(m.GetGauge().GetValue()).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})
	})
})
