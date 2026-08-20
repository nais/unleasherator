package controller

import (
	"context"
	"fmt"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newUnresolvedFleet builds instances that report no resolved image, the state
// both a genuine first deploy and a cleared instance status look like.
func newUnresolvedFleet(count int) []unleashv1.Unleash {
	instances := make([]unleashv1.Unleash, 0, count)
	for i := 0; i < count; i++ {
		instances = append(instances, unleashv1.Unleash{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("instance-%d", i), Namespace: "default"},
			Spec: unleashv1.UnleashSpec{
				ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{Name: "test-rc"},
			},
		})
	}
	return instances
}

// runIdleThenRolling drives the channel one step through Idle and, if it started
// a rollout, one step through Rolling, returning the persisted channel.
func runIdleThenRolling(t *testing.T, releaseChannel *unleashv1.ReleaseChannel, instances []unleashv1.Unleash) *unleashv1.ReleaseChannel {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	objects := []client.Object{releaseChannel}
	for i := range instances {
		objects = append(objects, &instances[i])
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(releaseChannel).
		Build()
	reconciler := &ReleaseChannelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(20),
	}

	log := ctrl.Log.WithName("test")
	_, err := reconciler.executeIdlePhase(context.Background(), releaseChannel, log)
	require.NoError(t, err)

	if releaseChannel.Status.Phase == unleashv1.ReleaseChannelPhaseRolling {
		_, err = reconciler.executeRollingPhase(context.Background(), releaseChannel, log)
		require.NoError(t, err)
	}

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))
	return updated
}

func TestInitialDeploymentIsBatched(t *testing.T) {
	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(deployedImage),
			Strategy: unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{Phase: unleashv1.ReleaseChannelPhaseIdle},
	}

	updated := runIdleThenRolling(t, releaseChannel, newUnresolvedFleet(3))

	assert.Equal(t, unleashv1.ReleaseChannelPhaseRolling, updated.Status.Phase,
		"a first deploy is a rollout and belongs in the Rolling state machine")
	assert.Len(t, updated.Status.InstanceImages, 1,
		"maxParallel must cap a first deploy the same way it caps any other rollout")
	require.NotNil(t, updated.Status.ActiveBatch,
		"a first deploy must be tracked as a batch so health gating applies to it")
	assert.Len(t, updated.Status.ActiveBatch.InstanceNames, 1)
}

func TestClearedInstanceStatusDoesNotBypassBatching(t *testing.T) {
	// A channel that has already rolled out, whose instances then lost their
	// status — a status migration, a field rename, a mass re-create. The old
	// initial-deployment shortcut triggered on exactly this shape, and its
	// comment claimed a set PreviousImage would prevent that while the condition
	// never looked at it.
	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(newerImage),
			Strategy: unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase:         unleashv1.ReleaseChannelPhaseIdle,
			PreviousImage: olderImage,
			InstanceImages: map[string]string{
				"instance-0": deployedImage,
				"instance-1": deployedImage,
				"instance-2": deployedImage,
			},
		},
	}

	updated := runIdleThenRolling(t, releaseChannel, newUnresolvedFleet(3))

	advanced := 0
	for _, image := range updated.Status.InstanceImages {
		if image == newerImage {
			advanced++
		}
	}
	assert.Equal(t, 1, advanced,
		"clearing instance status must not push the target image to the whole fleet at once")
}

func TestUpToDateInstancesAreAdoptedIntoInstanceImages(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	// A channel that finished its rollout, plus an instance that reached the
	// target image without ever being batched — it joined after the rollout and
	// resolved through the fallback. No rollout will ever pick it up, because
	// rollouts only assign instances that still need updating.
	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(deployedImage),
			Strategy: unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase:          unleashv1.ReleaseChannelPhaseIdle,
			InstanceImages: map[string]string{"tracked": deployedImage},
		},
	}

	instances := []unleashv1.Unleash{
		instanceRunning("tracked", deployedImage),
		instanceRunning("joined-later", deployedImage),
	}

	objects := []client.Object{releaseChannel}
	for i := range instances {
		objects = append(objects, &instances[i])
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(releaseChannel).
		Build()
	reconciler := &ReleaseChannelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(20),
	}

	_, err := reconciler.executeIdlePhase(context.Background(), releaseChannel, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))

	assert.Equal(t, deployedImage, updated.Status.InstanceImages["joined-later"],
		"joining a channel must get an instance into InstanceImages")
	assert.Equal(t, unleashv1.ReleaseChannelPhaseIdle, updated.Status.Phase,
		"adoption records what is already true and must not start a rollout")
}
