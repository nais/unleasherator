package unleashclient

import (
	"encoding/json"
	"testing"

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
	jsonString := `{"tokenName":"my-token","type":"client","environment":"development","projects":["default"],"secret":"default:development.0000000000000000000000000000000000000000000000000000000000000000","username":"my-token","alias":null,"project":"default","createdAt":"2023-12-08T11:36:52.035Z"}`
	var result ApiToken
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}

	assert.Equal(t, "my-token", result.TokenName)
	assert.Equal(t, "client", result.Type)
	assert.Equal(t, "development", result.Environment)
	assert.Equal(t, "default", result.Projects[0])
	assert.Equal(t, "default:development.0000000000000000000000000000000000000000000000000000000000000000", result.Secret)
	assert.Equal(t, "my-token", result.Username)
	assert.Equal(t, "", result.Alias)
	assert.Equal(t, "default", result.Project)
	assert.Equal(t, "2023-12-08T11:36:52.035Z", result.CreatedAt)
}

func TestApiTokenResult(t *testing.T) {
	jsonString := `{"tokens":[{"secret":"default:development.0000000000000000000000000000000000000000000000000000000000000000","tokenName":"token-a","type":"client","project":"default","projects":["default"],"environment":"development","expiresAt":null,"createdAt":"2023-12-08T09:23:19.962Z","alias":null,"seenAt":null,"username":"token-a"},{"secret":"*:production.0000000000000000000000000000000000000000000000000000000000000000","tokenName":"token-b","type":"client","project":"*","projects":["*"],"environment":"production","expiresAt":null,"createdAt":"2023-12-07T08:58:25.547Z","alias":null,"seenAt":null,"username":"token-b"},{"secret":"*:*.0000000000000000000000000000000000000000000000000000000000000000","tokenName":"admin","type":"admin","project":"*","projects":["*"],"environment":"*","expiresAt":null,"createdAt":"2023-05-31T06:54:22.698Z","alias":null,"seenAt":"2023-12-08T12:35:14.627Z","username":"admin"}]}`
	var result ApiTokenResult
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}

	assert.Equal(t, 3, len(result.Tokens))
}
