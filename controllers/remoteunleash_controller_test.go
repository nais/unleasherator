package controllers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

var _ = Describe("RemoteUnleash controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		RemoteUnleashNamespace = "default"
		RemoteUnleashServerURL = "http://unleash.nais.io"
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
						URL: RemoteUnleashServerURL,
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
			Expect(createdRemoteUnleash.Status.Conditions[1].Message).To(Equal("Failed to get admin token secret"))
			Expect(createdRemoteUnleash.IsReady()).To(BeFalse())

			By("By deleting the RemoteUnleash")
			Expect(k8sClient.Delete(ctx, createdRemoteUnleash)).Should(Succeed())
		})

		It("Should succeed when it can connect to Unleash", func() {
			// Mock Unleash server with a health endpoint
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/health" {
					w.WriteHeader(http.StatusNotFound)
				}

				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"health": "GOOD"}`))
			}))
			defer srv.Close()

			ctx := context.Background()
			RemoteUnleashName := "test-unleash-success"

			By("By creating a new Unleash secret")
			secret := &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Secret",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleasherator-test",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"INIT_ADMIN_API_TOKENS": []byte(RemoteUnleashToken),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

			By("By creating a new RemoteUnleash")
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
						URL: srv.URL,
					},
					AdminSecret: unleashv1.RemoteUnleashSecret{
						Name: secret.GetName(),
					},
				},
			}
			Expect(k8sClient.Create(ctx, remoteUnleash)).Should(Succeed())

			remoteUnleashLookupKey := types.NamespacedName{Name: RemoteUnleashName, Namespace: RemoteUnleashNamespace}
			createdRemoteUnleash := &unleashv1.RemoteUnleash{}

			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, remoteUnleashLookupKey, createdRemoteUnleash)
				if err != nil {
					return nil, err
				}
				return createdRemoteUnleash.Status.Conditions, nil
			}, timeout, interval).Should(HaveLen(2))

			Expect(createdRemoteUnleash.Status.Conditions[1].Message).To(Equal("Reconciled successfully"))
			Expect(createdRemoteUnleash.Status.Conditions[1].Type).To(Equal(typeAvailableUnleash))
			Expect(createdRemoteUnleash.Status.Conditions[1].Status).To(Equal(metav1.ConditionTrue))
			Expect(createdRemoteUnleash.Status.Conditions[1].Reason).To(Equal("Reconciling"))
			Expect(createdRemoteUnleash.IsReady()).To(BeTrue())
		})
	})
})
