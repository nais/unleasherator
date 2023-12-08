package unleashclient

import (
	"encoding/json"
	"testing"
)

func TestInstanceAdminStatsResult(t *testing.T) {
	jsonString := `{"timestamp":"2023-04-11T10:19:45.131Z","instanceId":"9bb58ff2-f295-4dd1-80a9-13e0c22de2fd","versionOSS":"4.19.3","versionEnterprise":"","users":3,"featureToggles":2,"projects":1,"contextFields":4,"roles":5,"groups":0,"environments":3,"segments":0,"strategies":8,"SAMLenabled":false,"OIDCenabled":false,"sum":"b9d48b54fad852075c0d5407acc35e79238f00b41d877196b9e37767588b8553"}`
	var result InstanceAdminStatsResult
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}
}

func TestApiToken(t *testing.T) {
	jsonString := `{"tokenName":"my-token","type":"client","environment":"development","projects":["default"],"secret":"default:development.0000000000000000000000000000000000000000000000000000000000000000","username":"my-token-2","alias":null,"project":"default","createdAt":"2023-12-08T11:36:52.035Z"}`
	var result ApiToken
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}
}

func TestApiTokenResult(t *testing.T) {
	jsonString := `{"tokens":[{"secret":"default:development.0000000000000000000000000000000000000000000000000000000000000000","tokenName":"token-a","type":"client","project":"default","projects":["default"],"environment":"development","expiresAt":null,"createdAt":"2023-12-08T09:23:19.962Z","alias":null,"seenAt":null,"username":"token-a"},{"secret":"*:production.0000000000000000000000000000000000000000000000000000000000000000","tokenName":"token-b","type":"client","project":"*","projects":["*"],"environment":"production","expiresAt":null,"createdAt":"2023-12-07T08:58:25.547Z","alias":null,"seenAt":null,"username":"token-b"},{"secret":"*:*.0000000000000000000000000000000000000000000000000000000000000000","tokenName":"admin","type":"admin","project":"*","projects":["*"],"environment":"*","expiresAt":null,"createdAt":"2023-05-31T06:54:22.698Z","alias":null,"seenAt":"2023-12-08T12:35:14.627Z","username":"admin"}]}`
	var result ApiTokenResult
	err := json.Unmarshal([]byte(jsonString), &result)
	if err != nil {
		t.Errorf("Error unmarshalling json: %s", err)
	}
}
