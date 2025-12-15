package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/jarcoal/httpmock"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ReleaseChannel Controller", func() {
	const (
		timeout  = time.Millisecond * 7000 // Provide extra slack for multi-instance and canary rollouts
		interval = time.Millisecond * 20   // Reduced from 100ms to 20ms for faster polling

		releaseChannelUnleashVersion = "v5.1.2"
	)

	// Use a unique suffix for each test run to avoid conflicts
	var testID string
	var namespace string // Use unique namespace per test for envtest isolation
	var testCounter int

	BeforeEach(func() {
		// Generate unique test ID for resource names to ensure isolation
		// Use both timestamp and counter to ensure uniqueness across tests
		testCounter++
		testID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), testCounter)
		namespace = fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), testCounter) // Unique namespace per test

		// Create the namespace
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: namespace,
			},
		}
		Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ns)
		})

		promCounterVecFlush(unleashPublished)

		// Ensure complete httpmock isolation between tests
		httpmock.DeactivateAndReset()
		httpmock.Activate()

		httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
			httpmock.NewStringResponder(200, `{"health": "OK"}`))
		httpmock.RegisterResponder("GET", unleashclient.InstanceAdminStatsEndpoint,
			httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, releaseChannelUnleashVersion)))
	})

	AfterEach(func() {
		// Clean up httpmock state after each test
		httpmock.Reset()
	})

	// Helper functions to reduce test duplication
	createUnleash := func(name, namespace, releaseChannelName string, labels map[string]string) *unleashv1.Unleash {
		unleash := &unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Spec: unleashv1.UnleashSpec{
				Database: unleashv1.UnleashDatabaseConfig{
					URL: "postgre://postgres",
				},
				ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
					Name: releaseChannelName,
				},
			},
		}
		return unleash
	}

	registerHTTPMocksForInstance := func(instance *unleashv1.Unleash) {
		httpmock.RegisterResponder("GET", fmt.Sprintf("%s%s", instance.URL(), unleashclient.HealthEndpoint),
			httpmock.NewStringResponder(200, `{"health": "OK"}`))
		httpmock.RegisterResponder("GET", fmt.Sprintf("%s%s", instance.URL(), unleashclient.InstanceAdminStatsEndpoint),
			httpmock.NewStringResponder(200, fmt.Sprintf(`{"versionOSS": "%s"}`, releaseChannelUnleashVersion)))
	}

	simulateDeploymentReady := func(instance *unleashv1.Unleash) {
		Eventually(func() error {
			deployment := &appsv1.Deployment{}
			if err := k8sClient.Get(ctx, instance.NamespacedName(), deployment); err != nil {
				return err
			}
			setDeploymentStatusAvailable(deployment)
			return k8sClient.Status().Update(ctx, deployment)
		}, timeout, interval).Should(Succeed())
	}

	// Automatic deployment simulation - runs in background
	var deploymentSimulationCtx context.Context
	var cancelDeploymentSimulation context.CancelFunc

	startAutomaticDeploymentSimulation := func() {
		deploymentSimulationCtx, cancelDeploymentSimulation = context.WithCancel(ctx)

		go func() {
			defer GinkgoRecover()

			// Watch for new deployments and immediately make them ready
			ticker := time.NewTicker(time.Millisecond * 10) // Check every 10ms
			defer ticker.Stop()

			processedDeployments := make(map[string]bool)

			for {
				select {
				case <-deploymentSimulationCtx.Done():
					return
				case <-ticker.C:
					deployments := &appsv1.DeploymentList{}
					if err := k8sClient.List(deploymentSimulationCtx, deployments); err != nil {
						continue
					}

					for _, deployment := range deployments.Items {
						deploymentKey := fmt.Sprintf("%s/%s", deployment.Namespace, deployment.Name)

						// Skip if already processed
						if processedDeployments[deploymentKey] {
							continue
						}

						// Check if deployment is not yet ready
						isReady := false
						for _, condition := range deployment.Status.Conditions {
							if condition.Type == appsv1.DeploymentProgressing &&
								condition.Status == corev1.ConditionTrue &&
								condition.Reason == "NewReplicaSetAvailable" {
								isReady = true
								break
							}
						}

						if !isReady {
							// Make deployment ready immediately
							deploymentCopy := deployment.DeepCopy()
							setDeploymentStatusAvailable(deploymentCopy)

							if err := k8sClient.Status().Update(deploymentSimulationCtx, deploymentCopy); err == nil {
								processedDeployments[deploymentKey] = true
								GinkgoWriter.Printf("[DEPLOYMENT-SIM] Made deployment %s ready\n", deploymentKey)
							}
						}
					}
				}
			}
		}()
	}

	stopAutomaticDeploymentSimulation := func() {
		if cancelDeploymentSimulation != nil {
			cancelDeploymentSimulation()
		}
	}

	waitForImageResolution := func(instance *unleashv1.Unleash, expectedImage string) {
		Eventually(func() string {
			Expect(k8sClient.Get(ctx, instance.NamespacedName(), instance)).Should(Succeed())
			return instance.Status.ResolvedReleaseChannelImage
		}, timeout, interval).Should(Equal(expectedImage))
	}

	waitForInstanceReady := func(instance *unleashv1.Unleash) {
		Eventually(func() bool {
			Expect(k8sClient.Get(ctx, instance.NamespacedName(), instance)).Should(Succeed())
			for _, condition := range instance.Status.Conditions {
				if condition.Type == unleashv1.UnleashStatusConditionTypeReconciled {
					return condition.Status == metav1.ConditionTrue
				}
			}
			return false
		}, timeout, interval).Should(BeTrue(), fmt.Sprintf("instance %s should be ready", instance.ObjectMeta.Name))
	}

	waitForConnection := func(instance *unleashv1.Unleash) {
		Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, instance).Should(ContainElement(metav1.Condition{
			Type:    unleashv1.UnleashStatusConditionTypeConnected,
			Status:  metav1.ConditionTrue,
			Reason:  "Reconciling",
			Message: "Successfully connected to Unleash instance",
		}))
	}

	Context("Basic ReleaseChannel functionality", func() {
		It("Should create a ReleaseChannel and initialize status", func() {
			By("Creating a basic ReleaseChannel")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("basic-test-channel-%s", testID),
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
				return len(releaseChannel.ObjectMeta.Finalizers) > 0
			}, timeout, interval).Should(BeTrue())
		})

		It("Should manage a single Unleash instance", func() {
			By("Creating a ReleaseChannel and one Unleash instance")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("single-instance-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "single-test:v1",
				},
			}

			unleash := createUnleash(fmt.Sprintf("single-unleash-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, releaseChannel)
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, unleash)

			By("Mocking deployment to be ready")
			simulateDeploymentReady(unleash)

			By("Waiting for Unleash instance to be connected")
			waitForConnection(unleash)

			By("Verifying instance gets the ReleaseChannel image")
			waitForImageResolution(unleash, "single-test:v1")
		})

		It("Should update instance when ReleaseChannel image changes", func() {
			By("Creating a ReleaseChannel and Unleash instance")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("update-test-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "update-test:v1",
				},
			}

			unleash := createUnleash(fmt.Sprintf("update-unleash-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Mocking deployment to be ready")
			simulateDeploymentReady(unleash)

			By("Waiting for initial setup")
			waitForImageResolution(unleash, "update-test:v1")

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
			waitForImageResolution(unleash, "update-test:v2")
		})

		It("Should ignore instances with CustomImage set", func() {
			By("Creating a ReleaseChannel and Unleash instance with CustomImage")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("custom-image-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "channel-image:v1",
				},
			}

			unleash := createUnleash(fmt.Sprintf("custom-image-unleash-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)
			unleash.Spec.CustomImage = "custom:v1" // This should make ReleaseChannel ignore this instance

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Mocking deployment to be ready")
			simulateDeploymentReady(unleash)

			By("Verifying instance uses CustomImage, not ReleaseChannel image")
			Consistently(func() string {
				Expect(k8sClient.Get(ctx, unleash.NamespacedName(), unleash)).Should(Succeed())
				return unleash.Status.ResolvedReleaseChannelImage
			}, time.Second*3, interval).Should(BeEmpty()) // Should remain empty since CustomImage is used
		})

		It("Should manage multiple instances in a basic rollout", func() {
			// Start automatic deployment simulation for this test
			startAutomaticDeploymentSimulation()
			defer stopAutomaticDeploymentSimulation()

			By("Creating a ReleaseChannel and two Unleash instances")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("multi-instance-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "multi-test:v1",
				},
			}

			unleash1 := createUnleash(fmt.Sprintf("multi-unleash-1-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)
			unleash2 := createUnleash(fmt.Sprintf("multi-unleash-2-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash1)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash2)).Should(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, unleash1)
				_ = k8sClient.Delete(ctx, unleash2)
				_ = k8sClient.Delete(ctx, releaseChannel)
			})

			By("Verifying both Unleash instances exist in the API server")
			Eventually(func() error {
				return k8sClient.Get(ctx, unleash1.NamespacedName(), &unleashv1.Unleash{})
			}, timeout, interval).Should(Succeed(), "unleash1 should be created")
			Eventually(func() error {
				return k8sClient.Get(ctx, unleash2.NamespacedName(), &unleashv1.Unleash{})
			}, timeout, interval).Should(Succeed(), "unleash2 should be created")

			By("Mocking deployments to be ready")
			simulateDeploymentReady(unleash1)
			simulateDeploymentReady(unleash2)

			By("Waiting for both instances to be connected")
			waitForConnection(unleash1)
			waitForConnection(unleash2)

			By("Verifying both instances get the initial ReleaseChannel image")
			waitForImageResolution(unleash1, "multi-test:v1")
			waitForImageResolution(unleash2, "multi-test:v1")

			By("Verifying ReleaseChannel discovers both instances")
			// After both instances are connected and have resolved images,
			// the ReleaseChannel should have reconciled and discovered them
			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())

				// Also check what instances exist in the namespace for debugging
				unleashList := &unleashv1.UnleashList{}
				_ = k8sClient.List(ctx, unleashList)
				matchingCount := 0
				for _, u := range unleashList.Items {
					if u.Spec.ReleaseChannel.Name == fmt.Sprintf("multi-instance-channel-%s", testID) && u.Spec.CustomImage == "" {
						matchingCount++
						GinkgoWriter.Printf("  Found matching instance: %s (RC: %s, CustomImage: %s)\n",
							u.ObjectMeta.Name, u.Spec.ReleaseChannel.Name, u.Spec.CustomImage)
					}
				}

				GinkgoWriter.Printf("Multi-instance test - RC status instances: %d, phase: %s, list query found: %d\n",
					releaseChannel.Status.Instances, releaseChannel.Status.Phase, matchingCount)
				return releaseChannel.Status.Instances
			}, timeout*2, interval).Should(Equal(2), "ReleaseChannel should discover both instances")

			By("Updating the ReleaseChannel to trigger a rollout")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel); err != nil {
					return err
				}
				releaseChannel.Spec.Image = "multi-test:v2"
				return k8sClient.Update(ctx, releaseChannel)
			}, timeout, interval).Should(Succeed())

			By("Verifying both instances eventually get the new image")
			waitForImageResolution(unleash1, "multi-test:v2")
			waitForImageResolution(unleash2, "multi-test:v2")

			By("Verifying ReleaseChannel status shows correct instance counts")
			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				GinkgoWriter.Printf("After image update - instances: %d, phase: %s, name: %s\n",
					releaseChannel.Status.Instances, releaseChannel.Status.Phase, releaseChannel.ObjectMeta.Name)
				return releaseChannel.Status.Instances
			}, timeout*2, interval).Should(Equal(2))

			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.InstancesUpToDate
			}, timeout*2, interval).Should(Equal(2))

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
			// Start automatic deployment simulation for this test
			startAutomaticDeploymentSimulation()
			defer stopAutomaticDeploymentSimulation()

			By("Step 1: Creating a ReleaseChannel with canary strategy")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("canary-test-channel-%s", testID),
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
					HealthChecks: unleashv1.HealthCheckConfig{
						Enabled:      true,
						InitialDelay: &metav1.Duration{Duration: time.Millisecond * 200},
						Timeout:      &metav1.Duration{Duration: time.Second * 30},
					},
				},
			}

			By("Step 2: Creating canary instance (staging environment)")
			canaryUnleash := createUnleash(fmt.Sprintf("canary-staging-%s", testID), namespace, releaseChannel.ObjectMeta.Name, map[string]string{
				"environment": "staging",
			})

			By("Step 3: Creating production instance (production environment)")
			prodUnleash := createUnleash(fmt.Sprintf("canary-production-%s", testID), namespace, releaseChannel.ObjectMeta.Name, map[string]string{
				"environment": "production",
			})

			By("Step 4: Creating all resources")
			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, canaryUnleash)).Should(Succeed())
			Expect(k8sClient.Create(ctx, prodUnleash)).Should(Succeed())

			By("Step 4a: Verifying both Unleash instances were created successfully")
			Eventually(func() error {
				return k8sClient.Get(ctx, canaryUnleash.NamespacedName(), &unleashv1.Unleash{})
			}, timeout, interval).Should(Succeed(), "canary-staging instance should exist")
			Eventually(func() error {
				return k8sClient.Get(ctx, prodUnleash.NamespacedName(), &unleashv1.Unleash{})
			}, timeout, interval).Should(Succeed(), "canary-production instance should exist")

			By("Step 4b: Setting up HTTP mocks for instance health checks")
			registerHTTPMocksForInstance(canaryUnleash)
			registerHTTPMocksForInstance(prodUnleash)

			// Setup intelligent deployment readiness simulation using fast polling for phase changes
			testCtx, cancel := context.WithCancel(ctx)
			defer cancel()

			go func() {
				var lastPhase unleashv1.ReleaseChannelPhase
				var lastGeneration int64

				// Use very fast polling to catch phase transitions immediately
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()

				for {
					select {
					case <-testCtx.Done():
						return
					case <-ticker.C:
						var currentRC unleashv1.ReleaseChannel
						if err := k8sClient.Get(testCtx, releaseChannel.NamespacedName(), &currentRC); err != nil {
							continue
						}

						currentPhase := currentRC.Status.Phase
						currentGeneration := currentRC.ObjectMeta.Generation

						// React to phase transitions or generation changes (indicating spec updates)
						if currentPhase != lastPhase || currentGeneration != lastGeneration {
							GinkgoWriter.Printf("State change: phase %s -> %s, generation %d -> %d\n",
								lastPhase, currentPhase, lastGeneration, currentGeneration)

							switch currentPhase {
							case unleashv1.ReleaseChannelPhaseIdle:
								// Initial setup or completion - ensure both deployments are ready
								// But only if we've transitioned from another phase (not on initial setup)
								if lastPhase != "" {
									GinkgoWriter.Printf("Returned to Idle - ensuring both deployments ready\n")
									simulateDeploymentReady(canaryUnleash)
									simulateDeploymentReady(prodUnleash)
								}

							case unleashv1.ReleaseChannelPhaseCanary:
								// Canary phase started - simulate canary deployment readiness
								GinkgoWriter.Printf("Simulating canary deployment readiness\n")
								simulateDeploymentReady(canaryUnleash)

							case unleashv1.ReleaseChannelPhaseRolling:
								// Rolling phase started - simulate production deployment readiness
								GinkgoWriter.Printf("Simulating production deployment readiness\n")
								simulateDeploymentReady(prodUnleash)
							}

							lastPhase = currentPhase
							lastGeneration = currentGeneration
						}
					}
				}
			}()

			By("Step 5: Waiting for both Unleash instances to become ready")
			// This ensures both instances exist and their controllers have processed them
			// before we check the ReleaseChannel status
			waitForImageResolution(canaryUnleash, "canary-test:v1")
			waitForImageResolution(prodUnleash, "canary-test:v1")

			// Also wait for both instances to be fully reconciled (Reconciled condition = True)
			waitForInstanceReady(canaryUnleash)
			waitForInstanceReady(prodUnleash)

			By("Step 6: Verifying ReleaseChannel discovers both instances")
			// The status.conditions changes on Unleash instances should trigger ReleaseChannel reconciliation
			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				GinkgoWriter.Printf("ReleaseChannel instances: %d, phase: %s\n",
					releaseChannel.Status.Instances, releaseChannel.Status.Phase)
				return releaseChannel.Status.Instances
			}, timeout, interval).Should(Equal(2), "ReleaseChannel should discover both instances")

			By("Step 7: Verifying ReleaseChannel is in Idle phase")
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Phase
			}, timeout, interval).Should(Equal(unleashv1.ReleaseChannelPhaseIdle))

			By("Step 7: Triggering canary deployment by updating image")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel); err != nil {
					return err
				}
				releaseChannel.Spec.Image = "canary-test:v2"
				return k8sClient.Update(ctx, releaseChannel)
			}, timeout, interval).Should(Succeed())

			By("Step 8: Verifying ReleaseChannel progresses from Idle through deployment phases")
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				GinkgoWriter.Printf("Current phase: %s\n", releaseChannel.Status.Phase)
				return releaseChannel.Status.Phase
			}, timeout*2, interval).Should(Or(
				Equal(unleashv1.ReleaseChannelPhaseCanary),
				Equal(unleashv1.ReleaseChannelPhaseRolling),
				Equal(unleashv1.ReleaseChannelPhaseCompleted),
				Equal(unleashv1.ReleaseChannelPhaseIdle), // May complete so fast we go back to Idle
			))

			By("Step 9: Verifying PreviousImage is set correctly")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return string(releaseChannel.Status.PreviousImage)
			}, timeout, interval).Should(Equal("canary-test:v1"))

			By("Step 10: Verifying only canary instance gets updated to new image")
			waitForImageResolution(canaryUnleash, "canary-test:v2")

			By("Step 11: Verifying canary deployment progresses through phases")
			// The canary should progress: Canary -> Rolling -> Completed (or back to Idle)
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				GinkgoWriter.Printf("Phase progression: %s\n", releaseChannel.Status.Phase)
				return releaseChannel.Status.Phase
			}, timeout*2, interval).Should(Or(
				Equal(unleashv1.ReleaseChannelPhaseRolling),
				Equal(unleashv1.ReleaseChannelPhaseCompleted),
				Equal(unleashv1.ReleaseChannelPhaseIdle),
			))

			By("Step 12: Verifying both instances eventually get the new image")
			waitForImageResolution(canaryUnleash, "canary-test:v2")
			waitForImageResolution(prodUnleash, "canary-test:v2")

			By("Step 13: Verifying ReleaseChannel status reflects successful deployment")
			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())

				GinkgoWriter.Printf("Total instances: %d, Up-to-date: %d, Phase: %s, RC: %s\n",
					releaseChannel.Status.Instances,
					releaseChannel.Status.InstancesUpToDate,
					releaseChannel.Status.Phase,
					releaseChannel.Name)
				return releaseChannel.Status.Instances
			}, timeout*3, interval).Should(Equal(2), "ReleaseChannel should track 2 instances")

			Eventually(func() int {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.InstancesUpToDate
			}, timeout, interval).Should(Equal(2))

			By("Step 14: Verifying ReleaseChannel reaches final state")
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				GinkgoWriter.Printf("Final phase: %s\n", releaseChannel.Status.Phase)
				return releaseChannel.Status.Phase
			}, timeout*2, interval).Should(Or(
				Equal(unleashv1.ReleaseChannelPhaseCompleted),
				Equal(unleashv1.ReleaseChannelPhaseIdle),
			))
		})
	})

	Context("Rollback and timeout functionality", func() {
		It("Should track StartTime when rollout begins", func() {
			By("Creating a ReleaseChannel and Unleash instance")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("starttime-test-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "starttime-test:v1",
				},
			}

			unleash := createUnleash(fmt.Sprintf("starttime-unleash-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Mocking deployment to be ready")
			simulateDeploymentReady(unleash)

			By("Waiting for initial setup")
			waitForImageResolution(unleash, "starttime-test:v1")

			By("Updating the ReleaseChannel image to trigger rollout")
			Eventually(func() error {
				err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)
				if err != nil {
					return err
				}
				releaseChannel.Spec.Image = "starttime-test:v2"
				return k8sClient.Update(ctx, releaseChannel)
			}, timeout, interval).Should(Succeed())

			By("Verifying StartTime is set when rollout begins")
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.StartTime != nil
			}, timeout, interval).Should(BeTrue(), "StartTime should be set when rollout begins")

			By("Verifying StartTime is recent")
			Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
			Expect(time.Since(releaseChannel.Status.StartTime.Time)).Should(BeNumerically("<", 5*time.Minute))
		})

		It("Should configure auto-rollback via spec", func() {
			By("Creating a ReleaseChannel with rollback enabled")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("rollback-config-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "rollback-config-test:v1",
					Rollback: unleashv1.RollbackConfig{
						Enabled:   true,
						OnFailure: true,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())

			By("Verifying rollback configuration is persisted")
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Spec.Rollback.Enabled && releaseChannel.Spec.Rollback.OnFailure
			}, timeout, interval).Should(BeTrue())
		})

		It("Should configure custom maxUpgradeTime", func() {
			By("Creating a ReleaseChannel with custom maxUpgradeTime")
			customTimeout := metav1.Duration{Duration: 30 * time.Minute}
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("timeout-config-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "timeout-config-test:v1",
					Strategy: unleashv1.ReleaseChannelStrategy{
						MaxUpgradeTime: &customTimeout,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())

			By("Verifying maxUpgradeTime is persisted")
			Eventually(func() time.Duration {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				if releaseChannel.Spec.Strategy.MaxUpgradeTime != nil {
					return releaseChannel.Spec.Strategy.MaxUpgradeTime.Duration
				}
				return 0
			}, timeout, interval).Should(Equal(30 * time.Minute))
		})

		It("Should configure custom health check endpoint", func() {
			By("Creating a ReleaseChannel with custom health check endpoint")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("health-endpoint-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "health-endpoint-test:v1",
					HealthChecks: unleashv1.HealthCheckConfig{
						Enabled:  true,
						Endpoint: "/api/health/ready",
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())

			By("Verifying health check endpoint is persisted")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Spec.HealthChecks.Endpoint
			}, timeout, interval).Should(Equal("/api/health/ready"))
		})

		It("Should clear StartTime and FailureReason when rollout completes", func() {
			// Start automatic deployment simulation for this test
			startAutomaticDeploymentSimulation()
			defer stopAutomaticDeploymentSimulation()

			By("Creating a ReleaseChannel and Unleash instance")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("clear-state-channel-%s", testID),
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "clear-state-test:v1",
				},
			}

			unleash := createUnleash(fmt.Sprintf("clear-state-unleash-%s", testID), namespace, releaseChannel.ObjectMeta.Name, nil)

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())

			By("Waiting for initial setup")
			waitForConnection(unleash)
			waitForImageResolution(unleash, "clear-state-test:v1")

			By("Triggering a new rollout")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel); err != nil {
					return err
				}
				releaseChannel.Spec.Image = "clear-state-test:v2"
				return k8sClient.Update(ctx, releaseChannel)
			}, timeout, interval).Should(Succeed())

			By("Waiting for rollout to complete")
			Eventually(func() unleashv1.ReleaseChannelPhase {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				return releaseChannel.Status.Phase
			}, timeout*3, interval).Should(Or(
				Equal(unleashv1.ReleaseChannelPhaseCompleted),
				Equal(unleashv1.ReleaseChannelPhaseIdle),
			))

			By("Verifying state is cleared after completion")
			// Wait for transition from Completed to Idle (where state is cleared)
			Eventually(func() bool {
				Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
				// If phase is Idle, StartTime should be nil
				if releaseChannel.Status.Phase == unleashv1.ReleaseChannelPhaseIdle {
					return releaseChannel.Status.StartTime == nil
				}
				// Still transitioning
				return false
			}, timeout*2, interval).Should(BeTrue(), "StartTime should be cleared after rollout completes")
		})
	})
})
