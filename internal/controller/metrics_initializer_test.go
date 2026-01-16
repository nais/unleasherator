package controller

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

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
})
