package controller

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
	"github.com/nais/unleasherator/internal/unleashclient"
)

func getApiToken(k8sClient client.Client, ctx context.Context, apiToken *unleashv1.ApiToken) ([]metav1.Condition, error) {
	if err := k8sClient.Get(ctx, apiToken.NamespacedName(), apiToken); err != nil {
		return nil, err
	}

	return unsetConditionLastTransitionTime(apiToken.Status.Conditions), nil
}

var _ = Describe("ApiToken controller", Ordered, func() {
	const (
		ApiTokenNamespace = "default"
		ApiTokenServerURL = "http://api-token-unleash.nais.io"
		ApiTokenSecret    = "*:*.be44368985f7fb3237c584ef86f3d6bdada42ddbd63a019d26955178"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var existingTokens = unleashclient.ApiTokenResult{
		Tokens: []unleashclient.ApiToken{},
	}

	BeforeEach(func() {
		existingTokens.Tokens = []unleashclient.ApiToken{}

		httpmock.Activate()
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, `{"versionOSS": "v5.1.2"}`))
		httpmock.RegisterResponder("GET", fmt.Sprintf("=~^%s/.+\\z", unleashclient.ApiTokensEndpoint),
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

				var tokenReq unleashclient.ApiTokenRequest
				err := json.NewDecoder(req.Body).Decode(&tokenReq)
				Expect(err).ToNot(HaveOccurred())

				tokenResp := unleashclient.ApiToken{
					Secret:      ApiTokenSecret,
					TokenName:   tokenReq.Username,
					Type:        strings.ToLower(tokenReq.Type),
					Environment: tokenReq.Environment,
					Projects:    tokenReq.Projects,
					CreatedAt:   time.Now().Format(time.RFC3339),
				}

				existingTokens.Tokens = append(existingTokens.Tokens, tokenResp)

				return httpmock.NewJsonResponse(201, tokenResp)
			})
		httpmock.RegisterResponder("DELETE", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint),
			func(req *http.Request) (*http.Response, error) {
				defer GinkgoRecover()

				urlPath := strings.Split(req.URL.Path, "/")
				tokenSecret := urlPath[len(urlPath)-1]

				for i, token := range existingTokens.Tokens {
					if token.Secret == tokenSecret {
						existingTokens.Tokens = append(existingTokens.Tokens[:i], existingTokens.Tokens[i+1:]...)
						return httpmock.NewStringResponse(200, ""), nil
					}
				}

				Fail("Unknown token was attempted to be deleted")
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
			Expect(apiTokenCreated.Status.Created).Should(Equal(false))
			Expect(apiTokenCreated.Status.Failed).Should(Equal(true))

			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeCreated)).Should(Equal(0.0))
			Expect(promGaugeVecVal(apiTokenStatus, ApiTokenNamespace, apiTokenName, unleashv1.ApiTokenStatusConditionTypeFailed)).Should(Equal(1.0))

			By("Cleaning up the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})
	})

	Context("Invalid ApiToken", func() {
		PIt("Should fail for invalid ApiToken type")
		PIt("Should fail for non-existing ApiToken environment")
		PIt("Should fail for non-existing ApiToken project")
	})

	Context("When creating a new ApiToken", func() {
		PIt("Should succeed when it can create token for Unleash")

		It("Should succeed when it can create token for RemoteUnleash", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-remoteunleash-success"
			apiTokenLookup := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("By creating a new RemoteUnleash")
			secretCreated := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, ApiTokenSecret)
			Expect(k8sClient.Create(ctx, secretCreated)).Should(Succeed())

			unleashKey, unleashCreated := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, secretCreated)
			Eventually(func() error {
				return k8sClient.Create(ctx, unleashCreated)
			}, timeout, interval).Should(Succeed())
			Eventually(remoteUnleashEventually(ctx, unleashKey, unleashCreated), timeout, interval).Should(ContainElement(remoteUnleashSuccessCondition()))

			By("By creating a new ApiToken")
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(apiTokenCreated.Spec).Should(Equal(unleashv1.ApiTokenSpec{
				UnleashInstance: unleashv1.ApiTokenUnleashInstance{
					Name:       unleashCreated.Name,
					Kind:       "RemoteUnleash",
					ApiVersion: "unleash.nais.io/v1",
				},
				SecretName:  apiTokenName,
				Type:        "CLIENT",
				Environment: "development",
				Projects:    []string{"default"},
			}))

			Eventually(func(g Gomega) {
				info := httpmock.GetCallCountInfo()
				g.Expect(info[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).To(Equal(1))
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(existingTokens.Tokens).To(HaveLen(1))
				g.Expect(existingTokens.Tokens[0].TokenName).To(Equal(apiTokenCreated.ApiTokenName("unleasherator")))
				g.Expect(existingTokens.Tokens[0].Type).To(Equal("client"))
				g.Expect(existingTokens.Tokens[0].Environment).To(Equal("development"))
				g.Expect(existingTokens.Tokens[0].Projects).To(Equal([]string{"default"}))
			}, timeout, interval).Should(Succeed())

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)).To(Succeed())
				g.Expect(apiTokenSecretCreated.Data).To(Equal(map[string][]byte{
					unleashv1.ApiTokenSecretTokenEnv:    []byte(ApiTokenSecret),
					unleashv1.ApiTokenSecretTypeEnv:     []byte("CLIENT"),
					unleashv1.ApiTokenSecretServerEnv:   []byte(ApiTokenServerURL),
					unleashv1.ApiTokenSecretEnvEnv:      []byte("development"),
					unleashv1.ApiTokenSecretProjectsEnv: []byte("default"),
				}))
			}, timeout, interval).Should(Succeed())

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

		It("Should create token with custom environment and project", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-custom"
			apiTokenLookup := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("By creating a new RemoteUnleash")
			secretCreated := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, ApiTokenSecret)
			Expect(k8sClient.Create(ctx, secretCreated)).Should(Succeed())

			unleashKey, unleashCreated := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, secretCreated)
			Expect(k8sClient.Create(ctx, unleashCreated)).Should(Succeed())
			Eventually(remoteUnleashEventually(ctx, unleashKey, unleashCreated), timeout, interval).Should(ContainElement(remoteUnleashSuccessCondition()))

			By("By creating a new ApiToken")
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			apiTokenCreated.Spec.Environment = "production"
			apiTokenCreated.Spec.Projects = []string{"project1", "project2", "project3"}
			apiTokenCreated.Spec.Type = "FRONTEND"

			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(apiTokenCreated.Spec.Type).Should(Equal("FRONTEND"))
			Expect(apiTokenCreated.Spec.Environment).Should(Equal("production"))
			Expect(apiTokenCreated.Spec.Projects).Should(Equal([]string{"project1", "project2", "project3"}))

			Eventually(func(g Gomega) {
				info := httpmock.GetCallCountInfo()
				g.Expect(info[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).To(Equal(1))
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(existingTokens.Tokens).To(HaveLen(1))
				g.Expect(existingTokens.Tokens[0].Type).To(Equal("frontend"))
				g.Expect(existingTokens.Tokens[0].Environment).To(Equal("production"))
				g.Expect(existingTokens.Tokens[0].Projects).To(Equal([]string{"project1", "project2", "project3"}))
			}, timeout, interval).Should(Succeed())

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)).To(Succeed())
				g.Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretTypeEnv]).To(Equal([]byte("FRONTEND")))
				g.Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretEnvEnv]).To(Equal([]byte("production")))
				g.Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretProjectsEnv]).To(Equal([]byte("project1,project2,project3")))
			}, timeout, interval).Should(Succeed())

			By("By deleting the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
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
					TokenName:   apiTokenCreated.ApiTokenName("unleasherator"),
					Type:        "CLIENT",
					Environment: "development",
					Project:     "default",
					Projects:    []string{"default"},
					CreatedAt:   time.Now().Format(time.RFC3339),
				},
			}
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(existingTokens.Tokens).Should(HaveLen(1))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(0))

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)
			}, timeout, interval).Should(Succeed())

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
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			apiTokenSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecret)).Should(Succeed())
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(1))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("DELETE %s", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint))]).Should(Equal(0))

			By("By updating the ApiToken with a new environment")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenCreated)).To(Succeed())
				apiTokenCreated.Spec.Environment = "production"
				g.Expect(k8sClient.Update(ctx, apiTokenCreated)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			Eventually(apiTokenSecretEventually(ctx, apiTokenLookup, apiTokenSecret), timeout, interval).Should(ContainElement([]byte("production")))
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(existingTokens.Tokens[0].Environment).Should(Equal("production"))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(2))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("DELETE %s", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint))]).Should(Equal(1))
			Expect(promCounterVecVal(apiTokenDeletedCounter, ApiTokenNamespace, apiTokenName)).Should(Equal(1.0))
			Expect(promCounterVecVal(apiTokenCreatedCounter, ApiTokenNamespace, apiTokenName)).Should(Equal(2.0))
			Eventually(func() error {
				return k8sClient.Get(ctx, apiTokenLookup, apiTokenSecret)
			}, timeout, interval).Should(Succeed())
			Expect(apiTokenSecret.Data[unleashv1.ApiTokenSecretEnvEnv]).Should(Equal([]byte("production")))

			By("By updating the ApiToken with a new project")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenCreated)).To(Succeed())
				apiTokenCreated.Spec.Projects = []string{"project1", "project2", "project3"}
				g.Expect(k8sClient.Update(ctx, apiTokenCreated)).To(Succeed())
			}, timeout, interval).Should(Succeed())

			Eventually(apiTokenSecretEventually(ctx, apiTokenLookup, apiTokenSecret), timeout, interval).Should(ContainElement([]byte("project1,project2,project3")))
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(existingTokens.Tokens[0].Projects).Should(Equal([]string{"project1", "project2", "project3"}))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(3))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("DELETE %s", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint))]).Should(Equal(2))
			Expect(promCounterVecVal(apiTokenDeletedCounter, ApiTokenNamespace, apiTokenName)).Should(Equal(2.0))
			Expect(promCounterVecVal(apiTokenCreatedCounter, ApiTokenNamespace, apiTokenName)).Should(Equal(3.0))
			Eventually(func() error {
				return k8sClient.Get(ctx, apiTokenLookup, apiTokenSecret)
			}, timeout, interval).Should(Succeed())
			Expect(apiTokenSecret.Data[unleashv1.ApiTokenSecretProjectsEnv]).Should(Equal([]byte("project1,project2,project3")))

			By("By deleting the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})
	})

	Context("When dealing with duplicate Unleash tokens", func() {
		It("Should delete duplicate tokens", func() {
			ctx := context.Background()

			apiTokenName := "test-apitoken-duplicate"
			apiTokenLookup := types.NamespacedName{Name: apiTokenName, Namespace: ApiTokenNamespace}

			By("By creating a new RemoteUnleash")
			secretCreated := remoteUnleashSecretResource(apiTokenName, ApiTokenNamespace, ApiTokenSecret)
			Expect(k8sClient.Create(ctx, secretCreated)).Should(Succeed())
			unleashKey, unleashCreated := remoteUnleashResource(apiTokenName, ApiTokenNamespace, ApiTokenServerURL, secretCreated)
			Expect(k8sClient.Create(ctx, unleashCreated)).Should(Succeed())
			Eventually(remoteUnleashEventually(ctx, unleashKey, unleashCreated), timeout, interval).Should(ContainElement(remoteUnleashSuccessCondition()))

			existingTokens.Tokens = []unleashclient.ApiToken{
				{
					Secret:      ApiTokenSecret + "-1",
					TokenName:   "test-apitoken-duplicate-unleasherator",
					Type:        "CLIENT",
					Environment: "development",
					Project:     "default",
					Projects:    []string{"default"},
					CreatedAt:   time.Date(2021, 1, 1, 2, 0, 0, 0, time.UTC).Format(time.RFC3339),
				},
				{
					Secret:      ApiTokenSecret + "-2",
					TokenName:   "test-apitoken-duplicate-unleasherator",
					Type:        "CLIENT",
					Environment: "production",
					Project:     "default",
					Projects:    []string{"default"},
					CreatedAt:   time.Date(2021, 1, 1, 1, 0, 0, 0, time.UTC).Format(time.RFC3339),
				},
				{
					Secret:      ApiTokenSecret + "-3",
					TokenName:   "test-apitoken-duplicate-unleasherator",
					Type:        "CLIENT",
					Environment: "development",
					Project:     "default",
					Projects:    []string{"default"},
					CreatedAt:   time.Date(2021, 1, 1, 3, 0, 0, 0, time.UTC).Format(time.RFC3339),
				},
			}

			By("By creating a new ApiToken")
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))

			Expect(promGaugeVecVal(apiTokenExistingTokens, ApiTokenNamespace, apiTokenName, "development")).Should(Equal(3.0))
			Expect(promCounterVecVal(apiTokenDeletedCounter, ApiTokenNamespace, apiTokenName)).Should(Equal(2.0))

			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("DELETE %s", fmt.Sprintf("=~%s/.*", unleashclient.ApiTokensEndpoint))]).Should(Equal(2))

			// Verify that the older tokens were deleted
			for _, token := range existingTokens.Tokens {
				Expect(token.Secret).ShouldNot(Equal(ApiTokenSecret + "-1"))
				Expect(token.Secret).ShouldNot(Equal(ApiTokenSecret + "-2"))
			}

			// Verify that we kept the newest duplicate token
			Expect(existingTokens.Tokens).Should(HaveLen(1))
			Expect(existingTokens.Tokens[0].Secret).Should(Equal(ApiTokenSecret + "-3"))
		})
	})
})
