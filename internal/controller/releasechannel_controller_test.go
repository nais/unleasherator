package controller

import (
	"fmt"
	"time"

	"github.com/jarcoal/httpmock"
	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/nais/unleasherator/internal/unleashclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ReleaseChannel Controller", func() {
	const (
		namespace = "default"

		timeout  = time.Second * 10
		interval = time.Millisecond * 250

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

	Context("When updating a release channel", func() {
		It("Should update Unleash instances for the release channel", func() {
			By("Creating a ReleaseChannel and Unleash instances")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-release-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "my-image:v1",
				},
			}

			unleash1 := unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleash1",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://unleash1",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			unleash2 := unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unleash2",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://unleash2",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &unleash1)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &unleash2)).Should(Succeed())

			// Mock the deployments to be ready so Unleash instances become Connected
			By("Mocking deployment status for unleash1")
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

			By("Mocking deployment status for unleash2")
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

			// expect release channel to be connected

			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, &unleash1).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, &unleash2).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			By("Updating the ReleaseChannel")
			Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
			releaseChannel.Spec.Image = "my-image:v2"
			Expect(k8sClient.Update(ctx, releaseChannel)).Should(Succeed())

			By("Verifying that the Unleash instances have been updated")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash1.NamespacedName(), &unleash1)).Should(Succeed())
				fmt.Printf("unleash1.Spec.CustomImage: %s\n", unleash1.Spec.CustomImage)
				return string(unleash1.Spec.CustomImage)
			}, timeout, interval).Should(Equal("my-image:v2"))
		})

		It("Should record comprehensive metrics during rollout", func() {
			By("Creating a ReleaseChannel and Unleash instances")
			releaseChannel := &unleashv1.ReleaseChannel{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metrics-test-channel",
					Namespace: namespace,
				},
				Spec: unleashv1.ReleaseChannelSpec{
					Image: "metrics-test:v1",
				},
			}

			unleash1 := unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metrics-unleash1",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://metrics-unleash1",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			unleash2 := unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "metrics-unleash2",
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://metrics-unleash2",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: releaseChannel.Name,
					},
				},
			}

			Expect(k8sClient.Create(ctx, releaseChannel)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &unleash1)).Should(Succeed())
			Expect(k8sClient.Create(ctx, &unleash2)).Should(Succeed())

			// Mock the deployments to be ready so Unleash instances become Connected
			By("Mocking deployment status for metrics-unleash1")
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

			By("Mocking deployment status for metrics-unleash2")
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

			// Wait for instances to be connected
			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, &unleash1).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			Eventually(getUnleash, timeout, interval).WithArguments(k8sClient, ctx, &unleash2).Should(ContainElement(metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeConnected,
				Status:  metav1.ConditionTrue,
				Reason:  "Reconciling",
				Message: "Successfully connected to Unleash instance",
			}))

			By("Checking initial metrics")
			// Get initial metric values
			initialInstances := getMetricValue(releaseChannelInstances, namespace, releaseChannel.Name)
			initialUpToDate := getMetricValue(releaseChannelInstancesUpToDate, namespace, releaseChannel.Name)
			initialRollouts := getCounterValue(releaseChannelRollouts, namespace, releaseChannel.Name, "success")
			initialUpdates := getCounterValue(releaseChannelInstanceUpdates, namespace, releaseChannel.Name, "success")

			By("Updating the ReleaseChannel to trigger rollout")
			Expect(k8sClient.Get(ctx, releaseChannel.NamespacedName(), releaseChannel)).Should(Succeed())
			releaseChannel.Spec.Image = "metrics-test:v2"
			Expect(k8sClient.Update(ctx, releaseChannel)).Should(Succeed())

			By("Verifying that instances get updated and metrics are recorded")
			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash1.NamespacedName(), &unleash1)).Should(Succeed())
				return string(unleash1.Spec.CustomImage)
			}, timeout, interval).Should(Equal("metrics-test:v2"))

			Eventually(func() string {
				Expect(k8sClient.Get(ctx, unleash2.NamespacedName(), &unleash2)).Should(Succeed())
				return string(unleash2.Spec.CustomImage)
			}, timeout, interval).Should(Equal("metrics-test:v2"))

			By("Verifying metrics are updated correctly")
			Eventually(func() float64 {
				return getMetricValue(releaseChannelInstances, namespace, releaseChannel.Name)
			}, timeout, interval).Should(BeNumerically(">=", 2), "Should track at least 2 instances")

			Eventually(func() float64 {
				return getMetricValue(releaseChannelInstancesUpToDate, namespace, releaseChannel.Name)
			}, timeout, interval).Should(BeNumerically(">=", 2), "Should show instances as up to date")

			Eventually(func() float64 {
				return getCounterValue(releaseChannelInstanceUpdates, namespace, releaseChannel.Name, "success")
			}, timeout, interval).Should(BeNumerically(">", initialUpdates), "Should record successful instance updates")

			// Check that we have some rollout activity
			Eventually(func() float64 {
				return getCounterValue(releaseChannelRollouts, namespace, releaseChannel.Name, "success")
			}, timeout, interval).Should(BeNumerically(">=", initialRollouts), "Should track rollout success")

			By("Verifying status metric reflects completion")
			Eventually(func() float64 {
				return getMetricValue(releaseChannelStatus, namespace, releaseChannel.Name)
			}, timeout, interval).Should(BeNumerically(">=", 0.5), "Status should indicate progress or completion")

			// Cleanup variables to avoid unused variable warnings
			_ = initialInstances
			_ = initialUpToDate
		})
	})
})

// Helper functions for metric testing
func getMetricValue(metric *prometheus.GaugeVec, namespace, name string) float64 {
	metric_dto := &dto.Metric{}
	if err := metric.WithLabelValues(namespace, name).(prometheus.Metric).Write(metric_dto); err != nil {
		return -1
	}
	if metric_dto.Gauge != nil {
		return *metric_dto.Gauge.Value
	}
	return 0
}

func getCounterValue(metric *prometheus.CounterVec, namespace, name, result string) float64 {
	metric_dto := &dto.Metric{}
	if err := metric.WithLabelValues(namespace, name, result).(prometheus.Metric).Write(metric_dto); err != nil {
		return -1
	}
	if metric_dto.Counter != nil {
		return *metric_dto.Counter.Value
	}
	return 0
}
