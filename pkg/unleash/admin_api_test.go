package unleash

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
