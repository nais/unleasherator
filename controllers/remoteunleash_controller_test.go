package controllers

import (
	"context"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/unleashclient"
)

func getRemoteUnleash(k8sClient client.Client, ctx context.Context, createdRemoteUnleash *unleashv1.RemoteUnleash) ([]metav1.Condition, error) {
	if err := k8sClient.Get(ctx, createdRemoteUnleash.NamespacedName(), createdRemoteUnleash); err != nil {
		return nil, err
	}

	return unsetConditionLastTransitionTime(createdRemoteUnleash.Status.Conditions), nil
}

var _ = Describe("RemoteUnleash controller", func() {
	const (
		RemoteUnleashNamespace = "default"
		RemoteUnleashServerURL = "http://remoteunleash.nais.io"
		RemoteUnleashToken     = "test"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a RemoteUnleash", func() {
		It("Should fail if the secret does not exist", func() {
			ctx := context.Background()

			RemoteUnleashName := "test-unleash-fail-secret"

			By("By creating a new RemoteUnleash")
			secret := remoteUnleashSecretResource(RemoteUnleashName, RemoteUnleashNamespace, RemoteUnleashToken)
			_, remoteUnleash := remoteUnleashResource(RemoteUnleashName, RemoteUnleashNamespace, RemoteUnleashServerURL, secret)
			Expect(k8sClient.Create(ctx, remoteUnleash)).Should(Succeed())

			createdRemoteUnleash := &unleashv1.RemoteUnleash{ObjectMeta: remoteUnleash.ObjectMeta}
			Eventually(getRemoteUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdRemoteUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
				Message: "Failed to get admin token secret",
			}))
			Expect(createdRemoteUnleash.IsReady()).To(BeFalse())

			var m = &dto.Metric{}
			err := unleashStatus.WithLabelValues(RemoteUnleashNamespace, RemoteUnleashName, "available").Write(m)
			Expect(err).ToNot(HaveOccurred())
			Expect(m.GetGauge().GetValue()).To(Equal(float64(0)))

			By("By deleting the RemoteUnleash")
			Expect(k8sClient.Delete(ctx, createdRemoteUnleash)).Should(Succeed())
		})

		It("Should succeed when it can connect to Unleash", func() {
			ctx := context.Background()
			RemoteUnleashName := "test-unleash-success"

			By("By mocking Unleash API")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))

			By("By creating a new Unleash secret")
			secret := remoteUnleashSecretResource(RemoteUnleashName, RemoteUnleashNamespace, RemoteUnleashToken)
			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

			By("By creating a new RemoteUnleash")
			_, remoteUnleash := remoteUnleashResource(RemoteUnleashName, RemoteUnleashNamespace, RemoteUnleashServerURL, secret)
			Expect(k8sClient.Create(ctx, remoteUnleash)).Should(Succeed())

			createdRemoteUnleash := &unleashv1.RemoteUnleash{ObjectMeta: remoteUnleash.ObjectMeta}
			Eventually(getRemoteUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdRemoteUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash",
			}))

			Expect(createdRemoteUnleash.IsReady()).To(BeTrue())
			Expect(createdRemoteUnleash.Status.Version).To(Equal("v4.0.0"))
			Expect(createdRemoteUnleash.Status.Reconciled).To(BeTrue())
			Expect(createdRemoteUnleash.Status.Connected).To(BeTrue())
		})
	})
})
