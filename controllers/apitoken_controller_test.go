package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	existingTokens := unleashclient.ApiTokenResult{}

	const (
		ApiTokenNamespace = "default"
		ApiTokenServerURL = "http://api-token-unleash.nais.io"
		ApiTokenSecret    = "*:*.be44368985f7fb3237c584ef86f3d6bdada42ddbd63a019d26955178"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		existingTokens = unleashclient.ApiTokenResult{}

		httpmock.Activate()
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, `{"versionOSS": "v5.1.2"}`))
		httpmock.RegisterResponder("GET", unleashclient.ApiTokensEndpoint,
			func(req *http.Request) (*http.Response, error) {
				defer GinkgoRecover()
				resp, err := httpmock.NewJsonResponse(200, existingTokens)

				if err != nil {
					return httpmock.NewStringResponse(500, ""), nil
				}
				return resp, nil
			})
		httpmock.RegisterResponder("POST", unleashclient.ApiTokensEndpoint,
			func(req *http.Request) (*http.Response, error) {
				defer GinkgoRecover()

				tokenRequest := unleashclient.ApiTokenRequest{}
				if err := json.NewDecoder(req.Body).Decode(&tokenRequest); err != nil {
					return httpmock.NewStringResponse(400, ""), nil
				}

				existingTokens.Tokens = append(existingTokens.Tokens, unleashclient.ApiToken{
					Secret:      ApiTokenSecret,
					Username:    tokenRequest.Username,
					Type:        tokenRequest.Type,
					Environment: tokenRequest.Environment,
					Project:     tokenRequest.Project,
					Projects:    tokenRequest.Projects,
					ExpiresAt:   tokenRequest.ExpiresAt,
					CreatedAt:   time.Now().Format(time.RFC3339),
				})

				resp, err := httpmock.NewJsonResponse(201, existingTokens.Tokens[len(existingTokens.Tokens)-1])

				if err != nil {
					return httpmock.NewStringResponse(500, ""), nil
				}
				return resp, nil
			})
		httpmock.RegisterResponder("DELETE", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint),
			func(req *http.Request) (*http.Response, error) {
				defer GinkgoRecover()

				urlPath := strings.Split(req.URL.Path, "/")
				tokenName := urlPath[len(urlPath)-1]
				fmt.Printf("Deleting token %s\n", tokenName)

				for i, token := range existingTokens.Tokens {
					if token.Username == tokenName {
						fmt.Printf("Deleting token %s\n", tokenName)
						existingTokens.Tokens = append(existingTokens.Tokens[:i], existingTokens.Tokens[i+1:]...)
						break
					}
				}

				return httpmock.NewStringResponse(200, ""), nil
			})
	})

	AfterEach(func() {
		httpmock.DeactivateAndReset()
	})

	Context("Missing Unleash Server", func() {
		It("Should fail when Unleash does not exist", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-unleash-fail"

			By("By creating a new ApiToken")
			unleash := unleashResource("test-unleash-not-exist", ApiTokenNamespace, unleashv1.UnleashSpec{})
			apiToken := unleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleash)
			Expect(k8sClient.Create(ctx, apiToken)).Should(Succeed())

			apiTokenCreated := &unleashv1.ApiToken{ObjectMeta: apiToken.ObjectMeta}
			Eventually(getApiToken, timeout, interval).WithArguments(k8sClient, ctx, apiTokenCreated).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "Unleash resource with name test-unleash-not-exist not found in namespace default",
			}))

			Expect(apiTokenCreated.Status.Created).Should(Equal(false))
			Expect(apiTokenCreated.Status.Failed).Should(Equal(true))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(0.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(1.0))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})

		It("Should fail when RemoteUnleash does not exist", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-fail"

			By("By creating a new ApiToken")
			secret := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, "test")
			_, remoteUnleash := remoteUnleashResource("test-remoteunleash-not-exist", ApiTokenNamespace, ApiTokenServerURL, secret)
			apiToken := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, remoteUnleash)
			Expect(k8sClient.Create(ctx, apiToken)).Should(Succeed())

			apiTokenCreated := &unleashv1.ApiToken{ObjectMeta: apiToken.ObjectMeta}
			Eventually(getApiToken, timeout, interval).WithArguments(k8sClient, ctx, apiTokenCreated).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.ApiTokenStatusConditionTypeFailed,
				Status:  metav1.ConditionTrue,
				Reason:  "UnleashNotFound",
				Message: "RemoteUnleash resource with name test-remoteunleash-not-exist not found in namespace default",
			}))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(0.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(1.0))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})
	})

	Context("When creating a new ApiToken", func() {
		PIt("Should succeed when it can create token for Unleash", func() {

		})

		It("Should succeed when it can create token for RemoteUnleash", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-success"
			apiTokenLookup := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("By creating a new RemoteUnleash")
			secretCreated := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, ApiTokenSecret)
			Expect(k8sClient.Create(ctx, secretCreated)).Should(Succeed())

			unleashKey, unleashCreated := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, secretCreated)
			Expect(k8sClient.Create(ctx, unleashCreated)).Should(Succeed())
			Eventually(remoteUnleashEventually(ctx, unleashKey, unleashCreated), timeout, interval).Should(ContainElement(remoteUnleashSuccessCondition()))

			By("By creating a new ApiToken")
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(apiTokenCreated.Status.Created).Should(Equal(true))
			Expect(apiTokenCreated.Status.Failed).Should(Equal(false))

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)).Should(Succeed())

			Expect(apiTokenSecretCreated.Data).Should(HaveKey(unleashv1.ApiTokenSecretTokenEnv))
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretTokenEnv]).ShouldNot(BeEmpty())
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretTokenEnv]).Should(Equal([]byte(ApiTokenSecret)))

			Expect(apiTokenSecretCreated.Data).Should(HaveKey(unleashv1.ApiTokenSecretServerEnv))
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretServerEnv]).ShouldNot(BeEmpty())
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretServerEnv]).Should(Equal([]byte(ApiTokenServerURL)))

			Expect(apiTokenSecretCreated.Data).Should(HaveKey(unleashv1.ApiTokenSecretEnvEnv))
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretEnvEnv]).ShouldNot(BeEmpty())
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretEnvEnv]).Should(Equal([]byte("development")))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(1.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(0.0))

			By("By deleting the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(func() int {
				info := httpmock.GetCallCountInfo()
				return info[fmt.Sprintf("DELETE %s", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint))]
			}, timeout, interval).ShouldNot(BeZero())
			Expect(existingTokens.Tokens).Should(BeEmpty())
		})

		It("Should create secret when ApiToken exists in Unleash but not in Kubernetes", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-exists"
			apiTokenLookup := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("By creating a new RemoteUnleash")
			secretCreated := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, ApiTokenSecret)
			Expect(k8sClient.Create(ctx, secretCreated)).Should(Succeed())

			unleashKey, unleashCreated := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, secretCreated)
			Expect(k8sClient.Create(ctx, unleashCreated)).Should(Succeed())
			Eventually(remoteUnleashEventually(ctx, unleashKey, unleashCreated), timeout, interval).Should(ContainElement(remoteUnleashSuccessCondition()))

			By("By creating a new ApiToken")
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			existingTokens.Tokens = []unleashclient.ApiToken{
				{
					Secret:      ApiTokenSecret,
					Username:    apiTokenCreated.ApiTokenName("unleasherator"),
					Type:        apiTokenCreated.Spec.Type,
					Environment: apiTokenCreated.Spec.Environment,
					Projects:    apiTokenCreated.Spec.Projects,
					ExpiresAt:   time.Now().AddDate(0, 0, 1).Format(time.RFC3339),
					CreatedAt:   time.Now().Format(time.RFC3339),
				},
			}
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(existingTokens.Tokens).Should(HaveLen(1))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(0))

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)).Should(Succeed())

			By("By deleting the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})
	})

	Context("When updating an existing ApiToken", func() {
		It("Should update ApiToken in Unleash when it differs from Kubernetes", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-updated"
			apiTokenLookup := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("By creating a new RemoteUnleash")
			secretCreated := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, ApiTokenSecret)
			Expect(k8sClient.Create(ctx, secretCreated)).Should(Succeed())
			unleashKey, unleashCreated := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, secretCreated)
			Expect(k8sClient.Create(ctx, unleashCreated)).Should(Succeed())
			Eventually(remoteUnleashEventually(ctx, unleashKey, unleashCreated), timeout, interval).Should(ContainElement(remoteUnleashSuccessCondition()))

			By("By creating a new ApiToken")
			apiTokenCreated := remoteUnleashApiTokenResourceWithEnv(apiTokenName, ApiTokenNamespace, apiTokenName, "development", unleashCreated)
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))

			By("By updating the ApiToken")
			apiTokenCreated.Spec.Environment = "production"
			Expect(k8sClient.Update(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))

			By("By checking that the ApiToken was updated in Unleash")
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(2))
			Expect(existingTokens.Tokens[0].Environment).Should(Equal("production"))

			By("By deleting the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})
	})
})
