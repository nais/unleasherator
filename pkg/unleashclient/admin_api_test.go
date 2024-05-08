package unleashclient

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
)

func TestInstanceAdminStatsResult(t *testing.T) {
	jsonString := `{"timestamp":"2023-12-09T19:41:44.853Z","instanceId":"01e6339c-23a9-4a51-b1a8-664a46536c2b","versionOSS":"5.6.6","versionEnterprise":"","users":3,"activeUsers":{"last7":1,"last30":1,"last60":1,"last90":3},"featureToggles":5,"projects":1,"contextFields":5,"roles":5,"customRootRoles":0,"customRootRolesInUse":0,"groups":0,"environments":3,"segments":0,"strategies":5,"SAMLenabled":false,"OIDCenabled":false,"clientApps":[{"range":"allTime","count":0},{"range":"30d","count":0},{"range":"7d","count":0}],"featureExports":0,"featureImports":0,"productionChanges":{"last30":1,"last60":1,"last90":1},"sum":"402a4b9c214464d0346e02ee9d5ac4d19071888020bbafc70c900aa13edf49bf"}`
	var result InstanceAdminStatsResult
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}

	assert.Equal(t, "2023-12-09T19:41:44.853Z", result.Timestamp)
	assert.Equal(t, "01e6339c-23a9-4a51-b1a8-664a46536c2b", result.InstanceID)
	assert.Equal(t, "5.6.6", result.VersionOSS)
	assert.Equal(t, "", result.VersionEnterprise)
	assert.Equal(t, float64(3), result.Users)
	assert.Equal(t, float64(1), result.ActiveUsers.Last7)
	assert.Equal(t, float64(1), result.ActiveUsers.Last30)
	assert.Equal(t, float64(1), result.ActiveUsers.Last60)
	assert.Equal(t, float64(3), result.ActiveUsers.Last90)
	assert.Equal(t, float64(5), result.FeatureToggles)
	assert.Equal(t, float64(1), result.Projects)
	assert.Equal(t, float64(5), result.ContextFields)
	assert.Equal(t, float64(5), result.Roles)
	assert.Equal(t, float64(0), result.CustomRootRoles)
	assert.Equal(t, float64(0), result.CustomRootRolesInUse)
	assert.Equal(t, float64(0), result.Groups)
	assert.Equal(t, float64(3), result.Environments)
	assert.Equal(t, float64(0), result.Segments)
	assert.Equal(t, float64(5), result.Strategies)
	assert.Equal(t, false, result.SAMLenabled)
	assert.Equal(t, false, result.OIDCenabled)
	assert.Equal(t, 3, len(result.ClientApps))
	assert.Equal(t, "allTime", result.ClientApps[0].Range)
	assert.Equal(t, float64(0), result.ClientApps[0].Count)
	assert.Equal(t, "30d", result.ClientApps[1].Range)
	assert.Equal(t, float64(0), result.ClientApps[1].Count)
	assert.Equal(t, "7d", result.ClientApps[2].Range)
	assert.Equal(t, float64(0), result.ClientApps[2].Count)
	assert.Equal(t, float64(0), result.FeatureExports)
	assert.Equal(t, float64(0), result.FeatureImports)
	assert.Equal(t, float64(1), result.ProductionChanges.Last30)
	assert.Equal(t, float64(1), result.ProductionChanges.Last60)
	assert.Equal(t, float64(1), result.ProductionChanges.Last90)
	assert.Equal(t, "402a4b9c214464d0346e02ee9d5ac4d19071888020bbafc70c900aa13edf49bf", result.Sum)
}

func TestApiToken(t *testing.T) {
	jsonString := `{"tokenName":"my-token","type":"client","environment":"development","projects":["default"],"secret":"default:development.00000","username":"my-token","alias":null,"project":"default","createdAt":"2023-12-08T11:36:52.035Z"}`
	var result ApiToken
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}

	assert.Equal(t, "my-token", result.TokenName)
	assert.Equal(t, "client", result.Type)
	assert.Equal(t, "development", result.Environment)
	assert.Equal(t, "default", result.Projects[0])
	assert.Equal(t, "default:development.00000", result.Secret)
	assert.Equal(t, "my-token", result.Username)
	assert.Equal(t, "", result.Alias)
	assert.Equal(t, "default", result.Project)
	assert.Equal(t, "2023-12-08T11:36:52.035Z", result.CreatedAt)

	jsonString = `{"tokenName":"my-token-star","type":"client","environment":"development","projects":["*"],"secret":"*:development.00000","username":"my-token-star","alias":null,"project":"*","createdAt":"2023-12-11T09:17:59.680Z"}`
	err = json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}

	assert.Equal(t, "*", result.Projects[0])
	assert.Equal(t, "*", result.Project)
}

func TestApiTokenResult(t *testing.T) {
	jsonString := `{"tokens":[{"secret":"default:development.00000","tokenName":"token-a","type":"client","project":"default","projects":["default"],"environment":"development","expiresAt":null,"createdAt":"2023-12-08T09:23:19.962Z","alias":null,"seenAt":null,"username":"token-a"},{"secret":"*:production.00000","tokenName":"token-b","type":"client","project":"*","projects":["*"],"environment":"production","expiresAt":null,"createdAt":"2023-12-07T08:58:25.547Z","alias":null,"seenAt":null,"username":"token-b"},{"secret":"*:*.00000","tokenName":"admin","type":"admin","project":"*","projects":["*"],"environment":"*","expiresAt":null,"createdAt":"2023-05-31T06:54:22.698Z","alias":null,"seenAt":"2023-12-08T12:35:14.627Z","username":"admin"}]}`
	var result ApiTokenResult
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}

	assert.Equal(t, 3, len(result.Tokens))
}
func TestClient_GetAPIToken(t *testing.T) {
	httpmock.Activate()
	defer httpmock.DeactivateAndReset()
	httpmock.RegisterResponder("GET", "=~^/api/admin/api-tokens/.+\\z", func(req *http.Request) (*http.Response, error) {
		tokenData, err := os.ReadFile("testdata/unleash-admin-api-get-tokens.json")
		assert.NoError(t, err)

		tokenResult := ApiTokenResult{}
		err = json.Unmarshal(tokenData, &tokenResult)
		assert.NoError(t, err)

		// Get token name from URL path
		paths := strings.Split(req.URL.Path, "/")
		name := paths[len(paths)-1]

		tokens := make([]ApiToken, 0)
		for _, token := range tokenResult.Tokens {
			if token.TokenName == name {
				tokens = append(tokens, token)
			}
		}

		jsonData, err := json.Marshal(ApiTokenResult{Tokens: tokens})
		assert.NoError(t, err)

		return httpmock.NewBytesResponse(200, jsonData), nil
	})

	client, err := NewClient("http://unleash.example.com", "token")
	assert.NoError(t, err)

	t.Run("Token for default project exists", func(t *testing.T) {
		expectedToken := ApiToken{
			Secret:      "default:development.00000",
			TokenName:   "token-default",
			Type:        "client",
			Project:     "default",
			Projects:    []string{"default"},
			Environment: "development",
			CreatedAt:   "2023-12-08T09:23:19.962Z",
			Username:    "token-default",
		}

		tokens, err := client.GetAPITokensByName(context.Background(), "token-default")
		assert.NoError(t, err)
		assert.Equal(t, 1, len(tokens.Tokens))
		assert.Equal(t, expectedToken, tokens.Tokens[0])
	})

	t.Run("Token for wildcard (*) project exists", func(t *testing.T) {
		expectedToken := ApiToken{
			Secret:      "*:production.00000",
			TokenName:   "token-wildcard",
			Type:        "client",
			Project:     "*",
			Projects:    []string{"*"},
			Environment: "production",
			CreatedAt:   "2023-12-07T08:58:25.547Z",
			Username:    "token-wildcard",
		}

		tokens, err := client.GetAPITokensByName(context.Background(), "token-wildcard")
		assert.NoError(t, err)
		assert.Equal(t, 1, len(tokens.Tokens))
		assert.Equal(t, expectedToken, tokens.Tokens[0])
	})

	t.Run("Token with multiple projects exists", func(t *testing.T) {
		expectedToken := ApiToken{
			Secret:      "project-a:development.00000",
			TokenName:   "token-multi-projects",
			Type:        "client",
			Project:     "project-a",
			Projects:    []string{"project-a", "project-b"},
			Environment: "development",
			CreatedAt:   "2023-12-07T08:58:25.547Z",
			Username:    "token-multi-projects",
		}

		tokens, err := client.GetAPITokensByName(context.Background(), "token-multi-projects")
		assert.NoError(t, err)
		assert.Equal(t, 1, len(tokens.Tokens))
		assert.Equal(t, expectedToken, tokens.Tokens[0])
	})

	t.Run("Tokens with same name exists", func(t *testing.T) {
		expectedTokens := ApiToken{
			Secret:      "project-a:development.00000",
			TokenName:   "token-multi-projects",
			Type:        "client",
			Project:     "project-a",
			Projects:    []string{"project-a", "project-b"},
			Environment: "development",
			CreatedAt:   "2023-12-07T08:58:25.547Z",
			Username:    "token-multi-projects",
		}

		tokens, err := client.GetAPITokensByName(context.Background(), "token-multi-projects")
		assert.NoError(t, err)
		assert.Equal(t, 1, len(tokens.Tokens))
		assert.Equal(t, expectedToken, tokens.Tokens[0])
	})

	t.Run("Token does not exist", func(t *testing.T) {
		// @TODO check actual behavior when token is not found
		tokens, err := client.GetAPITokensByName(context.Background(), "non-existent-user")
		assert.NoError(t, err)
		assert.Equal(t, 0, len(tokens.Tokens))
	})
}

func TestClient_CreateAPIToken(t *testing.T) {
	client, err := NewClientWithHttpClient("http://unleash.example.com", "token", http.DefaultClient)
	assert.NoError(t, err)

	t.Run("Should create token for default project", func(t *testing.T) {
		httpmock.Activate()
		defer httpmock.DeactivateAndReset()
		httpmock.RegisterResponder("POST", "/api/admin/api-tokens", func(req *http.Request) (*http.Response, error) {
			jsonData, err := os.ReadFile("testdata/unleash-admin-api-create-token-default.json")
			if err != nil {
				return httpmock.NewStringResponse(500, ""), err
			}

			return httpmock.NewBytesResponse(201, jsonData), nil
		})

		tokenRequest := ApiTokenRequest{
			Username:    "my-token-default",
			Type:        "client",
			Environment: "development",
			Projects:    []string{"default"},
		}

		expectedToken := &ApiToken{
			Secret:      "default:development.00000",
			TokenName:   "my-token-default",
			Type:        "client",
			Project:     "default",
			Projects:    []string{"default"},
			Environment: "development",
			CreatedAt:   "2023-12-11T10:21:28.088Z",
			Username:    "my-token-default",
		}

		token, err := client.CreateAPIToken(context.Background(), tokenRequest)
		assert.NoError(t, err)
		assert.Equal(t, expectedToken, token)
	})
}
