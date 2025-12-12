package unleashclient

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApiTokenRequest_JSONSerialization verifies that TokenName is properly omitted
// for v5/v6 compatibility when empty, but included for v7+
func TestApiTokenRequest_JSONSerialization(t *testing.T) {
	t.Run("v7 includes TokenName in JSON", func(t *testing.T) {
		req := ApiTokenRequest{
			Username:    "my-token",
			TokenName:   "my-token", // Set for v7+
			Type:        "client",
			Environment: "development",
			Projects:    []string{"default"},
		}

		jsonBytes, err := json.Marshal(req)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(jsonBytes, &result)
		require.NoError(t, err)

		assert.Equal(t, "my-token", result["username"])
		assert.Equal(t, "my-token", result["tokenName"], "tokenName should be present for v7+")
		assert.Equal(t, "client", result["type"])
		assert.Equal(t, "development", result["environment"])
	})

	t.Run("v5/v6 omits TokenName from JSON", func(t *testing.T) {
		req := ApiTokenRequest{
			Username:    "my-token",
			TokenName:   "", // Empty for v5/v6
			Type:        "client",
			Environment: "development",
			Projects:    []string{"default"},
		}

		jsonBytes, err := json.Marshal(req)
		require.NoError(t, err)

		var result map[string]interface{}
		err = json.Unmarshal(jsonBytes, &result)
		require.NoError(t, err)

		assert.Equal(t, "my-token", result["username"])
		assert.NotContains(t, result, "tokenName", "tokenName should be omitted when empty (v5/v6 compatibility)")
		assert.Equal(t, "client", result["type"])
		assert.Equal(t, "development", result["environment"])
	})
}
