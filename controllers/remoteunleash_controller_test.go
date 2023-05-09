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

var _ = Describe("RemoteUnleash controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		RemoteUnleashName      = "test-remote-unleash"
		RemoteUnleashNamespace = "default"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a RemoteUnleash", func() {
		It("Should fail if the secret does not exist", func() {
			By("By creating a new RemoteUnleash")
			ctx := context.Background()
			remoteUnleash := &unleashv1.RemoteUnleash{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "RemoteUnleash",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      RemoteUnleashName,
					Namespace: RemoteUnleashNamespace,
				},
				Spec: unleashv1.RemoteUnleashSpec{
					Server: unleashv1.RemoteUnleashServer{
						URL: "https://unleash.nais.io",
					},
					AdminSecret: unleashv1.RemoteUnleashSecret{
						Name: "unleasherator-not-exist",
					},
				},
			}
			Expect(k8sClient.Create(ctx, remoteUnleash)).Should(Succeed())

			remoteUnleashLookupKey := types.NamespacedName{Name: RemoteUnleashName, Namespace: RemoteUnleashNamespace}
			createdRemoteUnleash := &unleashv1.RemoteUnleash{}

			// We'll need to retry getting this newly created RemoteUnleash, given that creation may not immediately happen.
			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, remoteUnleashLookupKey, createdRemoteUnleash)
				if err != nil {
					return nil, err
				}
				return createdRemoteUnleash.Status.Conditions, nil
			}, timeout, interval).Should(HaveLen(2))

			Expect(createdRemoteUnleash.Status.Conditions[1].Type).To(Equal(typeAvailableUnleash))
			Expect(createdRemoteUnleash.Status.Conditions[1].Status).To(Equal(metav1.ConditionFalse))
			Expect(createdRemoteUnleash.Status.Conditions[1].Reason).To(Equal("Reconciling"))
			Expect(createdRemoteUnleash.Status.Conditions[1].Message).To(Equal("Failed to get admin secret"))
			Expect(createdRemoteUnleash.IsReady()).To(BeFalse())
		})
	})
})
