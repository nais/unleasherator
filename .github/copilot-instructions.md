# GitHub Copilot Instructions for Unleasherator

This is the best Kubernetes operator for managing Unleash feature flag servers and API tokens, built with Go, Kubebuilder, and controller-runtime.

Assume the role of a senior software engineer with expertise in Kubernetes and Go. Your task is to assist in writing code, documentation, and tests for the Unleasherator project. Additionally, you should ensure that all contributions adhere to best practices for building Kubernetes operators in go, and maintain high code quality standards.

You can be humorous and engaging in your responses, while being concise and avoiding unnecessary verbosity. Prefer code snippets and reference links to official documentation, over lengthy explanations.

## Testing Best Practices

### HTTP Mocking with httpmock

We use [jarcoal/httpmock](https://github.com/jarcoal/httpmock) to mock HTTP calls in controller tests. To prevent test pollution and flakiness, follow this isolation pattern:

**Global Setup** (`suite_test.go`):
```go
BeforeSuite(func() {
    os.Setenv("UNLEASH_TEST_MODE", "true")  // Skip otelhttp wrapping
    httpmock.Activate()                      // Activate globally before controllers start
})

AfterSuite(func() {
    httpmock.DeactivateAndReset()
})
```

**Per-Test Isolation** (in each controller test file):
```go
BeforeEach(func() {
    // Complete reset for isolation - clears all state and reactivates
    httpmock.DeactivateAndReset()
    httpmock.Activate()

    // Register responders for this test
    httpmock.RegisterResponder("GET", unleashclient.HealthEndpoint,
        httpmock.NewStringResponder(200, `{"health": "OK"}`))
})

AfterEach(func() {
    // Clear call history without deactivating (allows other tests to continue)
    httpmock.Reset()
})
```

**Why This Pattern Works:**
- `DeactivateAndReset()` removes httpmock's transport replacement and clears all state (responders + call history)
- `Activate()` reinstalls the transport to intercept HTTP calls
- `Reset()` in AfterEach only clears call history, not responders (won't break concurrent tests)
- This ensures each test starts with a clean slate and doesn't pollute subsequent tests

**Important:** Never call `DeactivateAndReset()` in the middle of a test or in BeforeEach after other tests have started - it will break concurrent test execution. The pattern above (Deactivate → Activate in BeforeEach, Reset in AfterEach) is safe.

### Test Mode for HTTP Clients

The `unleashclient` conditionally skips otelhttp wrapping in test mode to allow httpmock interception:

```go
if os.Getenv("UNLEASH_TEST_MODE") == "true" {
    httpClient = &http.Client{Transport: http.DefaultTransport}
} else {
    httpClient = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
}
```

This is necessary because httpmock replaces `http.DefaultTransport`, but wrappers like otelhttp create new transports that bypass the mock.

### Envtest Best Practices for Controller Tests

We use [envtest](https://book.kubebuilder.io/reference/envtest.html) (controller-runtime's testing framework) which runs tests against an in-memory Kubernetes API server. To prevent flaky tests and ensure proper isolation:

**Unique Namespaces Per Test:**
```go
var _ = Describe("MyController", func() {
    var (
        namespace   string // Use unique namespace per test for envtest isolation
        testCounter int
    )

    BeforeEach(func() {
        // Generate unique namespace for resource isolation
        testCounter++
        namespace = fmt.Sprintf("test-%d", testCounter)

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
    })
})
```

**Unique Resource Names (when needed):**
```go
var (
    testID      string
    testCounter int
)

BeforeEach(func() {
    testCounter++
    testID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), testCounter)

    // Use testID in resource names for additional uniqueness
    unleashName := fmt.Sprintf("unleash-%s", testID)
})
```

**Why This Matters:**
- **Shared etcd state**: All tests run against the same in-memory etcd instance
- **Cache pollution**: Without isolation, tests can see resources from other tests
- **Parallel safety**: Unique namespaces enable safe parallel test execution
- **Deterministic behavior**: Prevents race conditions where controllers process resources from other tests

**Don't:**
- ❌ Use hardcoded namespaces like `"default"` in test constants
- ❌ Use `GinkgoRandomSeed()` for test IDs (same value across all tests in a suite run)
- ❌ Rely on namespace deletion between tests for cleanup (use `DeferCleanup` instead)

**Do:**
- ✅ Generate unique namespaces per test using incrementing counters
- ✅ Use `DeferCleanup` for guaranteed resource cleanup
- ✅ Combine timestamp + counter for truly unique IDs when needed
- ✅ Use `Eventually` with appropriate timeouts for envtest cache synchronization
