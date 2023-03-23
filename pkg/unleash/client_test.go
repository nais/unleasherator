package unleash

import (
	"testing"
)

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
