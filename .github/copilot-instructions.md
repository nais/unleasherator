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
