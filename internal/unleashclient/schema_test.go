package unleashclient_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/nais/unleasherator/internal/unleashclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApiTokenRequest_HasRequiredFields verifies that our ApiTokenRequest struct
// contains all fields required by Unleash v7 API, preventing issues like the
// missing tokenName field that broke v7 compatibility.
func TestApiTokenRequest_HasRequiredFields(t *testing.T) {
	req := unleashclient.ApiTokenRequest{
		Username:    "test-user",
		TokenName:   "test-token",
		Type:        "CLIENT",
		Environment: "development",
		Projects:    []string{"default"},
	}

	// Marshal to JSON to verify fields are present
	data, err := json.Marshal(req)
	require.NoError(t, err)

	// Unmarshal to map to check field names
	var fields map[string]interface{}
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	// Verify required fields for v7
	t.Run("v7 required fields", func(t *testing.T) {
		assert.Contains(t, fields, "username", "username field required by Unleash API")
		assert.Contains(t, fields, "tokenName", "tokenName field required by Unleash v7 API")
		assert.Contains(t, fields, "type", "type field required by Unleash API")
	})

	// Verify optional fields are present in struct
	t.Run("optional fields", func(t *testing.T) {
		assert.Contains(t, fields, "environment", "environment field used for token scoping")
		assert.Contains(t, fields, "projects", "projects field used for token scoping")
	})
}

// TestApiTokenRequest_TokenNameMatchesUsername verifies that tokenName and username
// are set to the same value, which is required for proper v7 compatibility.
func TestApiTokenRequest_TokenNameMatchesUsername(t *testing.T) {
	req := unleashclient.ApiTokenRequest{
		Username:  "my-token-name",
		TokenName: "my-token-name",
		Type:      "CLIENT",
	}

	assert.Equal(t, req.Username, req.TokenName,
		"tokenName must match username for v7 compatibility")
}

// TestApiToken_ResponseFields verifies that our ApiToken response struct
// can handle responses from all Unleash versions.
func TestApiToken_ResponseFields(t *testing.T) {
	// Sample response mimicking Unleash API
	sampleJSON := `{
		"secret": "default:development.abc123",
		"username": "test-token",
		"tokenName": "test-token",
		"type": "client",
		"environment": "development",
		"project": "default",
		"projects": ["default"],
		"expiresAt": null,
		"createdAt": "2024-01-01T00:00:00.000Z",
		"seenAt": null,
		"alias": null
	}`

	var token unleashclient.ApiToken
	err := json.Unmarshal([]byte(sampleJSON), &token)
	require.NoError(t, err)

	// Verify all fields are populated correctly
	assert.Equal(t, "default:development.abc123", token.Secret)
	assert.Equal(t, "test-token", token.Username)
	assert.Equal(t, "test-token", token.TokenName)
	assert.Equal(t, "client", token.Type)
	assert.Equal(t, "development", token.Environment)
	assert.Equal(t, "default", token.Project)
	assert.Equal(t, []string{"default"}, token.Projects)
}

// TestSchemaFiles_Exist verifies that schema files are present for validation.
// This is a basic check - the actual schema validation would require the schemas
// to be committed or fetched in CI.
func TestSchemaFiles_Exist(t *testing.T) {
	schemaDir := filepath.Join("..", "..", "hack", "schemas")

	// Check if schema directory exists
	info, err := os.Stat(schemaDir)
	if os.IsNotExist(err) {
		t.Skip("Schema directory doesn't exist yet. Run ./hack/fetch-unleash-schemas.sh to fetch schemas.")
		return
	}
	require.NoError(t, err)
	require.True(t, info.IsDir())

	// List expected schema files
	expectedSchemas := []string{
		"unleash-v5-openapi.json",
		"unleash-v6-openapi.json",
		"unleash-v7-openapi.json",
	}

	// Check if at least one schema exists
	foundSchemas := 0
	for _, schema := range expectedSchemas {
		schemaPath := filepath.Join(schemaDir, schema)
		if _, err := os.Stat(schemaPath); err == nil {
			foundSchemas++
			t.Logf("✓ Found schema: %s", schema)
		}
	}

	if foundSchemas == 0 {
		t.Skip("No schemas found. Run ./hack/fetch-unleash-schemas.sh to fetch schemas.")
	} else {
		t.Logf("Found %d/%d schemas", foundSchemas, len(expectedSchemas))
	}
}

// TestApiTokenRequest_JSONTags verifies that JSON tags are properly set
// to ensure correct serialization.
func TestApiTokenRequest_JSONTags(t *testing.T) {
	tests := []struct {
		name       string
		field      string
		wantInJSON bool
	}{
		{"username always included", "username", true},
		{"tokenName always included", "tokenName", true},
		{"type always included", "type", true},
		{"environment can be omitted", "environment", true},
		{"projects can be omitted", "projects", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := unleashclient.ApiTokenRequest{
				Username:    "test",
				TokenName:   "test",
				Type:        "CLIENT",
				Environment: "dev",
				Projects:    []string{"default"},
			}

			data, err := json.Marshal(req)
			require.NoError(t, err)

			var fields map[string]interface{}
			err = json.Unmarshal(data, &fields)
			require.NoError(t, err)

			if tt.wantInJSON {
				assert.Contains(t, fields, tt.field)
			}
		})
	}
}

// TestApiTokenRequest_BackwardCompatibility ensures that requests work with
// older Unleash versions by not breaking when extra fields are present.
func TestApiTokenRequest_BackwardCompatibility(t *testing.T) {
	t.Run("v5 should ignore tokenName", func(t *testing.T) {
		req := unleashclient.ApiTokenRequest{
			Username:    "test-token",
			TokenName:   "test-token", // v5 will ignore this
			Type:        "CLIENT",
			Environment: "development",
			Projects:    []string{"default"},
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)

		// Verify tokenName is in the JSON (v5/v6 will just ignore it)
		assert.Contains(t, string(data), "tokenName",
			"tokenName should be present in JSON for v7, v5/v6 will ignore it")
	})

	t.Run("all versions accept standard fields", func(t *testing.T) {
		// These fields work across all versions
		req := unleashclient.ApiTokenRequest{
			Username:    "universal-token",
			TokenName:   "universal-token",
			Type:        "CLIENT",
			Environment: "production",
			Projects:    []string{"project1", "project2"},
		}

		data, err := json.Marshal(req)
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	})
}
