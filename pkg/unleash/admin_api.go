package unleash

import (
	"net/http"
)

type InstanceAdminStatsResult struct {
	InstanceID        string  `json:"instanceId"`
	Timestamp         string  `json:"timestamp"`
	VersionOSS        string  `json:"versionOSS"`
	VersionEnterprise string  `json:"versionEnterprise"`
	Users             float64 `json:"users"`
	FeatureToggles    float64 `json:"featureToggles"`
	Projects          float64 `json:"projects"`
	ContextFields     float64 `json:"contextFields"`
	Roles             float64 `json:"roles"`
	Groups            float64 `json:"groups"`
	Environments      float64 `json:"environments"`
	Segments          float64 `json:"segments"`
	Strategies        float64 `json:"strategies"`
	SAMLenabled       bool    `json:"SAMLenabled"`
	OIDCenabled       bool    `json:"OIDCenabled"`
	Sum               string  `json:"sum"`
}

type HealthResult struct {
	Health string `json:"health"`
}

// GetHealth returns the health of the Unleash instance.
func (c *Client) GetHealth() (*HealthResult, *http.Response, error) {
	health := &HealthResult{}
	res, err := c.HTTPGet("/health", health)

	if err != nil {
		return health, res, err
	}
	defer res.Body.Close()

	return health, res, nil
}

// GetInstanceAdminStats returns instance admin stats (admin only endpoint - requires admin token).
func (c *Client) GetInstanceAdminStats() (*InstanceAdminStatsResult, *http.Response, error) {
	adminStats := &InstanceAdminStatsResult{}

	res, err := c.HTTPGet("/api/admin/instance-admin/statistics", adminStats)
	if err != nil {
		return adminStats, res, err
	}

	return adminStats, res, nil
}

type ApiToken struct {
	Secret      string   `json:"secret"`
	Username    string   `json:"username"`
	Type        string   `json:"type"` // Possible values: [client, admin, frontend]
	Environment string   `json:"environment"`
	Project     string   `json:"project"`
	Projects    []string `json:"projects"`
	ExpiresAt   string   `json:"expiresAt"`
	CreatedAt   string   `json:"createdAt"`
	SeenAt      string   `json:"seenAt"`
	Alias       string   `json:"alias"`
}

type ApiTokenResult struct {
	Tokens []ApiToken `json:"tokens"`
}

type ApiTokenRequest struct {
	Secret      string   `json:"secret,omitempty"`
	Username    string   `json:"username"`
	Type        string   `json:"type"` // One of client, admin, frontend
	Environment string   `json:"environment,omitempty"`
	Project     string   `json:"project,omitempty"`
	Projects    []string `json:"projects,omitempty"`
	ExpiresAt   string   `json:"expiresAt,omitempty"`
}

// CreateAPIToken creates a new API token (admin only endpoint - requires admin token).
// https://docs.getunleash.io/reference/api/unleash/create-api-token
func (c *Client) CreateAPIToken(req ApiTokenRequest) (*ApiToken, error) {
	res := &ApiToken{}

	_, err := c.HTTPPost("/api/admin/api-tokens", req, res)
	if err != nil {
		return res, err
	}

	return res, nil
}

// GetAllAPITokens returns all API tokens (admin only endpoint - requires admin token).
// https://docs.getunleash.io/reference/api/unleash/get-all-api-tokens
func (c *Client) GetAllAPITokens() (*ApiTokenResult, error) {
	res := &ApiTokenResult{}

	_, err := c.HTTPGet("/api/admin/api-tokens", &res)
	if err != nil {
		return res, err
	}

	return res, nil
}

// CheckAPITokenExists checks if an API token with the given username exists.
func (c *Client) CheckAPITokenExists(userName string) (bool, error) {
	tokens, err := c.GetAllAPITokens()
	if err != nil {
		return false, err
	}

	for _, t := range tokens.Tokens {
		if t.Username == userName {
			return true, nil
		}
	}

	return false, nil
}

func (c *Client) DeleteApiToken(tokenString string) error {
	err := c.HTTPDelete("/api/admin/api-tokens", tokenString)

	if err != nil {
		return err
	}
	return nil
}
