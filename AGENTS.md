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

**Flaky tests:**

Controller tests share a single in-memory etcd and 4 continuously running controllers. This makes some tests sensitive to ordering and timing. If tests fail during feature work, **rerun before debugging** — intermittent failures are expected. Only investigate if a test fails consistently.

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

**Never use small fixed-size channel buffers with `prometheus.Collect`:**

```go
// Good - large buffer prevents Collect from blocking when many metrics exist
ch := make(chan prometheus.Metric, 1024)
gaugeVec.Collect(ch)
close(ch)

// Bad - blocks forever if more than 16 metric series exist
ch := make(chan prometheus.Metric, 16)
gaugeVec.Collect(ch) // deadlocks when 4 controllers create metrics concurrently
close(ch)
```

Why: All 4 controllers run continuously and create metrics for every reconciled resource. With `GaugeVec` labels like `(name, status, version, release_channel)`, the total series count grows with each test's resources. A buffer smaller than the total series count causes `Collect` to block on channel send forever — the goroutine dump shows `prometheus.(*metricMap).Collect` stuck on `chan send`.

**Test timeout:**

`make test` uses `-race -count=1 -timeout 180s`. The race detector adds 2-10x overhead; account for this in test timeouts.

### testify/mock Thread Safety

`testify/mock` is NOT safe to use from concurrent goroutines without care. Controllers run continuously and call mock methods concurrently with test assertions.

**Never read `mock.Calls` directly:**

```go
// Good - thread-safe call count check inside Eventually
Eventually(func() bool {
    return mockPublisher.AssertNumberOfCalls(&silentT{}, "Publish", 1)
}, timeout, interval).Should(BeTrue())

// Bad - direct slice access races with controller goroutines appending
Eventually(func() int {
    return len(mockPublisher.Calls)
}, timeout, interval).Should(Equal(1))
```

**Never use `AssertExpectations` on mocks whose arguments include shared pointers:**

```go
// Good - check call counts only (no argument inspection via reflect)
mockPublisher.AssertNumberOfCalls(&silentT{}, "PublishRemoved", 1)

// Bad - reads arguments via reflect while controller writes to same *Unleash pointer
mockPublisher.AssertExpectations(GinkgoT()) // DATA RACE
```

Why: `AssertExpectations` calls `Arguments.Diff` which uses `fmt.Sprintf` + `reflect` to inspect recorded call arguments. If those arguments include `*Unleash` pointers, and the controller's `r.Get()` concurrently deserializes into the same pointer, the race detector fires.

**Use `silentT` for mock assertions inside `Eventually`:**

```go
type silentT struct{}
func (silentT) Logf(string, ...interface{})  {}
func (silentT) Errorf(string, ...interface{}) {}
func (silentT) FailNow()                     {}

// GinkgoT().Errorf triggers Fail()/panic, preventing Eventually from retrying
Eventually(func() bool {
    return mock.AssertNumberOfCalls(&silentT{}, "Method", 1)
}, timeout, interval).Should(BeTrue())
```

**Use channels instead of polling `mock.Calls` for handler capture:**

```go
// Good - channel-based, race-free
handlerCh := make(chan federation.Handler, 1)
mockSubscriber.On("Subscribe", mock.Anything, mock.MatchedBy(func(h federation.Handler) bool {
    select {
    case handlerCh <- h:
    default:
    }
    return true
})).Once().WaitUntil(blockCh).Return(nil)

var handler federation.Handler
Eventually(handlerCh, timeout, interval).Should(Receive(&handler))

// Bad - races between length check and index access
Eventually(func() int { return len(mock.Calls) }).Should(Equal(1))
handler := mock.Calls[0].Arguments.Get(1).(Handler) // race
```

**Reset mocks safely between tests:**

```go
// Good - set Maybe() expectations to absorb in-flight calls, clear call log
mockPublisher.ExpectedCalls = []*mock.Call{
    mockPublisher.On("Publish", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil),
    mockPublisher.On("PublishRemoved", mock.Anything, mock.Anything).Maybe().Return(nil),
}
mockPublisher.Calls = nil

// Bad - leaves mock briefly in an invalid state; in-flight calls panic
mockPublisher.Mock = mock.Mock{}
```

Note: Direct field assignment (`ExpectedCalls`, `Calls`) bypasses testify's internal mutex. This is unavoidable since testify provides no thread-safe `Reset()` API. The race window is minimal during `BeforeEach`.

### httpmock Lifecycle

**Never use `DeactivateAndReset` + `Activate` between tests:**

```go
// Good - reset state without touching http.DefaultTransport
httpmock.Reset()
httpmock.ZeroCallCounters()

// Bad - briefly restores http.DefaultTransport, racing with in-flight requests
httpmock.DeactivateAndReset()
httpmock.Activate()
```

Why: `DeactivateAndReset` restores `http.DefaultTransport` for a brief moment. Any controller goroutine making an HTTP request during that window hits the real network instead of the mock.

### Avoiding Shared Mutable State

**Don't use package-level variables that accumulate across tests:**

```go
// Good - local variable scoped to test
localTokens := ApiTokenResult{Tokens: []ApiToken{}}
httpmock.RegisterResponder("POST", ..., func(req *http.Request) (*http.Response, error) {
    localTokens.Tokens = append(localTokens.Tokens, newToken)
    return httpmock.NewJsonResponse(201, newToken)
})

// Bad - shared variable leaks state between tests
var existingTokens ApiTokenResult // package-level
httpmock.RegisterResponder("POST", ..., func(req *http.Request) (*http.Response, error) {
    existingTokens.Tokens = append(existingTokens.Tokens, newToken) // accumulates across tests
    return httpmock.NewJsonResponse(201, newToken)
})
```

### Avoiding Pointer Aliasing with Controllers

**Pass immutable keys instead of mutable object pointers to background goroutines:**

```go
// Good - NamespacedName is a value type, safe to share
key := instance.NamespacedName()
go func() {
    deployment := &appsv1.Deployment{}
    k8sClient.Get(ctx, key, deployment)
}()

// Bad - controller may call r.Get() into the same *Unleash pointer concurrently
go func() {
    deployment := &appsv1.Deployment{}
    k8sClient.Get(ctx, instance.NamespacedName(), deployment) // reads instance.Name/Namespace while controller writes
}()
```
