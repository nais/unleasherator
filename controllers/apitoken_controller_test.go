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

var _ = Describe("Api token controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		name       = "test-unleash-apitoken"
		namespace  = "default"
		secretName = "api-token-secret"
		timeout    = time.Second * 10
		duration   = time.Second * 10
		interval   = time.Millisecond * 250
	)

	Context("When updating api token Status", func() {
		ctx := context.Background()
		apiToken := &unleashv1.ApiToken{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "unleash.nais.io/v1",
				Kind:       "Unleash",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: unleashv1.ApiTokenSpec{
				UnleashName: name,
				SecretName:  secretName,
			},
		}

		key := types.NamespacedName{Name: name, Namespace: namespace}

		It("Should set the secret name when new tokens are created", func() {

			By("By creating a new api-token")
			Expect(k8sClient.Create(ctx, apiToken)).Should(Succeed())
			createToken := &unleashv1.ApiToken{}
			Eventually(func() error {
				err := k8sClient.Get(ctx, key, createToken)
				return err
			}, timeout, interval).Should(Succeed())
			Expect(createToken.Spec.SecretName).Should(Equal(secretName))

			By("Eventually the status gets set") //TODO: Surely this one should not fail, controller L90
			f := &unleashv1.ApiToken{}

			Eventually(func() []metav1.Condition {
				k8sClient.Get(context.Background(), key, f)
				return f.Status.Conditions
			}, timeout, interval).ShouldNot(BeEmpty())

		})
		Context("When setting DeletionTimeStamp", func() {
			It("Deletion should work", func() {
				f := &unleashv1.ApiToken{}

				By("Eventually gets deleted, once a deletion timestamp exists")
				deletionTime := &metav1.Time{Time: time.Now().UTC()}

				// set the deletion timestamp in the object
				apiToken.SetDeletionTimestamp(deletionTime)

				k8sClient.Update(context.Background(), apiToken)
				Eventually(func() error {
					return k8sClient.Get(context.Background(), key, f)
				}, timeout, interval).Should(Succeed())

			})
		})
	})

})
