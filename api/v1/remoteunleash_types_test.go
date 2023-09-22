package unleash_nais_io_v1

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Call the AdminToken method and assert that it returns the expected token
type mockClient struct {
	mock.Mock
}

// GroupVersionKindFor implements client.Client.
func (*mockClient) GroupVersionKindFor(obj runtime.Object) (schema.GroupVersionKind, error) {
	panic("unimplemented")
}

// IsObjectNamespaced implements client.Client.
func (*mockClient) IsObjectNamespaced(obj runtime.Object) (bool, error) {
	panic("unimplemented")
}

func (m *mockClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	args := m.Called(ctx, key, obj, opts)
	return args.Error(0)
}

func (m *mockClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return nil
}

func (m *mockClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	return nil
}

func (m *mockClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return nil
}

func (m *mockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	return nil
}

func (m *mockClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	return nil
}

func (m *mockClient) DeleteAllOf(ctx context.Context, obj client.Object, opts ...client.DeleteAllOfOption) error {
	return nil
}

func (m *mockClient) RESTMapper() meta.RESTMapper {
	return nil
}

func (m *mockClient) Scheme() *runtime.Scheme {
	return nil
}

func (m *mockClient) Status() client.StatusWriter {
	return nil
}

func (m *mockClient) SubResource(subResource string) client.SubResourceClient {
	return nil
}

func TestRemoteUnleash_URL(t *testing.T) {
	unleash := &RemoteUnleash{
		Spec: RemoteUnleashSpec{
			Server: RemoteUnleashServer{
				URL: "https://my-unleash-server.com",
			},
		},
	}

	// Call the URL method and assert that it returns the expected URL
	assert.Equal(t, "https://my-unleash-server.com", unleash.URL())
}

func TestRemoteUnleash_NamespacedName(t *testing.T) {
	unleash := &RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-unleash",
			Namespace: "my-namespace",
		},
	}

	// Call the NamespacedName method and assert that it returns the expected namespaced name
	assert.Equal(t, types.NamespacedName{Name: "my-unleash", Namespace: "my-namespace"}, unleash.NamespacedName())
}

func TestRemoteUnleash_AdminSecretNamespacedName(t *testing.T) {
	unleash := &RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-unleash",
			Namespace: "my-namespace",
		},
		Spec: RemoteUnleashSpec{
			AdminSecret: RemoteUnleashSecret{
				Name: "my-secret",
			},
		},
	}

	// Call the AdminSecretNamespacedName method and assert that it returns the expected namespaced name
	assert.Equal(t, types.NamespacedName{Name: "my-secret", Namespace: "my-namespace"}, unleash.AdminSecretNamespacedName())
}

func TestRemoteUnleash_AdminToken(t *testing.T) {
	ctx := context.Background()
	c := new(mockClient)

	// Setup mock client to return a secret with a token
	secret := &v1.Secret{
		Data: map[string][]byte{
			"token": []byte("my-secret-token"),
		},
	}
	c.On("Get", ctx, types.NamespacedName{Name: "my-secret", Namespace: "my-namespace"}, &v1.Secret{}, []client.GetOption(nil)).Return(nil).Run(func(args mock.Arguments) {
		secretPtr := args.Get(2).(*v1.Secret)
		*secretPtr = *secret
	})

	// Create a RemoteUnleash instance with the secret name and key
	unleash := &RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-unleash",
			Namespace: "my-namespace",
		},
		Spec: RemoteUnleashSpec{
			Server: RemoteUnleashServer{
				URL: "https://my-unleash-server.com",
			},
			AdminSecret: RemoteUnleashSecret{
				Name: "my-secret",
				Key:  "token",
			},
		},
	}

	// Call the AdminToken method and assert that it returns the expected token
	token, err := unleash.AdminToken(ctx, c, "")
	assert.NoError(t, err)
	assert.Equal(t, []byte("my-secret-token"), token)

	// Assert that the mock client was called with the correct arguments
	// Keep in mind that the secret is a pointer that gets updated which is why we can not assert the original secret
	c.AssertCalled(t, "Get", ctx, types.NamespacedName{Name: "my-secret", Namespace: "my-namespace"}, secret, []client.GetOption(nil))
}

func TestRemoteUnleash_ApiClient(t *testing.T) {
	ctx := context.Background()
	c := &mockClient{}

	// Setup mock client to return a secret with a token
	secret := &v1.Secret{
		Data: map[string][]byte{
			"token": []byte("my-secret-token"),
		},
	}
	c.On("Get", ctx, types.NamespacedName{Name: "my-secret", Namespace: "my-namespace"}, &v1.Secret{}, []client.GetOption(nil)).Return(nil).Run(func(args mock.Arguments) {
		secretPtr := args.Get(2).(*v1.Secret)
		*secretPtr = *secret
	})

	// Create a RemoteUnleash instance with the secret name and key
	unleash := &RemoteUnleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-unleash",
			Namespace: "my-namespace",
		},
		Spec: RemoteUnleashSpec{
			Server: RemoteUnleashServer{
				URL: "https://my-unleash-server.com",
			},
			AdminSecret: RemoteUnleashSecret{
				Name: "my-secret",
				Key:  "token",
			},
		},
	}

	unleashClient, err := unleash.ApiClient(ctx, c, "")
	assert.NoError(t, err)
	assert.NotNil(t, unleashClient)
	assert.Equal(t, "https://my-unleash-server.com", unleashClient.URL.String())
	assert.Equal(t, "my-secret-token", unleashClient.ApiToken)

	// Assert that the mock client was called with the correct arguments
	// Keep in mind that the secret is a pointer that gets updated which is why we can not assert the original secret
	c.AssertCalled(t, "Get", ctx, types.NamespacedName{Name: "my-secret", Namespace: "my-namespace"}, secret, []client.GetOption(nil))
}

func TestRemoteUnleash_IsReady(t *testing.T) {
	unleash := &RemoteUnleash{
		Status: RemoteUnleashStatus{
			Conditions: []metav1.Condition{
				{
					Type:   UnleashStatusConditionTypeReconciled,
					Status: metav1.ConditionTrue,
				},
				{
					Type:   UnleashStatusConditionTypeConnected,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	// Call the IsReady method and assert that it returns true
	assert.True(t, unleash.IsReady())
}
