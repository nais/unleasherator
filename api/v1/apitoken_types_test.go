package unleash_nais_io_v1

import (
	"testing"

	"github.com/nais/unleasherator/pkg/unleashclient"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestApiToken_ApiTokenRequest(t *testing.T) {
	apiToken := &ApiToken{
		Spec: ApiTokenSpec{
			Type:        "client",
			Environment: "development",
			Projects:    []string{"project1", "project2"},
		},
	}

	suffix := "suffix"
	expectedRequest := unleashclient.ApiTokenRequest{
		Username:    apiToken.ApiTokenName(suffix),
		Type:        apiToken.Spec.Type,
		Environment: apiToken.Spec.Environment,
		Projects:    apiToken.Spec.Projects,
	}

	actualRequest := apiToken.ApiTokenRequest(suffix)

	assert.Equal(t, expectedRequest, actualRequest)
}

func TestApiToken_ApiTokenName(t *testing.T) {
	apiToken := &ApiToken{
		ObjectMeta: v1.ObjectMeta{
			Name: "name",
		},
	}

	suffix := "suffix"
	expectedName := apiToken.Name + "-" + suffix

	actualName := apiToken.ApiTokenName(suffix)

	assert.Equal(t, expectedName, actualName)
}
func TestApiToken_NamespacedName(t *testing.T) {
	apiToken := &ApiToken{
		ObjectMeta: v1.ObjectMeta{
			Namespace: "test-namespace",
			Name:      "test-name",
		},
	}

	expectedNamespacedName := types.NamespacedName{
		Namespace: "test-namespace",
		Name:      "test-name",
	}

	actualNamespacedName := apiToken.NamespacedName()

	assert.Equal(t, expectedNamespacedName, actualNamespacedName)
}
func TestApiToken_IsEqual(t *testing.T) {
	apiToken := &ApiToken{
		Spec: ApiTokenSpec{
			Type:        "client",
			Environment: "development",
			Projects:    []string{"project1", "project2"},
		},
	}

	unleashToken := &unleashclient.ApiToken{
		Type:        "client",
		Environment: "development",
		Projects:    []string{"project1", "project2"},
	}

	// Test when the tokens are equal
	assert.True(t, apiToken.ApiTokenIsEqual(unleashToken))

	// Test when the tokens have different types
	unleashToken.Type = "server"
	assert.False(t, apiToken.ApiTokenIsEqual(unleashToken))

	// Test when the tokens have different environments
	unleashToken.Type = "client"
	unleashToken.Environment = "production"
	assert.False(t, apiToken.ApiTokenIsEqual(unleashToken))

	// Test when the tokens have different projects
	unleashToken.Environment = "development"
	unleashToken.Projects = []string{"project1", "project3"}
	assert.False(t, apiToken.ApiTokenIsEqual(unleashToken))
}
