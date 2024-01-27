package unleashclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Client struct {
	URL        url.URL
	ApiToken   string
	HttpClient *http.Client
}

func NewClient(instanceUrl string, apiToken string) (*Client, error) {
	client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
	return NewClientWithHttpClient(instanceUrl, apiToken, &client)
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
		return res, fmt.Errorf("unexpected http status code %d with body %s", res.StatusCode, string(body))
	}

	err = json.Unmarshal(body, v)
	if err != nil {
		return res, err
	}

	return res, nil
}
