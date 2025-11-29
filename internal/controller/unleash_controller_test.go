package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/jarcoal/httpmock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/stretchr/testify/mock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/resources"
	"github.com/nais/unleasherator/internal/unleashclient"
)

func getUnleash(k8sClient client.Client, ctx context.Context, unleash *unleashv1.Unleash) ([]metav1.Condition, error) {
	if err := k8sClient.Get(ctx, unleash.NamespacedName(), unleash); err != nil {
		return nil, err
	}

	return unsetConditionLastTransitionTime(unleash.Status.Conditions), nil
}

func getDeployment(k8sClient client.Client, ctx context.Context, namespacedName client.ObjectKey, deployment *appsv1.Deployment) error {
	return k8sClient.Get(ctx, namespacedName, deployment)
}

func setDeploymentStatusFailed(deployment *appsv1.Deployment) {
	deployment.Status.Conditions = []appsv1.DeploymentCondition{
		{
			Type:    appsv1.DeploymentProgressing,
			Status:  corev1.ConditionFalse,
			Reason:  "ProgressDeadlineExceeded",
			Message: `Progress deadline exceeded.`,
			LastUpdateTime: metav1.Time{
				Time: time.Now(),
			},
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
}

func setDeploymentStatusAvailable(deployment *appsv1.Deployment) {
	deployment.Status.Conditions = []appsv1.DeploymentCondition{
		{
			Type:    appsv1.DeploymentProgressing,
			Status:  corev1.ConditionTrue,
			Reason:  "NewReplicaSetAvailable",
			Message: `ReplicaSet "fake-abc123" has successfully progressed.`,
			LastUpdateTime: metav1.Time{
				Time: time.Now(),
			},
			LastTransitionTime: metav1.Time{
				Time: time.Now(),
			},
		},
	}
}

var _ = Describe("Unleash controller", func() {
	deploymentTimeout = time.Second * 1

	const (
		UnleashNamespace = "default"
		UnleashVersion   = "v5.1.2"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	BeforeEach(func() {
		promCounterVecFlush(unleashPublished)

		httpmock.Activate()
		httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
			httpmock.NewStringResponder(200, `{"health": "OK"}`))
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, UnleashVersion)))
	})

	AfterEach(func() {
		httpmock.DeactivateAndReset()
	})

	Context("When comparing Unleash deployments", func() {
		It("Should return true for equal deployments", func() {
			ctx := context.Background()

			unleash := unleashResource("test-unleash", UnleashNamespace, unleashv1.UnleashSpec{
				Size:        100,
				CustomImage: "foo/bar:latest",
				Prometheus: unleashv1.UnleashPrometheusConfig{
					Enabled: true,
				},
				Database:      unleashv1.UnleashDatabaseConfig{URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false"},
				WebIngress:    unleashv1.UnleashIngressConfig{},
				ApiIngress:    unleashv1.UnleashIngressConfig{},
				NetworkPolicy: unleashv1.UnleashNetworkPolicyConfig{},
				ExtraEnvVars: []corev1.EnvVar{
					{Name: "foo", Value: "bar"},
				},
				ExtraVolumes: []corev1.Volume{
					{Name: "foo", VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{
							Medium: "Memory",
						},
					}},
				},
				ExtraVolumeMounts: []corev1.VolumeMount{
					{Name: "foo", MountPath: "/foo"},
				},
				ExtraContainers: []corev1.Container{
					{Name: "foo", Image: "foo/bar:latest"},
				},
				ExistingServiceAccountName: "foo",
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						"cpu":    resource.MustParse("100m"),
						"memory": resource.MustParse("128Mi"),
					},
					Requests: corev1.ResourceList{
						"cpu":    resource.MustParse("100m"),
						"memory": resource.MustParse("128Mi"),
					},
				},
				Federation: unleashv1.UnleashFederationConfig{},
			})

			existingDep, err := resources.DeploymentForUnleash(unleash, scheme.Scheme)
			Expect(err).To(BeNil())
			existingDep.ObjectMeta.OwnerReferences = []metav1.OwnerReference{}
			Expect(k8sClient.Create(ctx, existingDep)).Should(Succeed())
			Expect(k8sClient.Get(ctx, unleash.NamespacedName(), existingDep)).Should(Succeed())

			newDep, err := resources.DeploymentForUnleash(unleash, scheme.Scheme)
			Expect(err).To(BeNil())
			newDep.ObjectMeta.OwnerReferences = []metav1.OwnerReference{}

			Expect(equality.Semantic.DeepDerivative(newDep.Spec, existingDep.Spec)).To(BeTrue())
			Expect(equality.Semantic.DeepDerivative(newDep.Labels, existingDep.Labels)).To(BeTrue())
			Expect(k8sClient.Delete(ctx, existingDep)).Should(Succeed())
		})
	})

	Context("When creating a Unleash", func() {
		It("Should fail for missing database config", func() {
			ctx := context.Background()

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-fail", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
				Message: "Failed to reconcile Deployment: validation failed for Deployment (either database.url or database.secretName must be set)",
			}))
			Expect(createdUnleash.IsReady()).To(BeFalse())
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.HealthEndpoint)]).To(Equal(0))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.InstanceAdminStatsEndpoint)]).To(Equal(0))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should fail if Deployment rollout is not complete", func() {
			ctx := context.Background()

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-rollout-fail", UnleashNamespace, unleashv1.UnleashSpec{

				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By faking Deployment status as failed")
			createdDeployment := &appsv1.Deployment{}
			Eventually(getDeployment, timeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())
			setDeploymentStatusFailed(createdDeployment)
			Expect(k8sClient.Status().Update(ctx, createdDeployment)).Should(Succeed())

			By("By checking that Unleash is failed")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
				Message: "Deployment rollout timed out after 1s",
			}))
			Expect(createdUnleash.IsReady()).To(BeFalse())
			Expect(createdUnleash.Status.Reconciled).To(BeFalse())
			Expect(createdUnleash.Status.Connected).To(BeFalse())

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should fail when it cannot connect to Unleash", func() {
			ctx := context.Background()

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-connect-fail", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By faking Deployment status as available")
			createdDeployment := &appsv1.Deployment{}
			Eventually(getDeployment, timeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())
			setDeploymentStatusAvailable(createdDeployment)
			Expect(k8sClient.Status().Update(ctx, createdDeployment)).Should(Succeed())

			By("By mocking connection failure")
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(500, `{"error": "Internal Server Error"}`))

			By("By checking that Unleash is reconciled but not connected")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionFalse,
				Reason:  "Reconciling",
			}))

			Expect(createdUnleash.IsReady()).To(BeFalse())
			Expect(createdUnleash.Status.Reconciled).To(BeTrue())
			Expect(createdUnleash.Status.Connected).To(BeFalse())

			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.HealthEndpoint)]).To(Equal(0))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.InstanceAdminStatsEndpoint)]).ToNot(Equal(0))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should succeed when it can connect to Unleash", func() {
			ctx := context.Background()

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-success", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By faking Deployment status as available")
			createdDeployment := &appsv1.Deployment{}
			Eventually(getDeployment, timeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())
			setDeploymentStatusAvailable(createdDeployment)
			Expect(k8sClient.Status().Update(ctx, createdDeployment)).Should(Succeed())

			By("By checking that Unleash is connected")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			Expect(createdUnleash.Status.Version).To(Equal(UnleashVersion))
			Expect(createdUnleash.Status.Reconciled).To(BeTrue())
			Expect(createdUnleash.Status.Connected).To(BeTrue())

			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.HealthEndpoint)]).To(Equal(0))
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.InstanceAdminStatsEndpoint)]).To(Equal(1))

			deployment := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), deployment)).Should(Succeed())

			service := &corev1.Service{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), service)).Should(Succeed())

			instanceSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedInstanceSecretName(), instanceSecret)).Should(Succeed())

			operatorSecret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedOperatorSecretName(namespace), operatorSecret)).Should(Succeed())

			networkPolicy := &networkingv1.NetworkPolicy{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), networkPolicy)).Should(Succeed())

			serviceMonitor := &monitoringv1.ServiceMonitor{}
			Expect(k8sClient.Get(ctx, createdUnleash.NamespacedName(), serviceMonitor)).Should(Succeed())

			val, err := promGaugeVecVal(unleashStatus, createdUnleash.Namespace, createdUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			val, err = promGaugeVecVal(unleashStatus, createdUnleash.Namespace, createdUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should publish Unleash instance when federation is enabled", func() {
			ctx := context.Background()

			By("By mocking Unleash Publisher")
			matcher := func(unleash *unleashv1.Unleash) bool {
				return unleash.Name == "test-unleash-federate"
			}
			mockPublisher.On("Publish", mock.AnythingOfType("*context.valueCtx"), mock.MatchedBy(matcher), mock.AnythingOfType("string")).Return(nil)

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-federate", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
				Federation: unleashv1.UnleashFederationConfig{
					Enabled:    true,
					Namespaces: []string{"namespace1", "namespace2"},
					Clusters:   []string{"cluster1", "cluster2"},
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By faking Deployment status as available")
			createdDeployment := &appsv1.Deployment{}
			Eventually(getDeployment, timeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())
			setDeploymentStatusAvailable(createdDeployment)
			Expect(k8sClient.Status().Update(ctx, createdDeployment)).Should(Succeed())

			By("By checking that Unleash is connected")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			Expect(mockPublisher.AssertCalled(GinkgoT(), "Publish", mock.AnythingOfType("*context.valueCtx"), mock.AnythingOfType("*unleash_nais_io_v1.Unleash"), mock.AnythingOfType("string"))).To(BeTrue())
			Expect(mockPublisher.AssertNumberOfCalls(GinkgoT(), "Publish", 1)).To(BeTrue())

			val, err := promCounterVecVal(unleashPublished, "provisioned", unleashPublishMetricStatusSending)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			val, err = promCounterVecVal(unleashPublished, "provisioned", unleashPublishMetricStatusSuccess)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})
	})
})
