package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/unleash"
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
			ctx := context.Background()

			apiTokenName := "test-apitoken-unleash-fail"
			apiTokenLookupKey := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

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
						Kind:       "Unleash",
						Name:       "test-unleash-not-exist",
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
				Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "Unleash resource with name test-unleash-not-exist not found in namespace default",
			}))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, &createdApiToken)).Should(Succeed())
		})

		It("Should fail when RemoteUnleash does not exist", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-fail"
			apiTokenLookupKey := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

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
						Name:       "test-remoteunleash-not-exist",
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
				Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "RemoteUnleash resource with name test-remoteunleash-not-exist not found in namespace default",
			}))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, &createdApiToken)).Should(Succeed())
		})

		PIt("Should succeed when it can create token for Unleash", func() {

		})

		It("Should succeed when it can create token for RemoteUnleash", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-success"
			apiTokenLookupKey := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}
			apiTokenSecret := "*:*.be44368985f7fb3237c584ef86f3d6bdada42ddbd63a019d26955178"

			By("Mocking RemoteUnleash endpoints")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", fmt.Sprintf("%s/health", ApiTokenServerURL),
				httpmock.NewStringResponder(200, `{"health": "GOOD"}`))
			httpmock.RegisterResponder("GET", fmt.Sprintf("%s/api/admin/api-tokens", ApiTokenServerURL),
				httpmock.NewStringResponder(200, `{"tokens": []}`))
			httpmock.RegisterResponder("POST", fmt.Sprintf("%s/api/admin/api-tokens", ApiTokenServerURL),
				func(req *http.Request) (*http.Response, error) {
					defer GinkgoRecover()

					tokenRequest := unleash.ApiTokenRequest{}
					if err := json.NewDecoder(req.Body).Decode(&tokenRequest); err != nil {
						return httpmock.NewStringResponse(400, ""), nil
					}

					Expect(tokenRequest.Username).Should(Equal(fmt.Sprintf("%s-%s", apiTokenName, ApiTokenNameSuffix)))

					resp, err := httpmock.NewJsonResponse(201, unleash.ApiToken{
						Secret:      apiTokenSecret,
						Username:    tokenRequest.Username,
						Type:        tokenRequest.Type,
						Environment: tokenRequest.Environment,
						Project:     tokenRequest.Project,
						Projects:    tokenRequest.Projects,
						ExpiresAt:   tokenRequest.ExpiresAt,
						CreatedAt:   time.Now().Format("2006-01-02T15:04:05.999Z"),
					})

					if err != nil {
						return httpmock.NewStringResponse(500, ""), nil
					}
					return resp, nil
				})

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
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
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
				Type:    unleashv1.ApiTokenStatusConditionTypeCreated,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "API token successfully created",
			}))

			By("By checking that the ApiToken secret has been created")
			createdApiTokenSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookupKey, createdApiTokenSecret)).Should(Succeed())

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretTokenEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).Should(Equal([]byte(apiTokenSecret)))

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretServerEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).Should(Equal([]byte(ApiTokenServerURL)))

			By("Cleaning up the ApiToken")
			httpmock.RegisterResponder("DELETE", "=~http://unleash.nais.io/api/admin/api-tokens/.*", httpmock.NewStringResponder(200, ""))
			Expect(k8sClient.Delete(ctx, &createdApiToken)).Should(Succeed())
			Eventually(func() int {
				info := httpmock.GetCallCountInfo()
				return info["DELETE =~http://unleash.nais.io/api/admin/api-tokens/.*"]
			}, timeout, interval).ShouldNot(BeZero())
		})

		It("Should create secret when ApiToken exists in Unleash but not in Kubernetes", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-exists"
			apiTokenLookupKey := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}
			apiTokenSecret := "*:*.be44368985f7fb3237c584ef86f3d6bdada42ddbd63a019d26955178"

			By("Mocking RemoteUnleash endpoints")
			httpmock.Activate()
			defer httpmock.DeactivateAndReset()
			httpmock.RegisterResponder("GET", fmt.Sprintf("%s/health", ApiTokenServerURL),
				httpmock.NewStringResponder(200, `{"health": "GOOD"}`))
			httpmock.RegisterResponder("GET", fmt.Sprintf("%s/api/admin/api-tokens", ApiTokenServerURL),
				func(req *http.Request) (*http.Response, error) {
					resp, err := httpmock.NewJsonResponse(200, unleash.ApiTokenResult{
						Tokens: []unleash.ApiToken{
							{
								Secret:      apiTokenSecret,
								Username:    fmt.Sprintf("%s-%s", apiTokenName, ApiTokenNameSuffix),
								Type:        "client",
								Environment: "*",
								Project:     "*",
								Projects:    []string{},
								ExpiresAt:   time.Now().UTC().AddDate(0, 0, 1).Format(time.RFC3339),
								CreatedAt:   time.Now().UTC().Format(time.RFC3339),
							},
						},
					})
					if err != nil {
						return httpmock.NewStringResponse(500, ""), nil
					}
					return resp, nil
				})

			By("By creating a new RemoteUnleash")
			createdRemoteUnleashSecret := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, apiTokenSecret)
			Expect(k8sClient.Create(ctx, createdRemoteUnleashSecret)).Should(Succeed())

			remoteUnleashLookupKey, createdRemoteUnleash := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, createdRemoteUnleashSecret)
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
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash",
			}))

			By("By creating a new ApiToken")
			createdApiToken := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, createdRemoteUnleash)
			Expect(k8sClient.Create(ctx, createdApiToken)).Should(Succeed())

			Eventually(func() ([]metav1.Condition, error) {
				err := k8sClient.Get(ctx, apiTokenLookupKey, createdApiToken)
				if err != nil {
					return nil, err
				}

				// unset condition.LastTransitionTime to make comparison easier
				unsetConditionLastTransitionTime(createdApiToken.Status.Conditions)

				return createdApiToken.Status.Conditions, nil
			}, timeout, interval).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeCreated,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "API token successfully created",
			}))

			By("By checking that the ApiToken secret has been created")
			createdApiTokenSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookupKey, createdApiTokenSecret)).Should(Succeed())

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretTokenEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).Should(Equal([]byte(apiTokenSecret)))

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretServerEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).Should(Equal([]byte(ApiTokenServerURL)))
		})
	})
})
