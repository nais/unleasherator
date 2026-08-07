package unleashclient

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
