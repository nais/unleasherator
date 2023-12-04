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
	"sigs.k8s.io/controller-runtime/pkg/client"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/pkg/unleashclient"
)

func getApiToken(k8sClient client.Client, ctx context.Context, apiToken *unleashv1.ApiToken) ([]metav1.Condition, error) {
	if err := k8sClient.Get(ctx, apiToken.NamespacedName(), apiToken); err != nil {
		return nil, err
	}

	return unsetConditionLastTransitionTime(apiToken.Status.Conditions), nil
}

var _ = Describe("ApiToken controller", func() {
	const (
		ApiTokenNamespace = "default"
		ApiTokenServerURL = "http://api-token-unleash.nais.io"
		ApiTokenToken     = "test"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When creating an ApiToken", func() {
		It("Should fail when Unleash does not exist", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-unleash-fail"

			By("By creating a new ApiToken")
			unleash := unleashResource("test-unleash-not-exist", ApiTokenNamespace, unleashv1.UnleashSpec{})
			apiToken := unleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleash)
			Expect(k8sClient.Create(ctx, apiToken)).Should(Succeed())

			createdApiToken := &unleashv1.ApiToken{ObjectMeta: apiToken.ObjectMeta}
			Eventually(getApiToken, timeout, interval).WithArguments(k8sClient, ctx, createdApiToken).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "Unleash resource with name test-unleash-not-exist not found in namespace default",
			}))

			Expect(createdApiToken.Status.Created).Should(Equal(false))
			Expect(createdApiToken.Status.Failed).Should(Equal(true))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(0.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(1.0))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, createdApiToken)).Should(Succeed())
		})

		It("Should fail when RemoteUnleash does not exist", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-fail"

			By("By creating a new ApiToken")
			secret := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, "test")
			_, remoteUnleash := remoteUnleashResource("test-remoteunleash-not-exist", ApiTokenNamespace, ApiTokenServerURL, secret)
			apiToken := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, remoteUnleash)
			Expect(k8sClient.Create(ctx, apiToken)).Should(Succeed())

			createdApiToken := &unleashv1.ApiToken{ObjectMeta: apiToken.ObjectMeta}
			Eventually(getApiToken, timeout, interval).WithArguments(k8sClient, ctx, createdApiToken).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "RemoteUnleash resource with name test-remoteunleash-not-exist not found in namespace default",
			}))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(0.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(1.0))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, createdApiToken)).Should(Succeed())
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
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))
			httpmock.RegisterResponder("GET", unleashclient.ApiTokensEndpoint,
				httpmock.NewStringResponder(200, `{"tokens": []}`))
			httpmock.RegisterResponder("POST", unleashclient.ApiTokensEndpoint,
				func(req *http.Request) (*http.Response, error) {
					defer GinkgoRecover()

					tokenRequest := unleashclient.ApiTokenRequest{}
					if err := json.NewDecoder(req.Body).Decode(&tokenRequest); err != nil {
						return httpmock.NewStringResponse(400, ""), nil
					}

					Expect(tokenRequest.Username).Should(Equal(fmt.Sprintf("%s-%s", apiTokenName, ApiTokenNameSuffix)))

					resp, err := httpmock.NewJsonResponse(201, unleashclient.ApiToken{
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

			Expect(createdApiToken.Status.Created).Should(Equal(true))
			Expect(createdApiToken.Status.Failed).Should(Equal(false))

			By("By checking that the ApiToken secret has been created")
			createdApiTokenSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookupKey, createdApiTokenSecret)).Should(Succeed())

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretTokenEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretTokenEnv]).Should(Equal([]byte(apiTokenSecret)))

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretServerEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretServerEnv]).Should(Equal([]byte(ApiTokenServerURL)))

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretEnvEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretEnvEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretEnvEnv]).Should(Equal([]byte("development")))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(1.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(0.0))

			By("By deleting the ApiToken")
			deletePath := fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint)
			httpmock.RegisterResponder("DELETE", deletePath, httpmock.NewStringResponder(200, ""))
			Expect(k8sClient.Delete(ctx, createdApiToken)).Should(Succeed())
			Eventually(func() int {
				info := httpmock.GetCallCountInfo()
				return info[fmt.Sprintf("DELETE %s", deletePath)]
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
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(200, `{"versionOSS": "v4.0.0"}`))
			httpmock.RegisterResponder("GET", unleashclient.ApiTokensEndpoint,
				func(req *http.Request) (*http.Response, error) {
					resp, err := httpmock.NewJsonResponse(200, unleashclient.ApiTokenResult{
						Tokens: []unleashclient.ApiToken{
							{
								Secret:      apiTokenSecret,
								Username:    fmt.Sprintf("%s-%s", apiTokenName, ApiTokenNameSuffix),
								Type:        "client",
								Environment: "development",
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

			Expect(createdApiTokenSecret.Data).Should(HaveKey(unleashv1.ApiTokenSecretEnvEnv))
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretEnvEnv]).ShouldNot(BeEmpty())
			Expect(createdApiTokenSecret.Data[unleashv1.ApiTokenSecretEnvEnv]).Should(Equal([]byte("development")))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(1.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(0.0))

			By("By deleting the ApiToken")
			deletePath := fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint)
			httpmock.RegisterResponder("DELETE", deletePath, httpmock.NewStringResponder(200, ""))
			Expect(k8sClient.Delete(ctx, createdApiToken)).Should(Succeed())
			Eventually(func() int {
				info := httpmock.GetCallCountInfo()
				return info[fmt.Sprintf("DELETE %s", deletePath)]
			}, timeout, interval).ShouldNot(BeZero())
		})
	})
})
