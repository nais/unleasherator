package controllers

import (
	"context"
	"fmt"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

var _ = Describe("ApiToken controller", func() {

	// Define utility constants for object names and testing timeouts/durations and intervals.
	const (
		ApiTokenNamespace = "default"
		ApiTokenServerURL = "http://unleash.nais.io"
		ApiTokenToken     = "test"

		timeout  = time.Second * 10
		duration = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating a ApiToken", func() {
		It("Should fail when Unleash does not exist", func() {

		})

		It("Should fail when RemoteUnleash does not exist", func() {

		})

		It("Should succeed when it can create token for Unleash", func() {

		})

		It("Should succeed when it can create token for RemoteUnleash", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-success"
			apiTokenLookupKey := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("Mocking RemoteUnleash endpoints")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", fmt.Sprintf("%s/health", ApiTokenServerURL),
				httpmock.NewStringResponder(200, `{"health": "GOOD"}`))
			httpmock.RegisterResponder("GET", fmt.Sprintf("%s/api/admin/api-tokens", ApiTokenServerURL),
				httpmock.NewStringResponder(200, `{"tokens": []}`))
			httpmock.RegisterResponder("POST", fmt.Sprintf("%s/api/admin/api-tokens", ApiTokenServerURL),
				httpmock.NewStringResponder(201, `{
					"username": "test",
					"tokenName": "test",
					"secret": "*:*.be44368985f7fb3237c584ef86f3d6bdada42ddbd63a019d26955178",
					"type": "client"
				}`))

			By("By creating a new RemoteUnleash")
			createdRemoteUnleashSecret := &corev1.Secret{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Secret",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("unleasherator-%s", apiTokenName),
					Namespace: ApiTokenNamespace,
				},
				Data: map[string][]byte{
					"token": []byte(ApiTokenToken),
				},
			}
			Expect(k8sClient.Create(ctx, createdRemoteUnleashSecret)).Should(Succeed())

			remoteUnleashLookupKey := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}
			createdRemoteUnleash := &unleashv1.RemoteUnleash{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "RemoteUnleash",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      remoteUnleashLookupKey.Name,
					Namespace: remoteUnleashLookupKey.Namespace,
				},
				Spec: unleashv1.RemoteUnleashSpec{
					Server: unleashv1.RemoteUnleashServer{
						URL: ApiTokenServerURL,
					},
					AdminSecret: unleashv1.RemoteUnleashSecret{
						Name: createdRemoteUnleashSecret.Name,
					},
				},
			}
			Expect(k8sClient.Create(ctx, createdRemoteUnleash)).Should(Succeed())

			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, remoteUnleashLookupKey, createdRemoteUnleash)
				if err != nil {
					return nil, err
				}

				// unset condition.LastTransitionTime to make comparison easier
				unsetConditionLastTransitionTime(createdRemoteUnleash.Status.Conditions)

				return createdRemoteUnleash.Status.Conditions, nil
			}, timeout, interval).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnection,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash",
			}))

			By("By creating a new ApiToken")
			createdApiToken := unleashv1.ApiToken{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "unleash.nais.io/v1",
					Kind:       "ApiToken",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      apiTokenName,
					Namespace: ApiTokenNamespace,
				},
				Spec: unleashv1.ApiTokenSpec{
					UnleashInstance: unleashv1.ApiTokenUnleashInstance{
						ApiVersion: "unleash.nais.io/v1",
						Kind:       "RemoteUnleash",
						Name:       createdRemoteUnleash.Name,
					},
					SecretName: apiTokenName,
				},
			}
			Expect(k8sClient.Create(ctx, &createdApiToken)).Should(Succeed())

			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, apiTokenLookupKey, &createdApiToken)
				if err != nil {
					return nil, err
				}

				// unset condition.LastTransitionTime to make comparison easier
				unsetConditionLastTransitionTime(createdApiToken.Status.Conditions)

				return createdApiToken.Status.Conditions, nil
			}, timeout, interval).Should(ContainElement(metav1.Condition{
				Type:    typeCreatedToken,
				Status:  metav1.ConditionTrue,
				Reason:  "CreatedToken",
				Message: "Created token",
			}))

			By("By checking that the ApiToken secret has been created")
			createdApiTokenSecret := &corev1.Secret{}
			k8sClient.Get(ctx, apiTokenLookupKey, createdApiTokenSecret)

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretTokenEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).ShouldNot(BeEmpty())

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretServerEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).ShouldNot(BeEmpty())
		})
	})
})
