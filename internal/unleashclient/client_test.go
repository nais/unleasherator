package unleashclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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
