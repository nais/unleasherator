package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
					Database: unleashv1.DatabaseConfig{},
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
				return createdUnleash.Status.Conditions, nil
			}, timeout, interval).Should(HaveLen(1))

			Expect(createdUnleash.Status.Conditions[0].Type).To(Equal(typeAvailableUnleash))
			Expect(createdUnleash.Status.Conditions[0].Status).To(Equal(metav1.ConditionFalse))
			Expect(createdUnleash.Status.Conditions[0].Reason).To(Equal("Reconciling"))
			Expect(createdUnleash.Status.Conditions[0].Message).To(Equal("Failed to create Deployment for the custom resource (test-unleash-fail): (either database.url or database.secretName must be set)"))
			Expect(createdUnleash.IsReady()).To(BeFalse())

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should succeed when it can connect to Unleash", func() {
		})
	})
})
