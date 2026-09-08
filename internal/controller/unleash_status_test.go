package controller

import (
	"context"
	"fmt"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestUpdateStatusSuccessRefusesNewGenerationAfterConflict(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	const reconciledGeneration = int64(1)
	unleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: reconciledGeneration,
		},
	}

	updateCalls := 0
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(unleash.DeepCopy()).
		WithStatusSubresource(unleash).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, c client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				updateCalls++
				if updateCalls == 1 {
					current := &unleashv1.Unleash{}
					if err := c.Get(ctx, client.ObjectKeyFromObject(obj), current); err != nil {
						return err
					}
					current.Generation++
					if err := c.Update(ctx, current); err != nil {
						return err
					}
					return apierrors.NewConflict(
						schema.GroupResource{Group: "unleash.nais.io", Resource: "unleashes"},
						obj.GetName(),
						fmt.Errorf("the object has been modified"),
					)
				}
				return c.Status().Update(ctx, obj, opts...)
			},
		}).
		Build()

	reconciler := &UnleashReconciler{Client: fakeClient}
	err := reconciler.updateStatusReconcileSuccess(ctx, unleash, reconciledGeneration)

	require.ErrorIs(t, err, errUnleashGenerationChanged)
	assert.Equal(t, 1, updateCalls)

	stored := &unleashv1.Unleash{}
	require.NoError(t, fakeClient.Get(ctx, client.ObjectKeyFromObject(unleash), stored))
	assert.Equal(t, reconciledGeneration+1, stored.Generation)
	assert.Empty(t, stored.Status.Conditions)
	assert.False(t, stored.Status.Reconciled)
}

func TestUpdateStatusSuccessUsesReconciledGeneration(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	const reconciledGeneration = int64(3)
	unleash := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "success",
			Namespace:  "default",
			Generation: reconciledGeneration,
		},
	}
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(unleash.DeepCopy()).
		WithStatusSubresource(unleash).
		Build()

	reconciler := &UnleashReconciler{Client: fakeClient}
	require.NoError(t, reconciler.updateStatusReconcileSuccess(ctx, unleash, reconciledGeneration))

	stored := &unleashv1.Unleash{}
	require.NoError(t, fakeClient.Get(ctx, client.ObjectKeyFromObject(unleash), stored))
	require.Len(t, stored.Status.Conditions, 1)
	assert.Equal(t, reconciledGeneration, stored.Status.Conditions[0].ObservedGeneration)
	assert.Equal(t, metav1.ConditionTrue, stored.Status.Conditions[0].Status)
	assert.True(t, stored.Status.Reconciled)
}

func TestUpdateStatusNonSuccessKeepsReconciledGeneration(t *testing.T) {
	tests := []struct {
		name   string
		status metav1.ConditionStatus
	}{
		{name: "progressing", status: metav1.ConditionUnknown},
		{name: "failed", status: metav1.ConditionFalse},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			require.NoError(t, unleashv1.AddToScheme(scheme))

			const reconciledGeneration = int64(1)
			unleash := &unleashv1.Unleash{
				ObjectMeta: metav1.ObjectMeta{
					Name:       tt.name,
					Namespace:  "default",
					Generation: reconciledGeneration + 1,
				},
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(unleash.DeepCopy()).
				WithStatusSubresource(unleash).
				Build()

			reconciler := &UnleashReconciler{Client: fakeClient}
			err := reconciler.updateStatus(ctx, unleash, reconciledGeneration, nil, metav1.Condition{
				Type:    unleashv1.UnleashStatusConditionTypeReconciled,
				Status:  tt.status,
				Reason:  "Test",
				Message: tt.name,
			})
			require.NoError(t, err)

			stored := &unleashv1.Unleash{}
			require.NoError(t, fakeClient.Get(ctx, client.ObjectKeyFromObject(unleash), stored))
			require.Len(t, stored.Status.Conditions, 1)
			assert.Equal(t, reconciledGeneration, stored.Status.Conditions[0].ObservedGeneration)
			assert.Equal(t, tt.status, stored.Status.Conditions[0].Status)
			assert.False(t, stored.IsReady())
		})
	}
}
