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

				tokenRequest := unleashclient.ApiTokenRequest{}
				if err := json.NewDecoder(req.Body).Decode(&tokenRequest); err != nil {
					return httpmock.NewStringResponse(400, ""), nil
				}

				Expect(tokenRequest.Username).ShouldNot(BeEmpty())
				Expect(tokenRequest.Type).Should(BeElementOf("CLIENT", "FRONTEND"))
				Expect(tokenRequest.Environment).ShouldNot(BeEmpty())
				Expect(tokenRequest.Project).Should(BeEmpty())
				Expect(tokenRequest.Projects).ShouldNot(BeEmpty())

				// {"id":"3cf5d4b3-488d-41bb-91a6-af30ab1b383b","name":"BadDataError","message":"Request validation failed: your request body or params contain invalid data: Project=default222 does not exist","details":[{"message":"Project=default222 does not exist","description":"Project=default222 does not exist"}]}⏎
				// {"id":"56d6a7a5-20e1-4cfd-ab43-8541f52e5221","name":"BadDataError","message":"Request validation failed: your request body or params contain invalid data: Environment=development222 does not exist","details":[{"message":"Environment=development222 does not exist","description":"Environment=development222 does not exist"}]}

				existingTokens.Tokens = append(existingTokens.Tokens, unleashclient.ApiToken{
					Secret:      ApiTokenSecret,
					Username:    tokenRequest.Username,
					Type:        strings.ToLower(tokenRequest.Type),
					Environment: tokenRequest.Environment,
					Project:     tokenRequest.Projects[0],
					Projects:    tokenRequest.Projects,
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
			Expect(k8sClient.Create(ctx, unleashCreated)).Should(Succeed())
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

			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(1))
			Expect(existingTokens.Tokens).Should(HaveLen(1))
			Expect(existingTokens.Tokens[0].Username).Should(Equal(apiTokenCreated.ApiTokenName("unleasherator")))
			Expect(existingTokens.Tokens[0].Type).Should(Equal("client"))
			Expect(existingTokens.Tokens[0].Environment).Should(Equal("development"))
			Expect(existingTokens.Tokens[0].Projects).Should(Equal([]string{"default"}))

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)).Should(Succeed())
			Expect(apiTokenSecretCreated.Data).Should(Equal(map[string][]byte{
				unleashv1.ApiTokenSecretTokenEnv:    []byte(ApiTokenSecret),
				unleashv1.ApiTokenSecretTypeEnv:     []byte("CLIENT"),
				unleashv1.ApiTokenSecretServerEnv:   []byte(ApiTokenServerURL),
				unleashv1.ApiTokenSecretEnvEnv:      []byte("development"),
				unleashv1.ApiTokenSecretProjectsEnv: []byte("default"),
			}))

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

			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(1))
			Expect(existingTokens.Tokens).Should(HaveLen(1))
			Expect(existingTokens.Tokens[0].Type).Should(Equal("frontend"))
			Expect(existingTokens.Tokens[0].Environment).Should(Equal("production"))
			Expect(existingTokens.Tokens[0].Projects).Should(Equal([]string{"project1", "project2", "project3"}))

			By("By checking that the ApiToken secret has been created")
			apiTokenSecretCreated := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecretCreated)).Should(Succeed())
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretTypeEnv]).Should(Equal([]byte("FRONTEND")))
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretEnvEnv]).Should(Equal([]byte("production")))
			Expect(apiTokenSecretCreated.Data[unleashv1.ApiTokenSecretProjectsEnv]).Should(Equal([]byte("project1,project2,project3")))

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
					Username:    apiTokenCreated.ApiTokenName("unleasherator"),
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
			apiTokenCreated := remoteUnleashApiTokenResource(apiTokenName, ApiTokenNamespace, apiTokenName, unleashCreated)
			Expect(k8sClient.Create(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			apiTokenSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, apiTokenLookup, apiTokenSecret)).Should(Succeed())

			By("By updating the ApiToken with a new environment")
			apiTokenCreated.Spec.Environment = "production"
			Expect(k8sClient.Update(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenSecretEventually(ctx, apiTokenLookup, apiTokenSecret), timeout, interval).Should(ContainElement([]byte("production")))
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(existingTokens.Tokens[0].Environment).Should(Equal("production"))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(2))

			By("By updating the ApiToken with a new project")
			apiTokenCreated.Spec.Projects = []string{"project1", "project2", "project3"}
			Expect(k8sClient.Update(ctx, apiTokenCreated)).Should(Succeed())
			Eventually(apiTokenSecretEventually(ctx, apiTokenLookup, apiTokenSecret), timeout, interval).Should(ContainElement([]byte("project1,project2,project3")))
			Eventually(apiTokenEventually(ctx, apiTokenLookup, apiTokenCreated), timeout, interval).Should(ContainElement(apiTokenSuccessCondition()))
			Expect(existingTokens.Tokens[0].Projects).Should(Equal([]string{"project1", "project2", "project3"}))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("POST %s", unleashclient.ApiTokensEndpoint)]).Should(Equal(3))

			By("By deleting the ApiToken")
			Expect(k8sClient.Delete(ctx, apiTokenCreated)).Should(Succeed())
		})
	})
})
