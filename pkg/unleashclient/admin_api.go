package unleashclient

import (
	"context"
	"fmt"
	"net/http"
)

type InstanceAdminStatsResult struct {
	InstanceID        string  `json:"instanceId"`
	Timestamp         string  `json:"timestamp"`
	VersionOSS        string  `json:"versionOSS"`
	VersionEnterprise string  `json:"versionEnterprise"`
	Users             float64 `json:"users"`
	ActiveUsers       struct {
		Last7  float64 `json:"last7"`
		Last30 float64 `json:"last30"`
		Last60 float64 `json:"last60"`
		Last90 float64 `json:"last90"`
	} `json:"activeUsers"`
	FeatureToggles       float64 `json:"featureToggles"`
	Projects             float64 `json:"projects"`
	ContextFields        float64 `json:"contextFields"`
	Roles                float64 `json:"roles"`
	CustomRootRoles      float64 `json:"customRootRoles"`
	CustomRootRolesInUse float64 `json:"customRootRolesInUse"`
	Groups               float64 `json:"groups"`
	Environments         float64 `json:"environments"`
	Segments             float64 `json:"segments"`
	Strategies           float64 `json:"strategies"`
	SAMLenabled          bool    `json:"SAMLenabled"`
	OIDCenabled          bool    `json:"OIDCenabled"`
	ClientApps           []struct {
		Range string  `json:"range"`
		Count float64 `json:"count"`
	} `json:"clientApps"`
	FeatureExports    float64 `json:"featureExports"`
	FeatureImports    float64 `json:"featureImports"`
	ProductionChanges struct {
		Last30 float64 `json:"last30"`
		Last60 float64 `json:"last60"`
		Last90 float64 `json:"last90"`
	} `json:"productionChanges"`
	Sum string `json:"sum"`
}

type HealthResult struct {
	Health string `json:"health"`
}

// GetHealth returns the health of the Unleash instance.
func (c *Client) GetHealth(ctx context.Context) (*HealthResult, *http.Response, error) {
	health := &HealthResult{}
	res, err := c.HTTPGet(ctx, HealthEndpoint, health)

	if err != nil {
		return health, res, err
	}
	defer res.Body.Close()

	return health, res, nil
}

// GetInstanceAdminStats returns instance admin stats (admin only endpoint - requires admin token).
func (c *Client) GetInstanceAdminStats(ctx context.Context) (*InstanceAdminStatsResult, *http.Response, error) {
	adminStats := &InstanceAdminStatsResult{}

	res, err := c.HTTPGet(ctx, InstanceAdminStatsEndpoint, adminStats)
	if err != nil {
		return adminStats, res, err
	}

	return adminStats, res, nil
}

type ApiToken struct {
	Secret      string   `json:"secret"`
	Username    string   `json:"username"`
	TokenName   string   `json:"tokenName"`
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
func (c *Client) CreateAPIToken(ctx context.Context, req ApiTokenRequest) (*ApiToken, error) {
	res := &ApiToken{}

	_, err := c.HTTPPost(ctx, ApiTokensEndpoint, req, res)
	if err != nil {
		return res, err
	}

	return res, nil
}

// GetAllAPITokens returns all API tokens.
// https://docs.getunleash.io/reference/api/unleash/get-all-api-tokens
func (c *Client) GetAllAPITokens(ctx context.Context) (*ApiTokenResult, error) {
	res := &ApiTokenResult{}

	_, err := c.HTTPGet(ctx, ApiTokensEndpoint, res)
	if err != nil {
		return res, err
	}

	return res, nil
}

// GetAPITokenByName returns API tokens with the given username, there can be multiple tokens since the username is not unique.
// https://docs.getunleash.io/reference/api/unleash/get-api-tokens-by-name
func (c *Client) GetAPITokensByName(ctx context.Context, userName string) (*ApiTokenResult, error) {
	res := &ApiTokenResult{}

	_, err := c.HTTPGet(ctx, fmt.Sprintf("%s/%s", ApiTokensEndpoint, userName), res)
	if err != nil {
		return res, err
	}

	return res, nil
}

// CheckAPITokenExists checks if an API token with the given username exists.
func (c *Client) CheckAPITokenExists(ctx context.Context, userName string) (bool, error) {
	token, err := c.GetAPITokensByName(ctx, userName)
	exists := len(token.Tokens) > 0

	return exists, err
}

// DeleteApiToken deletes an API token with the given token string.
// https://docs.getunleash.io/reference/api/unleash/delete-api-token
func (c *Client) DeleteApiToken(ctx context.Context, tokenString string) error {
	err := c.HTTPDelete(ctx, ApiTokensEndpoint, tokenString)

	if err != nil {
		return err
	}
	return nil
}
