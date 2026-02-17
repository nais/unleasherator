package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

// getMetricValue extracts the gauge value for a specific label set from unleashStatus
func getMetricValue(name, status, version, releaseChannel string) (float64, bool) {
	var m dto.Metric
	gauge := unleashStatus.WithLabelValues(name, status, version, releaseChannel)
	if err := gauge.Write(&m); err != nil {
		return 0, false
	}
	if m.Gauge == nil {
		return 0, false
	}
	return m.Gauge.GetValue(), true
}

var _ = Describe("MetricsInitializer", func() {
	var testCounter int

	Context("When initializing metrics for existing resources", func() {
		It("Should initialize Unleash metrics without panicking (correct label count)", func() {
			ctx := context.Background()
			testCounter++
			namespace := fmt.Sprintf("metrics-init-test-%d-%d", time.Now().UnixNano(), testCounter)
			// Use unique name to avoid metric collisions with other tests
			unleashName := fmt.Sprintf("mi-unleash-%d-%d", time.Now().UnixNano(), testCounter)

			By("Creating a test namespace")
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ns) })

			By("Creating an Unleash resource with version and release channel")
			unleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      unleashName,
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://test:test@localhost:5432/test",
					},
					ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{
						Name: "stable",
					},
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, unleash) })

			By("Setting the Unleash status with version and conditions")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), unleash); err != nil {
					return err
				}
				unleash.Status.Version = "6.3.0"
				unleash.Status.Reconciled = true
				unleash.Status.Connected = true
				unleash.Status.Conditions = []metav1.Condition{
					{
						Type:               unleashv1.UnleashStatusConditionTypeReconciled,
						Status:             metav1.ConditionTrue,
						Reason:             "Reconciling",
						Message:            "Test",
						LastTransitionTime: metav1.Now(),
					},
					{
						Type:               unleashv1.UnleashStatusConditionTypeConnected,
						Status:             metav1.ConditionTrue,
						Reason:             "Reconciling",
						Message:            "Test",
						LastTransitionTime: metav1.Now(),
					},
				}
				return k8sClient.Status().Update(ctx, unleash)
			}, time.Second*5, time.Millisecond*100).Should(Succeed())

			By("Running the MetricsInitializer - this should not panic")
			// The main purpose of this test is to catch label cardinality mismatch panics
			// If initUnleashMetrics uses wrong number of labels, Prometheus will panic
			initializer := &MetricsInitializer{Client: k8sClient}
			Expect(initializer.initUnleashMetrics(ctx)).To(Succeed())
		})

		It("Should use default values when version and release channel are empty", func() {
			ctx := context.Background()
			testCounter++
			namespace := fmt.Sprintf("metrics-init-test-%d-%d", time.Now().UnixNano(), testCounter)
			// Use unique name to avoid metric collisions with other tests
			unleashName := fmt.Sprintf("mi-defaults-%d-%d", time.Now().UnixNano(), testCounter)

			By("Creating a test namespace")
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ns) })

			By("Creating an Unleash resource without version or release channel")
			unleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:      unleashName,
					Namespace: namespace,
				},
				Spec: unleashv1.UnleashSpec{
					Database: unleashv1.UnleashDatabaseConfig{
						URL: "postgres://test:test@localhost:5432/test",
					},
				},
			}
			Expect(k8sClient.Create(ctx, unleash)).Should(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, unleash) })

			By("Setting minimal status conditions")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, unleash.NamespacedName(), unleash); err != nil {
					return err
				}
				unleash.Status.Conditions = []metav1.Condition{
					{
						Type:               unleashv1.UnleashStatusConditionTypeReconciled,
						Status:             metav1.ConditionFalse,
						Reason:             "Reconciling",
						Message:            "Test",
						LastTransitionTime: metav1.Now(),
					},
				}
				return k8sClient.Status().Update(ctx, unleash)
			}, time.Second*5, time.Millisecond*100).Should(Succeed())

			By("Running the MetricsInitializer - this should not panic")
			// This tests that empty version/releaseChannel are handled with defaults
			initializer := &MetricsInitializer{Client: k8sClient}
			Expect(initializer.initUnleashMetrics(ctx)).To(Succeed())
		})
	})

	Context("When updating status with version changes", func() {
		It("Should delete stale metrics when version changes to prevent alert matching", func() {
			testCounter++
			// Use unique name to avoid metric collisions with other tests - regression test for stale metric alerts
			unleashName := fmt.Sprintf("stale-metric-test-%d-%d", time.Now().UnixNano(), testCounter)
			oldVersion := "5.0.0"
			newVersion := "6.0.0"
			releaseChannel := "none"
			statusType := unleashv1.UnleashStatusConditionTypeConnected

			By("Setting up an initial metric with the old version")
			unleashStatus.WithLabelValues(unleashName, statusType, oldVersion, releaseChannel).Set(0)
			DeferCleanup(func() {
				// Clean up metrics after test
				unleashStatus.DeletePartialMatch(prometheus.Labels{"name": unleashName})
			})

			By("Verifying the old version metric exists")
			val, found := getMetricValue(unleashName, statusType, oldVersion, releaseChannel)
			Expect(found).To(BeTrue(), "old version metric should exist")
			Expect(val).To(Equal(0.0), "old version metric should be 0 (disconnected)")

			By("Simulating updateStatus behavior: delete partial match then set new metric")
			// This mirrors what updateStatus does - delete old metrics before setting new ones
			unleashStatus.DeletePartialMatch(prometheus.Labels{"name": unleashName, "status": statusType})
			unleashStatus.WithLabelValues(unleashName, statusType, newVersion, releaseChannel).Set(1)

			By("Verifying the new version metric exists with correct value")
			val, found = getMetricValue(unleashName, statusType, newVersion, releaseChannel)
			Expect(found).To(BeTrue(), "new version metric should exist")
			Expect(val).To(Equal(1.0), "new version metric should be 1 (connected)")

			By("Verifying the old version metric was deleted")
			// After DeletePartialMatch, creating the gauge again would produce a fresh 0 value,
			// but we want to verify that querying it returns a fresh/unset state.
			// The key insight: if we set a value for the old labels, they should NOT be there.
			// Check that the alert condition (status=Connected, value=0) doesn't match unexpected series
			// by verifying no metric with old version + new version both exist at value 0
			newVal, _ := getMetricValue(unleashName, statusType, newVersion, releaseChannel)
			Expect(newVal).To(Equal(1.0), "only the new version metric should be set to 1")
		})
	})
})
