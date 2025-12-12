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

HTTP mocking requires careful lifecycle management to prevent test pollution:

```go
// suite_test.go - global activation
BeforeSuite(func() {
    os.Setenv("UNLEASH_TEST_MODE", "true")
    httpmock.Activate()
})

// controller_test.go - per-test reset
BeforeEach(func() {
    httpmock.DeactivateAndReset()  // Complete state reset
    httpmock.Activate()             // Reinstall transport
    httpmock.RegisterResponder(...)
})

AfterEach(func() {
    httpmock.Reset()  // Clear history only, safe for parallel tests
})
```

Why: `DeactivateAndReset()` clears both responders and history; `Reset()` only clears history. Never deactivate mid-test or after other tests start.

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
