package controller

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	unleashv1 "github.com/nais/unleasherator/api/v1"
)

// Global atomic counter for test isolation - ensures uniqueness across parallel test executions
var globalTestCounter uint64

// generateTestID creates a unique identifier combining timestamp and atomic counter.
// This guarantees uniqueness even when tests run within the same nanosecond.
func generateTestID() string {
	counter := atomic.AddUint64(&globalTestCounter, 1)
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), counter)
}

// waitForCondition waits for a specific condition to be set on the resource status.
// This is more reliable than checking a specific status value because it handles
// the transient states during controller reconciliation.
func waitForCondition[T client.Object](
	ctx context.Context,
	k8sClient client.Client,
	obj T,
	conditionType string,
	expectedStatus metav1.ConditionStatus,
	timeout time.Duration,
	interval time.Duration,
) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).Should(Succeed())

		var conditions []metav1.Condition
		switch v := any(obj).(type) {
		case *unleashv1.Unleash:
			conditions = v.Status.Conditions
		case *unleashv1.RemoteUnleash:
			conditions = v.Status.Conditions
		case *unleashv1.ApiToken:
			conditions = v.Status.Conditions
		case *unleashv1.ReleaseChannel:
			// ReleaseChannel uses Phase instead of Conditions
			return
		}

		found := false
		for _, condition := range conditions {
			if condition.Type == conditionType {
				found = true
				g.Expect(condition.Status).To(Equal(expectedStatus),
					"condition %s should be %s, got %s: %s",
					conditionType, expectedStatus, condition.Status, condition.Message)
				break
			}
		}
		g.Expect(found).To(BeTrue(), "condition %s not found in status", conditionType)
	}, timeout, interval).Should(Succeed())
}

// waitForResourceStable waits until the resource's ResourceVersion stops changing,
// indicating the controller has finished reconciling. This helps avoid asserting
// on transient states.
func waitForResourceStable(
	ctx context.Context,
	k8sClient client.Client,
	obj client.Object,
	stableDuration time.Duration,
	timeout time.Duration,
	interval time.Duration,
) {
	var lastVersion string
	var stableStart time.Time

	Eventually(func() bool {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return false
		}

		currentVersion := obj.GetResourceVersion()
		if currentVersion != lastVersion {
			lastVersion = currentVersion
			stableStart = time.Now()
			return false
		}

		return time.Since(stableStart) >= stableDuration
	}, timeout, interval).Should(BeTrue(), "resource should become stable")
}

// retryOnConflict wraps an update function with conflict retry logic.
// This handles optimistic locking conflicts that occur when multiple controllers
// or test goroutines update the same resource.
func retryOnConflict(ctx context.Context, k8sClient client.Client, obj client.Object, updateFn func() error) error {
	maxRetries := 5
	for i := 0; i < maxRetries; i++ {
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		if err := updateFn(); err == nil {
			return nil
		}
		time.Sleep(time.Millisecond * 50)
	}
	return fmt.Errorf("failed after %d retries", maxRetries)
}

// logTestBoundary writes clear markers to GinkgoWriter for debugging test execution order
func logTestBoundary(phase, testName string) {
	GinkgoWriter.Printf("\n========== %s: %s ==========\n", phase, testName)
}
