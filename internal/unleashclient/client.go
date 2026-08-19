package unleashclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// DefaultTimeout bounds a single Unleash API call. Reconcilers hand the client
// a context without a deadline, so without it a server that accepts the
// connection but never answers pins a reconcile worker forever. It is a var so
// tests can shorten it.
var DefaultTimeout = 30 * time.Second

// maxResponseBytes caps how much of a response body is buffered. The instance
// URL is user controlled through RemoteUnleash, so an unbounded read lets a
// hostile or broken server OOM the operator. The largest response we expect is
// a full token list, which is orders of magnitude smaller than this.
const maxResponseBytes = 10 << 20 // 10 MiB

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
		httpClient = &http.Client{Transport: http.DefaultTransport, Timeout: DefaultTimeout}
	} else {
		httpClient = &http.Client{Transport: newTransport(http.DefaultTransport), Timeout: DefaultTimeout}
	}

	return NewClientWithHttpClient(instanceUrl, apiToken, httpClient)
}

// redactedPathSegment stands in for a secret that the Unleash admin API insists
// on receiving as a path segment.
const redactedPathSegment = "REDACTED"

type contextKey int

const (
	// redactedURLContextKey carries the URL that instrumentation is allowed to
	// see, set by the request builder that knows which part is secret.
	redactedURLContextKey contextKey = iota

	// unredactedURLContextKey carries the real URL past instrumentation so the
	// layer closest to the network can put it back.
	unredactedURLContextKey
)

// withRedactedURL marks a request whose URL embeds a secret.
func withRedactedURL(ctx context.Context, redacted string) context.Context {
	return context.WithValue(ctx, redactedURLContextKey, redacted)
}

// newTransport builds the transport chain used against real Unleash instances.
// otelhttp records the full request URL as a span attribute, which would ship
// deleted token secrets to the trace backend, so redaction has to happen above
// otelhttp and restoration below it: the span sees a placeholder while only the
// wire sees the secret.
func newTransport(base http.RoundTripper, opts ...otelhttp.Option) http.RoundTripper {
	return &redactingTransport{next: otelhttp.NewTransport(&restoringTransport{next: base}, opts...)}
}

type redactingTransport struct {
	next http.RoundTripper
}

func (t *redactingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	redacted, ok := req.Context().Value(redactedURLContextKey).(string)
	if !ok {
		return t.next.RoundTrip(req)
	}

	redactedURL, err := url.Parse(redacted)
	if err != nil {
		return nil, err
	}

	req = req.Clone(context.WithValue(req.Context(), unredactedURLContextKey, req.URL))
	req.URL = redactedURL

	return t.next.RoundTrip(req)
}

type restoringTransport struct {
	next http.RoundTripper
}

func (t *restoringTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	unredacted, ok := req.Context().Value(unredactedURLContextKey).(*url.URL)
	if !ok {
		return t.next.RoundTrip(req)
	}

	req = req.Clone(req.Context())
	req.URL = unredacted

	return t.next.RoundTrip(req)
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

// readResponseBody buffers the response up to maxResponseBytes and fails rather
// than growing without bound when the server sends more.
func readResponseBody(res *http.Response) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}

	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxResponseBytes)
	}

	return body, nil
}

func (c *Client) requestURL(requestPath string) *url.URL {
	req := new(url.URL)
	*req = c.URL
	req.Path = path.Join(c.URL.Path, requestPath)

	return req
}

// itemURL addresses a single item under an endpoint, where the item is data
// that reached the client from outside it — a token name derived from an
// ApiToken resource, or a token secret returned by the server.
//
// The item is escaped and kept out of path.Join. Both matter, and for
// different reasons: without escaping, an item containing "/" adds path
// segments and re-points the request at another endpoint, and path.Join
// resolves a ".." away silently rather than treating it as the traversal it
// is. An item that is nothing but dots is refused outright, because there is
// no name it could be — only a relative path element.
func (c *Client) itemURL(requestPath, item string) (*url.URL, error) {
	if item == "" || strings.Trim(item, ".") == "" {
		return nil, fmt.Errorf("invalid path segment %q", item)
	}

	req := c.requestURL(requestPath)
	req.RawPath = req.EscapedPath() + "/" + url.PathEscape(item)
	req.Path = req.Path + "/" + item

	return req, nil
}

func (c *Client) HTTPGet(ctx context.Context, requestPath string, v any) (*http.Response, error) {
	return c.httpGet(ctx, c.requestURL(requestPath), v)
}

// HTTPGetItem fetches a single item under an endpoint. The item is treated as
// data, not as part of the request path; see itemURL.
func (c *Client) HTTPGetItem(ctx context.Context, requestPath, item string, v any) (*http.Response, error) {
	itemURL, err := c.itemURL(requestPath, item)
	if err != nil {
		return nil, err
	}

	return c.httpGet(ctx, itemURL, v)
}

func (c *Client) httpGet(ctx context.Context, url *url.URL, v any) (*http.Response, error) {
	requestURL := url.String()
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
	defer res.Body.Close()

	body, err := readResponseBody(res)
	if err != nil {
		return res, err
	}

	if res.StatusCode != http.StatusOK {
		return res, parseAPIError(res.StatusCode, body)
	}

	err = json.Unmarshal(body, v)
	if err != nil {
		return res, err
	}

	return res, nil
}

func (c *Client) HTTPDelete(ctx context.Context, requestPath string, item string) error {
	itemURL, err := c.itemURL(requestPath, item)
	if err != nil {
		return err
	}
	requestURL := itemURL.String()
	requestMethod := "DELETE"

	// The Unleash admin API identifies the token to delete by its secret, and
	// only accepts it as a path segment, so it cannot move to a header the way
	// the admin credential does. Hand the transport chain a redacted URL to
	// hold up to instrumentation instead.
	redactedURL, err := c.itemURL(requestPath, redactedPathSegment)
	if err != nil {
		return err
	}
	ctx = withRedactedURL(ctx, redactedURL.String())

	req, err := http.NewRequestWithContext(ctx, requestMethod, requestURL, nil)
	if err != nil {
		return err
	}

	req.Header.Add("Authorization", c.ApiToken)

	res, err := c.HttpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP DELETE request failed: %w", withoutURLError(err))
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return &UnleashAPIError{StatusCode: res.StatusCode}
	}
	return nil
}

func withoutURLError(err error) error {
	for {
		var urlErr *url.Error
		if !errors.As(err, &urlErr) {
			return err
		}
		if urlErr.Err == nil {
			return errors.New("HTTP request failed")
		}
		err = urlErr.Err
	}
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

	body, err := readResponseBody(res)
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
