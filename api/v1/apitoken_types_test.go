package unleash_nais_io_v1

import (
	"testing"

	"github.com/nais/unleasherator/internal/unleashclient"
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
	testCases := []struct {
		name          string
		apiToken      *ApiToken
		unleashToken  unleashclient.ApiToken
		expectedEqual bool
	}{
		{
			name: "Token types are case different",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "client",
				Environment: "development",
				Projects:    []string{"project1", "project2"},
			},
			expectedEqual: true,
		},
		{
			name: "Tokens are equal",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "client",
				Environment: "development",
				Projects:    []string{"project1", "project2"},
			},
			expectedEqual: true,
		},
		{
			name: "Tokens have different types",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "FRONTEND",
				Environment: "development",
				Projects:    []string{"project1", "project2"},
			},
			expectedEqual: false,
		},
		{
			name: "Tokens have different environments",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "production",
				Projects:    []string{"project1", "project2"},
			},
			expectedEqual: false,
		},
		{
			name: "Tokens have same projects but in different order",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "development",
				Projects:    []string{"project2", "project1"},
			},
			expectedEqual: true,
		},
		{
			name: "Tokens have different projects",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "development",
				Projects:    []string{"project1", "project3"},
			},
			expectedEqual: false,
		},
		{
			name: "Tokens are wildcards",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"*"},
				},
			},
			unleashToken: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "development",
				Projects:    []string{"*"},
			},
			expectedEqual: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualEqual := tc.apiToken.IsEqual(tc.unleashToken)
			assert.Equal(t, tc.expectedEqual, actualEqual)
		})
	}
}

func TestApiToken_ExistsInList(t *testing.T) {
	testCases := []struct {
		name      string
		apiToken  *ApiToken
		tokenList []unleashclient.ApiToken
		exists    bool
	}{
		{
			name: "Token exists in the list",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			tokenList: []unleashclient.ApiToken{
				{
					Type:        "client",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
				{
					Type:        "client",
					Environment: "production",
					Projects:    []string{"project3", "project4"},
				},
			},
			exists: true,
		},
		{
			name: "Token does not exist in the list",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "staging",
					Projects:    []string{"project1", "project2"},
				},
			},
			tokenList: []unleashclient.ApiToken{
				{
					Type:        "client",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
				{
					Type:        "client",
					Environment: "production",
					Projects:    []string{"project3", "project4"},
				},
			},
			exists: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			token, exists := tc.apiToken.ExistsInList(tc.tokenList)
			assert.Equal(t, tc.exists, exists)
			if exists {
				assert.NotNil(t, token)
			} else {
				assert.Nil(t, token)
			}
		})
	}
}
func TestApiToken_Diff(t *testing.T) {
	testCases := []struct {
		name     string
		apiToken *ApiToken
		token    unleashclient.ApiToken
		expected string
	}{
		{
			name: "Type is different",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			token: unleashclient.ApiToken{
				Type:        "FRONTEND",
				Environment: "development",
				Projects:    []string{"project1", "project2"},
			},
			expected: "Type: CLIENT -> FRONTEND",
		},
		{
			name: "Environment is different",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			token: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "production",
				Projects:    []string{"project1", "project2"},
			},
			expected: "Environment: development -> production",
		},
		{
			name: "Projects are different",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			token: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "development",
				Projects:    []string{"project3", "project4"},
			},
			expected: "Projects: [project1 project2] -> [project3 project4]",
		},
		{
			name: "Multiple differences",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			token: unleashclient.ApiToken{
				Type:        "FRONTEND",
				Environment: "production",
				Projects:    []string{"project3", "project4"},
			},
			expected: "Type: CLIENT -> FRONTEND, Environment: development -> production, Projects: [project1 project2] -> [project3 project4]",
		},
		{
			name: "No differences",
			apiToken: &ApiToken{
				Spec: ApiTokenSpec{
					Type:        "CLIENT",
					Environment: "development",
					Projects:    []string{"project1", "project2"},
				},
			},
			token: unleashclient.ApiToken{
				Type:        "CLIENT",
				Environment: "development",
				Projects:    []string{"project1", "project2"},
			},
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actual := tc.apiToken.Diff(tc.token)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
