package unleashclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewClient(t *testing.T) {
	_, err := NewClient("http://localhost:4242", "test")

	if err != nil {
		t.Errorf("Unexpected error: %s", err)
	}
}

func TestRequestURL(t *testing.T) {
	client, _ := NewClient("http://localhost:4242/", "test")

	if client.requestURL("/api").String() != "http://localhost:4242/api" {
		t.Errorf("Expected URL to be http://localhost:4242/api, got %s", client.requestURL("/api").String())
	}

	if client.requestURL("//api/").String() != "http://localhost:4242/api" {
		t.Errorf("Expected URL to be http://localhost:4242/api, got %s", client.requestURL("/api/").String())
	}

	if client.URL.String() != "http://localhost:4242/" {
		t.Errorf("Expected URL to be http://localhost:4242/, got %s", client.URL.String())
	}
}

func TestHTTPDeleteSanitizesTokenFromTransportError(t *testing.T) {
	const token = "sensitive-token"
	client, err := NewClientWithHttpClient("https://unleash.example.com", "admin-token", &http.Client{
		Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, &url.Error{
				Op:  "Delete",
				URL: req.URL.String(),
				Err: errors.New("connection refused"),
			}
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = client.HTTPDelete(context.Background(), "/api/admin/api-tokens", token)
	if err == nil {
		t.Fatal("expected HTTP DELETE to fail")
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("transport error leaked token: %v", err)
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("transport error lost cause: %v", err)
	}
}

func TestHTTPDeleteKeepsSecretOutOfSpans(t *testing.T) {
	const (
		tokenSecret = "project:development.5ec4e7"
		adminToken  = "admin-token"
	)

	var (
		requestURL   string
		authHeader   string
		requestCount int
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		requestURL = r.URL.String()
		authHeader = r.Header.Get("Authorization")
	}))
	defer server.Close()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	client, err := NewClientWithHttpClient(server.URL, adminToken, &http.Client{
		Transport: newTransport(http.DefaultTransport, otelhttp.WithTracerProvider(provider)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := client.DeleteApiToken(context.Background(), tokenSecret); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The delete has to keep working: Unleash only accepts the secret as a path
	// segment, so redaction must not reach the wire.
	if requestCount != 1 {
		t.Fatalf("expected 1 request, got %d", requestCount)
	}
	if want := ApiTokensEndpoint + "/" + tokenSecret; requestURL != want {
		t.Fatalf("expected request URL %q, got %q", want, requestURL)
	}
	if authHeader != adminToken {
		t.Fatalf("expected admin token in Authorization header, got %q", authHeader)
	}

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("expected the delete to be traced")
	}
	for _, span := range spans {
		if strings.Contains(span.Name(), tokenSecret) {
			t.Errorf("span name leaked token secret: %s", span.Name())
		}
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.Emit(), tokenSecret) {
				t.Errorf("span attribute %s leaked token secret: %s", attr.Key, attr.Value.Emit())
			}
		}
	}
}

func TestHTTPDeleteKeepsAdminTokenOutOfRequestURL(t *testing.T) {
	const adminToken = "admin-token"

	var requestURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURL = r.URL.String()
	}))
	defer server.Close()

	client, err := NewClientWithHttpClient(server.URL, adminToken, server.Client())
	if err != nil {
		t.Fatal(err)
	}

	if err := client.DeleteApiToken(context.Background(), "some-token"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(requestURL, adminToken) {
		t.Fatalf("admin token leaked into request URL: %s", requestURL)
	}
}

func TestNewClientHasTimeout(t *testing.T) {
	client, err := NewClient("http://localhost:4242", "test")
	if err != nil {
		t.Fatal(err)
	}

	if client.HttpClient.Timeout == 0 {
		t.Fatal("expected the client to bound requests with a timeout")
	}
	if client.HttpClient.Timeout != DefaultTimeout {
		t.Fatalf("expected timeout %s, got %s", DefaultTimeout, client.HttpClient.Timeout)
	}
}

func TestHTTPGetGivesUpOnHungServer(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-released
	}))
	defer server.Close()
	defer close(released)

	original := DefaultTimeout
	DefaultTimeout = 100 * time.Millisecond
	defer func() { DefaultTimeout = original }()

	client, err := NewClient(server.URL, "test")
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := client.HTTPGet(context.Background(), HealthEndpoint, &HealthResult{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected the hung request to fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("request to a hung server never returned")
	}
}

func TestHTTPGetRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := make([]byte, 1<<20)
		for written := 0; written <= maxResponseBytes; written += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	client, err := NewClientWithHttpClient(server.URL, "test", server.Client())
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.HTTPGet(context.Background(), HealthEndpoint, &HealthResult{})
	if err == nil {
		t.Fatal("expected an oversized response body to be rejected")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected a size limit error, got: %v", err)
	}
}

// countingBody reports whether the response body was closed, which is what
// returns the connection to the pool.
type countingBody struct {
	io.Reader
	closed bool
}

func (b *countingBody) Close() error {
	b.closed = true
	return nil
}

func TestHTTPGetClosesResponseBody(t *testing.T) {
	for name, status := range map[string]int{"ok": http.StatusOK, "error": http.StatusInternalServerError} {
		t.Run(name, func(t *testing.T) {
			body := &countingBody{Reader: strings.NewReader(`{"health":"GOOD"}`)}
			client, err := NewClientWithHttpClient("http://unleash.example.com", "test", &http.Client{
				Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: status, Body: body, Header: http.Header{}}, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}

			//nolint:errcheck // the error is irrelevant, the body must be closed either way
			client.HTTPGet(context.Background(), HealthEndpoint, &HealthResult{})

			if !body.closed {
				t.Fatal("HTTPGet leaked the response body")
			}
		})
	}
}
