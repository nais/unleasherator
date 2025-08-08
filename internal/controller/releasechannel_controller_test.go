package controller

import (
	"fmt"
	"time"

	"github.com/jarcoal/httpmock"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ReleaseChannel Controller", func() {
	const (
		namespace = "default"

		timeout  = time.Second * 5
		interval = time.Millisecond * 100

		releaseChannelUnleashVersion = "v5.1.2"
	)

	BeforeEach(func() {
		promCounterVecFlush(unleashPublished)

		httpmock.Activate()
		httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
			httpmock.NewStringResponder(200, `{"health": "OK"}`))
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, releaseChannelUnleashVersion)))
	})

	AfterEach(func() {
		httpmock.DeactivateAndReset()
	})

	Context("Basic ReleaseChannel functionality", func() {
		It("Should create a ReleaseChannel and initialize status", func() {
			By("Creating a basic ReleaseChannel")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "basic-test-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "test-image:v1",
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())

			By("Verifying ReleaseChannel status is initialized")
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Phase
			}, timeout, interval).Should(Equal(unleashv1.ReleaseChannelPhaseIdle))

			By("Verifying finalizer is added")
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return len(releaseChannel.Finalizers) > 0
			}, timeout, interval).Should(BeTrue())
		})

		It("Should manage a single Unleash instance", func() {
			By("Creating a ReleaseChannel and one Unleash instance")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "single-instance-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "single-test:v1",
				},
			}

			unleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "single-unleash",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://single-unleash",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Mocking deployment to be ready")
			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      unleash.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			By("Waiting for Unleash instance to be connected")
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, unleash).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			By("Verifying instance gets the ReleaseChannel image")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash.NamespacedName(), unleash)).Should(Succeed())
				return unleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("single-test:v1"))
		})

		It("Should update instance when ReleaseChannel image changes", func() {
			By("Creating a ReleaseChannel and Unleash instance")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "update-test-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "update-test:v1",
				},
			}

			unleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "update-unleash",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://update-unleash",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Mocking deployment to be ready")
			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      unleash.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			By("Waiting for initial setup")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash.NamespacedName(), unleash)).Should(Succeed())
				return unleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("update-test:v1"))

			By("Updating the ReleaseChannel image")
			Eventually(func() error {
				err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)
				if err != nil {
					return err
				}
				releaseChannel.Spec.Image = "update-test:v2"
				return k8sClient.Update(ctx, releaseChannel)
			}, timeout, interval).Should(Succeed())

			By("Verifying the Unleash instance gets updated")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash.NamespacedName(), unleash)).Should(Succeed())
				return unleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("update-test:v2"))
		})

		It("Should ignore instances with CustomImage set", func() {
			By("Creating a ReleaseChannel and Unleash instance with CustomImage")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-image-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "channel-image:v1",
				},
			}

			unleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "custom-image-unleash",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://custom-image-unleash",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
					CustomImage: "custom:v1", // This should make ReleaseChannel ignore this instance
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Mocking deployment to be ready")
			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      unleash.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			By("Verifying instance uses CustomImage, not ReleaseChannel image")
			Consistently(func() string {
				Expect(k8sClient.Get(ctx, unleash.NamespacedName(), unleash)).Should(Succeed())
				return unleash.Status.ResolvedReleaseChannelImage
			}, time.Second*3, interval).Should(BeEmpty()) // Should remain empty since CustomImage is used
		})

		It("Should manage multiple instances in a basic rollout", func() {
			By("Creating a ReleaseChannel and two Unleash instances")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-instance-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "multi-test:v1",
				},
			}

			unleash1 := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-unleash-1",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://multi-unleash-1",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			unleash2 := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "multi-unleash-2",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://multi-unleash-2",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash1)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash2)).Should(Succeed())

			By("Mocking deployments to be ready")
			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      unleash1.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      unleash2.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			By("Waiting for both instances to be connected")
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, unleash1).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, unleash2).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			By("Verifying both instances get the initial ReleaseChannel image")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash1.NamespacedName(), unleash1)).Should(Succeed())
				return unleash1.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("multi-test:v1"))

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash2.NamespacedName(), unleash2)).Should(Succeed())
				return unleash2.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("multi-test:v1"))

			By("Updating the ReleaseChannel to trigger a rollout")
			Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
			releaseChannel.Spec.Image = "multi-test:v2"
			Expect(k8sClient.Update(ctx, releaseChannel)).Should(Succeed())

			By("Verifying both instances eventually get the new image")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash1.NamespacedName(), unleash1)).Should(Succeed())
				return unleash1.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("multi-test:v2"))

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash2.NamespacedName(), unleash2)).Should(Succeed())
				return unleash2.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("multi-test:v2"))

			By("Verifying ReleaseChannel status shows correct instance counts")
			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Instances
			}, timeout, interval).Should(Equal(2))

			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.InstancesUpToDate
			}, timeout, interval).Should(Equal(2))

			By("Verifying ReleaseChannel eventually reaches completed state")
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Phase
			}, timeout*2, interval).Should(Or(
				Equal(unleashv1.ReleaseChannelPhaseCompleted),
				Equal(unleashv1.ReleaseChannelPhaseIdle),
			))
		})

		It("Should perform canary deployment with label selector", func() {
			By("Creating a ReleaseChannel with canary strategy")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "canary-test-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "canary-test:v1",
					Strategy: unleashv1.ReleaseChannelStrategy{
						Canary: unleashv1.ReleaseChannelCanary{
							Enabled: true,
							LabelSelector: metav1.LabelSelector{
								MatchLabels: map[string]string{
									"environment": "staging",
								},
							},
						},
					},
				},
			}

			// Create canary instance (staging)
			canaryUnleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "canary-staging",
					Namespace: namespace,
					Labels: map[string]string{
						"environment": "staging",
					},
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://canary-staging",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			// Create production instance (should not be updated in canary phase)
			prodUnleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "canary-production",
					Namespace: namespace,
					Labels: map[string]string{
						"environment": "production",
					},
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://canary-production",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, canaryUnleash)).Should(Succeed())
			Expect(k8sClient.Create(ctx, prodUnleash)).Should(Succeed())

			By("Mocking deployments to be ready")
			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      canaryUnleash.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				deployment := &appsv1.Deployment{}
				err := getDeployment(k8sClient, ctx, client.ObjectKey{
					Namespace: namespace,
					Name:      prodUnleash.Name,
				}, deployment)
				if err != nil {
					return err
				}
				setDeploymentStatusAvailable(deployment)
				return k8sClient.Status().Update(ctx, deployment)
			}, timeout, interval).Should(Succeed())

			By("Verifying canary deployment behavior")
			// Both instances should get the initial image
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, canaryUnleash.NamespacedName(), canaryUnleash)).Should(Succeed())
				return canaryUnleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("canary-test:v1"))

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, prodUnleash.NamespacedName(), prodUnleash)).Should(Succeed())
				return prodUnleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("canary-test:v1"))

			By("Updating ReleaseChannel to trigger canary rollout")
			Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
			releaseChannel.Spec.Image = "canary-test:v2"
			Expect(k8sClient.Update(ctx, releaseChannel)).Should(Succeed())

			By("Waiting for ReleaseChannel to transition to Canary phase with PreviousImage set")
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Phase == unleashv1.ReleaseChannelPhaseCanary &&
					string(releaseChannel.Status.PreviousImage) == "canary-test:v1"
			}, timeout, interval).Should(BeTrue())

			By("Verifying canary instance gets updated first")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, canaryUnleash.NamespacedName(), canaryUnleash)).Should(Succeed())
				return canaryUnleash.Status.ResolvedReleaseChannelImage
			}, timeout, interval).Should(Equal("canary-test:v2"))

			By("Verifying production instance remains on old version during canary phase")
			Consistently(func() bool {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				Expect(k8sClient.Get(ctx, prodUnleash.NamespacedName(), prodUnleash)).Should(Succeed())

				// Only check if we're still in canary phase
				if releaseChannel.Status.Phase == unleashv1.ReleaseChannelPhaseCanary {
					return prodUnleash.Status.ResolvedReleaseChannelImage == "canary-test:v1"
				}
				// If we've moved beyond canary phase, the check is satisfied
				return true
			}, time.Second*5, interval).Should(BeTrue())

			By("Verifying ReleaseChannel status reflects canary deployment")
			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Instances
			}, timeout, interval).Should(Equal(2))

			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.CanaryInstances
			}, timeout, interval).Should(Equal(1))

			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.CanaryInstancesUpToDate
			}, timeout, interval).Should(Equal(1))
		})
	})
})
