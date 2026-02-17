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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

var _ = Describe("Unleash Controller", func() {
	const (
		UnleashVersion = "v5.1.2"
	)

	var (
		UnleashNamespace string // Use unique namespace per test for envtest isolation
		testCounter      int
		interval         = time.Millisecond * 10   // Reduced from 250ms to 10ms
		timeout          = time.Millisecond * 5000 // Increased to 5s for complex ReleaseChannel coordination scenarios
		// Longer timeout for tests involving multi-controller coordination
		// In CI with parallel tests, controller workqueues can get backed up
		coordinationTimeout = time.Second * 30
	)

	BeforeEach(func() {
		// Generate unique namespace for resource isolation
		// Use timestamp to prevent collisions during rapid test execution
		testCounter++
		UnleashNamespace = fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), testCounter)

		// Create the namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: UnleashNamespace,
			},
		}
		Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		promCounterVecFlush(unleashPublished)

		// Reset federation publisher mocks between tests to avoid leakage
		mockPublisher.Mock = mock.Mock{}

		httpmock.DeactivateAndReset() // Fully reset including call counts
		httpmock.Activate()
		// Re-register the global NoResponder to catch requests from concurrent tests
		// This is critical because controllers run continuously and may reconcile resources from other tests
		httpmock.RegisterNoResponder(httpmock.NewStringResponder(200, `{"health":"OK","versionOSS":"v5.1.2"}`))
		httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
			httpmock.NewStringResponder(200, `{"health": "OK"}`))
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, UnleashVersion)))
	})

	AfterEach(func() {
		// Only clear call history, don't deactivate (allows other concurrent tests to continue)
		httpmock.Reset()
		// Re-register NoResponder to catch requests from other concurrent tests
		httpmock.RegisterNoResponder(httpmock.NewStringResponder(200, `{"health":"OK","versionOSS":"v5.1.2"}`))
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

		PIt("Should fail when it cannot connect to Unleash")

		It("Should succeed when it can connect to Unleash", func() {
			ctx := context.Background()

			By("By resetting httpmock to isolate this test")
			httpmock.Reset()
			// Re-register NoResponder and necessary responders for connection testing
			httpmock.RegisterNoResponder(httpmock.NewStringResponder(200, `{"health":"OK","versionOSS":"v5.1.2"}`))
			httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
				httpmock.NewStringResponder(200, `{"health": "OK"}`))
			httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
				httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, UnleashVersion)))

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

			By("By verifying appropriate HTTP calls were made for connection verification")
			// The controller should not call the health endpoint in normal operation
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.HealthEndpoint)]).To(Equal(0))
			// The controller should call the admin stats endpoint to verify connection and get version
			Expect(httpmock.GetCallCountInfo()[fmt.Sprintf("GET %s", unleashclient.InstanceAdminStatsEndpoint)]).To(BeNumerically(">=", 1))

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

			// Reconciled metric uses "unknown" version because stats is nil during reconcile status update
			val, err := promGaugeVecVal(unleashStatus, createdUnleash.Name, unleashv1.UnleashStatusConditionTypeReconciled, "unknown", "none")
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			// Connected metric uses the actual version from stats (returned by API during connection test)
			val, err = promGaugeVecVal(unleashStatus, createdUnleash.Name, unleashv1.UnleashStatusConditionTypeConnected, UnleashVersion, "none")
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1)))

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should delete secrets when Unleash is deleted", func() {
			ctx := context.Background()

			By("By creating a new Unleash")
			unleash := unleashResource("test-unleash-delete-secrets", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By waiting for secrets to be created")
			instanceSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, unleash.NamespacedInstanceSecretName(), instanceSecret)
			}, timeout, interval).Should(Succeed())

			operatorSecret := &corev1.Secret{}
			Eventually(func() error {
				return k8sClient.Get(ctx, unleash.NamespacedOperatorSecretName(namespace), operatorSecret)
			}, timeout, interval).Should(Succeed())

			By("By deleting the Unleash")
			Expect(k8sClient.Delete(ctx, unleash)).Should(Succeed())

			By("By verifying the Unleash is gone")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, unleash.NamespacedName(), &unleashv1.Unleash{}))
			}, timeout, interval).Should(BeTrue())

			By("By verifying the operator secret is deleted")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, unleash.NamespacedOperatorSecretName(namespace), &corev1.Secret{}))
			}, timeout, interval).Should(BeTrue())

			By("By verifying the instance secret is deleted")
			Eventually(func() bool {
				return apierrors.IsNotFound(k8sClient.Get(ctx, unleash.NamespacedInstanceSecretName(), &corev1.Secret{}))
			}, timeout, interval).Should(BeTrue())
		})

		It("Should resolve ReleaseChannel image on creation and not update on subsequent reconciles", func() {
			ctx := context.Background()

			releaseChannelImage := "quay.io/unleash/unleash-server:6.3.0"
			releaseChannelName := "stable"

			By("By creating a ReleaseChannel")
			releaseChannel := releaseChannelResource(releaseChannelName, UnleashNamespace, releaseChannelImage)
			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())

			By("By waiting for ReleaseChannel to be ready")
			createdReleaseChannel := &unleashv1.ReleaseChannel{ObjectMeta: releaseChannel.ObjectMeta}
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), createdReleaseChannel); err != nil {
					return false
				}
				return createdReleaseChannel.Status.Phase == "Idle"
			}, timeout, interval).Should(BeTrue())

			By("By creating a new Unleash that references the ReleaseChannel")
			unleash := unleashResource("test-unleash-releasechannel", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
				ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
					Name: releaseChannelName,
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By verifying ReleaseChannel controller does NOT interfere during initial creation")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			// Wait for initial reconciliation to complete
			// Use coordinationTimeout because in CI, controller workqueues can get backed up
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return false
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage != ""
			}, coordinationTimeout, interval).Should(BeTrue())

			// In the new status-based architecture, coordination happens through status fields
			// rather than annotations, so we expect no coordination-related annotations
			if createdUnleash.Annotations != nil {
				// Check that no legacy ReleaseChannel coordination annotations exist
				for key := range createdUnleash.Annotations {
					Expect(key).ToNot(ContainSubstring("releasechannel.unleash.nais.io/"),
						"Legacy ReleaseChannel coordination annotations should not be present")
				}
			}

			By("By checking that the Unleash has the resolved ReleaseChannel image in status")
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal(releaseChannelImage))

			By("By verifying CustomImage is NOT set during initial creation")
			Expect(createdUnleash.Spec.CustomImage).Should(BeEmpty(), "CustomImage should not be set during initial creation")

			By("By faking Deployment status as available")
			createdDeployment := &appsv1.Deployment{}
			Eventually(getDeployment, timeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())
			setDeploymentStatusAvailable(createdDeployment)
			Expect(k8sClient.Status().Update(ctx, createdDeployment)).Should(Succeed())

			By("By checking that the deployment uses the ReleaseChannel image")
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdDeployment); err != nil {
					return ""
				}
				return createdDeployment.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal(releaseChannelImage))

			By("By manually triggering an Unleash reconcile (simulating periodic reconciliation)")
			// Force a reconcile by updating a label - this simulates routine reconciliation
			Eventually(func() error {
				freshUnleash := &unleashv1.Unleash{}
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), freshUnleash); err != nil {
					return err
				}
				if freshUnleash.Labels == nil {
					freshUnleash.Labels = map[string]string{}
				}
				freshUnleash.Labels["test"] = "reconcile-trigger"
				return k8sClient.Update(ctx, freshUnleash)
			}, timeout, interval).Should(Succeed())

			By("By verifying ReleaseChannel controller does NOT change image during routine reconciliation")
			// After routine reconciliation, the resolved image should remain stable
			Consistently(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage
			}, time.Millisecond*500, interval).Should(Equal(releaseChannelImage),
				"ReleaseChannel controller should not change resolved image during routine reconciliation")

			By("By checking that routine Unleash reconciliation does NOT change the image")
			// The status should remain the same after routine reconciliation
			Consistently(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage
			}, time.Millisecond*500, interval).Should(Equal(releaseChannelImage))

			By("By verifying CustomImage remains empty during routine operations")
			Consistently(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return "error"
				}
				return createdUnleash.Spec.CustomImage
			}, time.Millisecond*500, interval).Should(BeEmpty(), "CustomImage should remain empty during routine operations")

			By("By updating the ReleaseChannel to a new image (intentional rollout)")
			newReleaseChannelImage := "quay.io/unleash/unleash-server:6.4.0"
			// Get the latest version to avoid conflicts
			Eventually(func() error {
				freshReleaseChannel := &unleashv1.ReleaseChannel{}
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), freshReleaseChannel); err != nil {
					return err
				}
				freshReleaseChannel.Spec.Image = unleashv1.UnleashImage(newReleaseChannelImage)
				return k8sClient.Update(ctx, freshReleaseChannel)
			}, timeout, interval).Should(Succeed())

			By("By verifying ReleaseChannel controller DOES update status during intentional rollout")
			// Now the ReleaseChannel controller should update the Unleash status directly
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage
			}, coordinationTimeout, interval).Should(Equal(newReleaseChannelImage), "ReleaseChannel controller should update resolved image in status during rollout")

			By("By checking that ReleaseChannel name is tracked in status")
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ReleaseChannelName
			}, coordinationTimeout, interval).Should(Equal(releaseChannel.Name), "ReleaseChannel name should be tracked in status")

			By("By checking that the Unleash status is updated to the new image")
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage
			}, coordinationTimeout, interval).Should(Equal(newReleaseChannelImage))

			By("By verifying the status-based coordination works as intended")
			// The status should contain the expected coordination data
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return false
				}

				// Verify all required status fields are present
				hasResolvedImage := createdUnleash.Status.ResolvedReleaseChannelImage == newReleaseChannelImage
				hasReleaseChannelName := createdUnleash.Status.ReleaseChannelName == releaseChannel.Name

				return hasResolvedImage && hasReleaseChannelName
			}, coordinationTimeout, interval).Should(BeTrue(), "All coordination status fields should be present during rollout")

			By("By verifying that ReleaseChannel coordination is phase-aware")
			// Check that the rollout intent includes the phase information
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() string {
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), createdReleaseChannel); err != nil {
					return ""
				}
				return string(createdReleaseChannel.Status.Phase)
			}, coordinationTimeout, interval).Should(BeElementOf("Rolling", "Canary", "Idle"), "ReleaseChannel should transition through rollout phases")

			By("By cleaning up resources")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, releaseChannel)).Should(Succeed())
		})

		It("Should publish Unleash instance when federation is enabled", func() {
			ctx := context.Background()
			// Use longer timeout for this test - federation involves multiple reconcile cycles
			federationTimeout := time.Second * 10

			By("By mocking Unleash Publisher")
			matcher := func(unleash *unleashv1.Unleash) bool {
				return unleash.Name == "test-unleash-federate"
			}
			mockPublisher.On("Publish", mock.Anything, mock.MatchedBy(matcher), mock.AnythingOfType("string")).Return(nil)

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
			Eventually(getDeployment, federationTimeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())
			setDeploymentStatusAvailable(createdDeployment)
			Expect(k8sClient.Status().Update(ctx, createdDeployment)).Should(Succeed())

			By("By checking that Unleash is connected")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}
			Eventually(getUnleash, federationTimeout, interval).WithArguments(k8sClient, ctx, createdUnleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Expect(createdUnleash.IsReady()).To(BeTrue())
			Eventually(func() int {
				return len(mockPublisher.Calls)
			}, federationTimeout, interval).Should(Equal(1), "federation publisher should be invoked exactly once")

			Expect(mockPublisher.AssertExpectations(GinkgoT())).To(BeTrue())

			val, err := promCounterVecVal(unleashPublished, "provisioned", unleashPublishMetricStatusSending)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1))) // Called once

			val, err = promCounterVecVal(unleashPublished, "provisioned", unleashPublishMetricStatusSuccess)
			Expect(err).To(BeNil())
			Expect(val).To(Equal(float64(1))) // Called once

			By("By cleaning up the Unleash")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
		})

		It("Should wait for ReleaseChannel to become available instead of using default image", func() {
			ctx := context.Background()

			By("By creating an Unleash that references a non-existent ReleaseChannel")
			unleash := unleashResource("test-unleash-missing-rc", UnleashNamespace, unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgres://unleash:unleash@unleash-postgres:5432/unleash?ssl=false",
				},
				ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
					Name: "missing-channel",
				},
			})
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("By verifying the Unleash is stuck waiting (no deployment created)")
			createdUnleash := &unleashv1.Unleash{ObjectMeta: unleash.ObjectMeta}

			// The Unleash should exist but should not create a deployment
			Eventually(func() bool {
				return k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash) == nil
			}, timeout, interval).Should(BeTrue())

			// No deployment should be created yet
			createdDeployment := &appsv1.Deployment{}
			Consistently(func() bool {
				err := k8sClient.Get(ctx, unleash.NamespacedName(), createdDeployment)
				return apierrors.IsNotFound(err)
			}, time.Millisecond*500, interval).Should(BeTrue())

			By("By creating the missing ReleaseChannel")
			releaseChannelImage := "unleash:6.0.0"
			releaseChannel := releaseChannelResource("missing-channel", UnleashNamespace, releaseChannelImage)
			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())

			By("By waiting for ReleaseChannel to be ready")
			createdReleaseChannel := &unleashv1.ReleaseChannel{ObjectMeta: releaseChannel.ObjectMeta}
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), createdReleaseChannel); err != nil {
					return false
				}
				return createdReleaseChannel.Status.Phase == "Idle"
			}, coordinationTimeout, interval).Should(BeTrue())

			By("By verifying the Unleash now progresses and uses the ReleaseChannel image")
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdUnleash); err != nil {
					return ""
				}
				return createdUnleash.Status.ResolvedReleaseChannelImage
			}, coordinationTimeout, interval).Should(Equal(releaseChannelImage))

			By("By verifying the deployment is now created with the correct image")
			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(getDeployment, coordinationTimeout, interval).WithArguments(k8sClient, ctx, unleash.NamespacedName(), createdDeployment).Should(Succeed())

			// Use coordinationTimeout because this involves multi-controller coordination
			Eventually(func() string {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), createdDeployment); err != nil {
					return ""
				}
				return createdDeployment.Spec.Template.Spec.Containers[0].Image
			}, coordinationTimeout, interval).Should(Equal(releaseChannelImage))

			By("By cleaning up resources")
			Expect(k8sClient.Delete(ctx, createdUnleash)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, releaseChannel)).Should(Succeed())
		})
	})
})
