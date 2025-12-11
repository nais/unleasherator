package unleashclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Client struct {
	URL        url.URL
	ApiToken   string
	HttpClient *http.Client
}

func NewClient(instanceUrl string, apiToken string) (*Client, error) {
	// In tests, create a new client using the current http.DefaultTransport
	// This allows httpmock to work since it replaces http.DefaultTransport
	var httpClient *http.Client
	if os.Getenv("UNLEASH_TEST_MODE") == "true" {
		// Create a new client that uses whatever http.DefaultTransport currently is
		// If httpmock has been activated, this will be the mock transport
		httpClient = &http.Client{Transport: http.DefaultTransport}
	} else {
		httpClient = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	}

	return NewClientWithHttpClient(instanceUrl, apiToken, httpClient)
}

func NewClientWithHttpClient(instanceUrl string, apiToken string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(instanceUrl)
	if err != nil {
		return nil, err
	}

	if apiToken == "" {
		return nil, fmt.Errorf("apiToken can not be empty")
	}

	return &Client{
		URL:        *u,
		ApiToken:   apiToken,
		HttpClient: httpClient,
	}, nil
}

// UnleashAPIError represents a structured error response from the Unleash API
type UnleashAPIError struct {
	StatusCode int
	ID         string `json:"id"`
	Name       string `json:"name"`
	Message    string `json:"message"`
	Details    []struct {
		Message     string `json:"message"`
		Description string `json:"description"`
	} `json:"details"`
	RawBody string
}

func (e *UnleashAPIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("unleash API error (HTTP %d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("unleash API error (HTTP %d): %s", e.StatusCode, e.RawBody)
}

// IsV7CompatibilityIssue detects if this is likely a v7 compatibility issue
func (e *UnleashAPIError) IsV7CompatibilityIssue() bool {
	return e.StatusCode == 400 &&
		(strings.Contains(strings.ToLower(e.Message), "tokenname") ||
			strings.Contains(strings.ToLower(e.RawBody), "tokenname") ||
			strings.Contains(strings.ToLower(e.Message), "projects") ||
			strings.Contains(strings.ToLower(e.Message), "project field"))
}

// parseAPIError attempts to parse an error response from Unleash API
func parseAPIError(statusCode int, body []byte) *UnleashAPIError {
	apiErr := &UnleashAPIError{
		StatusCode: statusCode,
		RawBody:    string(body),
	}

	// Try to unmarshal as structured error
	if err := json.Unmarshal(body, apiErr); err == nil && apiErr.Message != "" {
		return apiErr
	}

	// Return with just raw body if parsing fails
	return apiErr
}
func (c *Client) requestURL(requestPath string) *url.URL {
	req := new(url.URL)
	*req = c.URL
	req.Path = path.Join(c.URL.Path, requestPath)

	return req
}

func (c *Client) HTTPGet(ctx context.Context, requestPath string, v any) (*http.Response, error) {
	requestURL := c.requestURL(requestPath).String()
	requestMethod := "GET"

	req, err := http.NewRequestWithContext(ctx, requestMethod, requestURL, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", c.ApiToken)

	res, err := c.HttpClient.Do(req)
	if err != nil {
		return res, err
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return res, err
	}

	if res.StatusCode != http.StatusOK {
		return res, fmt.Errorf("unexpected http status code %d", res.StatusCode)
	}

	err = json.Unmarshal(body, v)
	if err != nil {
		return res, err
	}

	return res, nil
}

func (c *Client) HTTPDelete(ctx context.Context, requestPath string, item string) error {
	requestURL := c.requestURL(fmt.Sprintf("%s/%s", requestPath, item)).String()
	requestMethod := "DELETE"

	req, err := http.NewRequestWithContext(ctx, requestMethod, requestURL, nil)

	if err != nil {
		return err
	}

	req.Header.Add("Authorization", c.ApiToken)

	res, err := c.HttpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected http status code %d", res.StatusCode)
	}
	return nil
}

func (c *Client) HTTPPost(ctx context.Context, requestPath string, p, v any) (*http.Response, error) {
	requestURL := c.requestURL(requestPath).String()
	requestMethod := "POST"
	requestBody, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, requestMethod, requestURL, bytes.NewBuffer(requestBody))

	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", c.ApiToken)

	res, err := c.HttpClient.Do(req)
	if err != nil {
		return res, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return res, err
	}

	if res.StatusCode != http.StatusCreated {
		return res, parseAPIError(res.StatusCode, body)
	}

	err = json.Unmarshal(body, v)
	if err != nil {
		return res, err
	}

	return res, nil
}
