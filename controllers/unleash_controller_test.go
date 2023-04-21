package controllers

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

var _ = Describe("Unleash controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		UnleashName      = "test-unleash"
		UnleashNamespace = "default"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When updating Unleash Status", func() {
		It("Should increase Unleash Status.Active count when new Jobs are created", func() {
			By("By creating a new Unleash")
			ctx := context.Background()
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
					Size: 1,
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			unleashLookupKey := types.NamespacedName{Name: UnleashName, Namespace: UnleashNamespace}
			createUnleash := &unleashv1.Unleash{}

			// We'll need to retry getting this newly created Unleash, given that creation may not immediately happen.
			Eventually(func() bool {
				err := k8sClient.Get(ctx, unleashLookupKey, createUnleash)
				return err == nil
			}, timeout, interval).Should(BeTrue())
			// Let's make sure our Schedule string value was properly converted/handled.
			Expect(createUnleash.Spec.Size).Should(Equal(int32(1)))

			By("This fails, always")
			Expect(false).Should(BeTrue())

		})
	})

})
