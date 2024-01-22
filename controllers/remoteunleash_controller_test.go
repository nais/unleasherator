package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/federation"
	"github.com/nais/unleasherator/pkg/pb"
	"github.com/nais/unleasherator/pkg/unleashclient"
)

func getRemoteUnleash(k8sClient client.Client, ctx context.Context, remoteUnleash *unleashv1.RemoteUnleash) ([]metav1.Condition, error) {
	if err := k8sClient.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash); err != nil {
		return nil, err
	}

	return unsetConditionLastTransitionTime(remoteUnleash.Status.Conditions), nil
}

var _ = Describe("RemoteUnleash controller", func() {
	const (
		RemoteUnleashNamespace = "default"
		RemoteUnleashServerURL = "http://remoteunleash.nais.io"
		RemoteUnleashToken     = "test"
		RemoteUnleashVersion   = "v5.1.2"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		promCounterVecFlush(remoteUnleashReceived)

		httpmock.Activate()
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, RemoteUnleashVersion)))
	})

	AfterEach(func() {
		httpmock.DeactivateAndReset()
	})

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
			Expect(promGaugeVecVal(remoteUnleashStatus, RemoteUnleashNamespace, RemoteUnleashName, unleashv1.UnleashStatusConditionTypeReconciled)).To(Equal(0.0))
			Expect(promGaugeVecVal(remoteUnleashStatus, RemoteUnleashNamespace, RemoteUnleashName, unleashv1.UnleashStatusConditionTypeConnected)).To(Equal(0.0))

			By("By deleting the RemoteUnleash")
			Expect(k8sClient.Delete(ctx, createdRemoteUnleash)).Should(Succeed())
		})

		PIt("Should fail if the secret does not contain a token")
		PIt("Should fail if it cannot connect to Unleash")

		It("Should succeed when it can connect to Unleash", func() {
			ctx := context.Background()
			RemoteUnleashName := "test-unleash-success"

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
			Expect(createdRemoteUnleash.Status.Version).To(Equal(RemoteUnleashVersion))
			Expect(createdRemoteUnleash.Status.Reconciled).To(BeTrue())
			Expect(createdRemoteUnleash.Status.Connected).To(BeTrue())

			Expect(promGaugeVecVal(remoteUnleashStatus, RemoteUnleashNamespace, RemoteUnleashName, unleashv1.UnleashStatusConditionTypeReconciled)).To(Equal(1.0))
			Expect(promGaugeVecVal(remoteUnleashStatus, RemoteUnleashNamespace, RemoteUnleashName, unleashv1.UnleashStatusConditionTypeConnected)).To(Equal(1.0))

			Expect(httpmock.GetCallCountInfo()).To(HaveLen(1))
			// This should ideally be 1, but due "object has been modified; please apply your changes to the latest version and try again" error
			// it can be 2 or more as the reconciler retries.
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.InstanceAdminStatsEndpoint)]).ToNot(Equal(0))
		})
	})

	Context("When subscribing to federated Unleash", func() {
		It("Should create RemoteUnleash if cluster matches", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			By("By starting the subscriber")
			started := make(chan bool)
			mockSubscriber.On("Subscribe", ctx, mock.AnythingOfType("Handler")).After(10 * time.Second).Return(nil)

			go func(ctx context.Context) {
				started <- true

				err := remoteUnleashReconciler.FederationSubscribe(ctx)
				Expect(err).ToNot(HaveOccurred(), "failed to subscribe to federation")
			}(ctx)

			<-started

			Eventually(func() int {
				return len(mockSubscriber.Calls)
			}, timeout, interval).Should(Equal(1))

			handler := mockSubscriber.Calls[0].Arguments.Get(1).(federation.Handler)

			var remoteUnleashes []*unleashv1.RemoteUnleash

			By("By creating a new RemoteUnleash that does not match cluster")
			name := "test-unleash-other-cluster"
			namespaces := []string{"some-namespace"}
			clusters := []string{"some-cluster"}
			secret := remoteUnleashSecretResource(name, namespaces[0], RemoteUnleashToken)
			_, remoteUnleash := remoteUnleashResource(name, namespaces[0], RemoteUnleashServerURL, secret)
			remoteUnleashes = []*unleashv1.RemoteUnleash{remoteUnleash}

			err := handler(ctx, remoteUnleashes, secret, clusters, pb.Status_Provisioned)
			Expect(err).ToNot(HaveOccurred())

			Expect(k8sClient.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash)).ShouldNot(Succeed())

			By("By creating a new RemoteUnleash that matches cluster")
			name = "test-unleash-same-cluster"
			namespaces = []string{"default"}
			clusters = []string{"test-cluster"}
			secret = remoteUnleashSecretResource(name, namespaces[0], RemoteUnleashToken)
			_, remoteUnleash = remoteUnleashResource(name, namespaces[0], RemoteUnleashServerURL, secret)
			remoteUnleashes = []*unleashv1.RemoteUnleash{remoteUnleash}

			err = handler(ctx, remoteUnleashes, secret, clusters, pb.Status_Provisioned)
			Expect(err).ToNot(HaveOccurred())

			Expect(k8sClient.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash)).Should(Succeed())
			Expect(promCounterVecVal(remoteUnleashReceived, "provisioned", "success")).To(Equal(1.0))

			By("By updating an existing RemoteUnleash that matches cluster")
			name = "test-unleash-same-cluster"
			namespaces = []string{"default"}
			clusters = []string{"test-cluster"}
			secret = remoteUnleashSecretResource(name, namespaces[0], RemoteUnleashToken)
			_, remoteUnleash = remoteUnleashResource(name, namespaces[0], RemoteUnleashServerURL, secret)
			remoteUnleashes = []*unleashv1.RemoteUnleash{remoteUnleash}

			Eventually(func() error {
				return handler(ctx, remoteUnleashes, secret, clusters, pb.Status_Provisioned)
			}, timeout, interval).ShouldNot(HaveOccurred())

			Expect(k8sClient.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash)).Should(Succeed())
			Expect(promCounterVecVal(remoteUnleashReceived, "provisioned", "success")).To(Equal(2.0))
		})

		It("Should handle RemoteUnleash namespace not existing", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			By("By starting the subscriber")
			started := make(chan bool)
			mockSubscriber.On("Subscribe", ctx, mock.AnythingOfType("Handler")).After(10 * time.Second).Return(nil)

			go func(ctx context.Context) {
				started <- true

				err := remoteUnleashReconciler.FederationSubscribe(ctx)
				Expect(err).ToNot(HaveOccurred(), "failed to subscribe to federation")
			}(ctx)

			<-started

			Eventually(func() int {
				return len(mockSubscriber.Calls)
			}, timeout, interval).Should(Not(Equal(0)))

			handler := mockSubscriber.Calls[0].Arguments.Get(1).(federation.Handler)

			var remoteUnleashes []*unleashv1.RemoteUnleash

			By("By creating a new RemoteUnleash in a namespace that does not exist")
			name := "test-unleash-namespaces-not-found"
			namespaces := []string{"namespace-not-found"}
			clusters := []string{"test-cluster"}
			secret := remoteUnleashSecretResource(name, "default", RemoteUnleashToken)
			_, remoteUnleash := remoteUnleashResource(name, namespaces[0], RemoteUnleashServerURL, secret)
			remoteUnleashes = []*unleashv1.RemoteUnleash{remoteUnleash}

			err := handler(ctx, remoteUnleashes, secret, clusters, pb.Status_Provisioned)
			Expect(err).ToNot(HaveOccurred())

			Expect(k8sClient.Get(ctx, remoteUnleash.NamespacedName(), remoteUnleash)).ShouldNot(Succeed())
			Expect(promCounterVecVal(remoteUnleashReceived, "provisioned", "failed")).To(Equal(1.0))
		})
	})
})
