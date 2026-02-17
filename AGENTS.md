# AI Agent Instructions

Kubernetes operator for Unleash feature flags. Go, Kubebuilder, controller-runtime.

## Core Principles

Follow [tiger style](https://tigerstyle.dev/): safety, performance, developer experience.

**Communication:**

- Be concise. No verbose summaries.
- Code over words. Reference docs over explanations.
- Inline comments explain *why*, never *what*.

**Workflow:**

- Always run `make all` after code changes (fmt, lint, vet, build, generate, helm).
- Never create documentation files.
- Make it work, make it right, make it fast.

**Key Commands:**

- `make all` - Full build pipeline
- `make test` - Run unit tests
- `make test FOCUS="test name"` - Run specific test
- `make build` - Build manager binary
- `make lint` - Run golangci-lint
- `make fmt` - Format code

## Testing

**Strategy:**

- **Unit tests** - Controller reconciliation with envtest (shared in-memory etcd)
- **Schema validation** - Types validated against official Unleash OpenAPI schemas (v5/v6/v7)
- **E2E tests** - Helm chart testing with real Kubernetes cluster

**Commands:**

- `make test` - Run all unit tests
- `make test FOCUS="test name"` - Run specific test
- `make test-schemas` - Validate types against Unleash API schemas
- `make test-e2e` - Run end-to-end helm chart tests

**Coverage:**

- Controllers must handle all reconciliation paths
- Error handling validated via httpmock responses
- Schema tests prevent API drift on Unleash upgrades

### httpmock Isolation

All 4 controllers run continuously via shared `k8sManager` and reconcile resources from ALL tests concurrently. Global responder reset doesn't work—use unique URLs per test instead:

```go
// Generate unique URL per test using resource name + namespace
func mockRemoteUnleashURL(name, namespace string) string {
    return fmt.Sprintf("http://%s.%s", name, namespace)
}

// Register mocks for specific URL only
func registerHTTPMocksForRemoteUnleash(remoteUnleash *unleashv1.RemoteUnleash, version string) {
    serverURL := remoteUnleash.Spec.Server.URL
    httpmock.RegisterResponder("GET", serverURL+"/api/admin/instance-admin/statistics",
        httpmock.NewJsonResponderOrPanic(200, map[string]any{"versionOSS": version}))
}

// In test
BeforeEach(func() {
    name := fmt.Sprintf("test-%d-%d", time.Now().UnixNano(), testCounter)
    serverURL := mockRemoteUnleashURL(name, namespace)
    remoteUnleash := makeRemoteUnleash(name, namespace, serverURL, secretName)
    registerHTTPMocksForRemoteUnleash(remoteUnleash, "6.1.0")
})
```

Why: `instance.URL()` returns `http://<name>.<namespace>`, giving each test a unique hostname. Responders registered with full URLs only match their specific test's requests.

**Rules:**

- Never use path-only patterns like `=~/api/.*` (matches all hosts)
- Use full URLs: `serverURL + "/api/admin/..."`
- Call count checks must use full URL keys: `httpmock.GetCallCountInfo()[serverURL+"/api/..."]`

### envtest Isolation

All controller tests share one in-memory etcd. Prevent cache pollution:

```go
var testCounter int

BeforeEach(func() {
    testCounter++
    namespace := fmt.Sprintf("test-%d", testCounter)

    ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
    Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
    DeferCleanup(func() { _ = k8sClient.Delete(ctx, ns) })
})
```

Why: Unique namespaces prevent race conditions and cross-test interference in parallel execution.

**Rules:**

- No hardcoded namespaces
- No `GinkgoRandomSeed()` (same value per suite run)
- Use `DeferCleanup` for guaranteed cleanup
- Combine timestamp + counter for unique resource names when needed

### Avoiding Flaky Tests

**Use standardized timeouts from `test_constants_test.go`:**

```go
// Good - use standardized constants
Eventually(...).WithTimeout(TestTimeoutMedium).WithPolling(TestInterval).Should(...)

// Bad - arbitrary per-test timeouts
const timeout = time.Millisecond * 1234
```

**Wait for stable state before assertions:**

```go
// Good - wait for controller to finish reconciling
waitForCondition(ctx, k8sClient, unleash,
    unleashv1.UnleashStatusConditionTypeConnected,
    metav1.ConditionTrue, TestTimeoutMedium, TestInterval)

// Bad - check status immediately after creation
Expect(unleash.Status.Connected).To(BeTrue()) // May fail if controller hasn't run yet
```

**Use atomic counter for unique IDs:**

```go
// Good - guaranteed unique across parallel execution
testID := generateTestID() // uses atomic.AddUint64

// Bad - can collide within same nanosecond
testID := fmt.Sprintf("%d", time.Now().UnixNano())
```

**Handle optimistic locking conflicts:**

```go
// Good - retry on conflict
Eventually(func() error {
    Expect(k8sClient.Get(ctx, key, obj)).Should(Succeed())
    obj.Status.Field = newValue
    return k8sClient.Status().Update(ctx, obj)
}, TestTimeoutShort, TestInterval).Should(Succeed())

// Bad - single update attempt
obj.Status.Field = newValue
Expect(k8sClient.Status().Update(ctx, obj)).Should(Succeed()) // May fail with conflict
```

**Don't rely on httpmock call counts for shared resources:**

```go
// Good - check final state
Eventually(func() string {
    Expect(k8sClient.Get(ctx, key, obj)).Should(Succeed())
    return obj.Status.Version
}, TestTimeoutMedium, TestInterval).Should(Equal("v5.1.2"))

// Bad - call count can include background reconciliations from other tests
Expect(httpmock.GetCallCountInfo()[endpoint]).To(Equal(1))
```
