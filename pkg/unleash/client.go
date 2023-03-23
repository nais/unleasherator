package unleash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
)

type Client struct {
	URL      url.URL
	ApiToken string
}

func NewClient(instanceUrl string, apiToken string) (*Client, error) {
	u, err := url.Parse(instanceUrl)
	if err != nil {
		return nil, err
	}

	if apiToken == "" {
		return nil, fmt.Errorf("apiToken can not be empty")
	}

	return &Client{
		URL:      *u,
		ApiToken: apiToken,
	}, nil
}

func (c *Client) requestURL(requestPath string) *url.URL {
	req := new(url.URL)
	*req = c.URL
	req.Path = path.Join(c.URL.Path, requestPath)

	return req
}

func (c *Client) HTTPGet(requestPath string, v any) (*http.Response, error) {
	requestURL := c.requestURL(requestPath).String()
	requestMethod := "GET"

	client := &http.Client{}
	req, err := http.NewRequest(requestMethod, requestURL, nil)

	if err != nil {
		return nil, err
	}

	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", c.ApiToken)

	res, err := client.Do(req)
	if err != nil {
		return res, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return res, err
	}

	if res.StatusCode != 200 {
		return res, fmt.Errorf("unexpected http status code %d", res.StatusCode)
	}

	err = json.Unmarshal(body, v)
	if err != nil {
		return res, err
	}

	return res, nil
}

func (c *Client) HTTPPost(requestPath string, p, v any) (*http.Response, error) {
	requestURL := c.requestURL(requestPath).String()
	requestMethod := "POST"
	requestBody, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	client := &http.Client{}
	req, err := http.NewRequest(requestMethod, requestURL, bytes.NewBuffer(requestBody))

	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Authorization", c.ApiToken)

	res, err := client.Do(req)
	if err != nil {
		return res, err
	}
	defer res.Body.Close()

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return res, err
	}

	if res.StatusCode != 201 {
		return res, fmt.Errorf("unexpected http status code %d with body %s", res.StatusCode, string(body))
	}

	err = json.Unmarshal(body, v)
	if err != nil {
		return res, err
	}

	return res, nil
}
