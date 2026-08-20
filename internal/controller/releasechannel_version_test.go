package controller

import (
	"context"
	"fmt"
	"testing"
	"time"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	deployedImage = "europe-north1-docker.pkg.dev/nais-io/nais/images/unleash-v7:v7-7.5.1-e309531"
	olderImage    = "europe-north1-docker.pkg.dev/nais-io/nais/images/unleash-v7:v7-7.4.9-abc1234"
	newerImage    = "europe-north1-docker.pkg.dev/nais-io/nais/images/unleash-v7:v7-7.6.0-deadbee"
)

func TestImageTag(t *testing.T) {
	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{name: "plain tag", image: "unleash:5.6.0", expected: "5.6.0"},
		{name: "registry path", image: "quay.io/unleash/unleash-server:6.3.0", expected: "6.3.0"},
		{name: "embedded version tag", image: deployedImage, expected: "v7-7.5.1-e309531"},
		{name: "registry port is not a tag", image: "registry:5000/unleash", expected: ""},
		{name: "registry port with tag", image: "registry:5000/unleash:5.6.0", expected: "5.6.0"},
		{name: "digest pinned", image: "quay.io/unleash/unleash-server@sha256:abc123", expected: ""},
		{name: "untagged", image: "unleash", expected: ""},
		{name: "empty", image: "", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, imageTag(tt.image))
		})
	}
}

func TestImageVersion(t *testing.T) {
	tests := []struct {
		name       string
		image      string
		expectOK   bool
		expectCore string
	}{
		{name: "plain semver tag", image: "unleash:5.6.0", expectOK: true, expectCore: "5.6.0"},
		{name: "v prefix", image: "unleash:v5.6.0", expectOK: true, expectCore: "5.6.0"},
		{name: "embedded version with build hash", image: deployedImage, expectOK: true, expectCore: "7.5.1"},
		{name: "prerelease qualifier is dropped", image: "unleash:5.12.0-beta.1", expectOK: true, expectCore: "5.12.0"},
		{name: "mutable tag has no version", image: "unleash:latest", expectOK: false},
		{name: "major only tag has no version", image: "unleash:v7", expectOK: false},
		{name: "digest pin has no version", image: "unleash@sha256:abc123", expectOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, ok := imageVersion(tt.image)
			require.Equal(t, tt.expectOK, ok)
			if tt.expectOK {
				assert.Equal(t, tt.expectCore, version.String())
			}
		})
	}
}

func TestCompareImageVersions(t *testing.T) {
	tests := []struct {
		name       string
		current    string
		target     string
		expectOK   bool
		expectSign int
	}{
		{name: "upgrade", current: deployedImage, target: newerImage, expectOK: true, expectSign: 1},
		{name: "downgrade", current: deployedImage, target: olderImage, expectOK: true, expectSign: -1},
		{name: "same version", current: deployedImage, target: deployedImage, expectOK: true, expectSign: 0},
		{
			// A rebuild of the same version carries a different build hash. It
			// must not rank below its predecessor, or the downgrade guard would
			// block a rollout that changes nothing but the build.
			name:       "rebuild of same version",
			current:    deployedImage,
			target:     "europe-north1-docker.pkg.dev/nais-io/nais/images/unleash-v7:v7-7.5.1-0000000",
			expectOK:   true,
			expectSign: 0,
		},
		{name: "major downgrade", current: "unleash:6.3.0", target: "unleash:5.6.0", expectOK: true, expectSign: -1},
		{name: "unrankable target", current: "unleash:6.3.0", target: "unleash:latest", expectOK: false},
		{name: "unrankable current", current: "unleash:latest", target: "unleash:6.3.0", expectOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, ok := compareImageVersions(tt.current, tt.target)
			require.Equal(t, tt.expectOK, ok)
			if tt.expectOK {
				assert.Equal(t, tt.expectSign, order)
			}
		})
	}
}

// newDowngradeFixture builds a channel whose instances already run
// deployedImage, with spec.image pointing at targetImage.
func newDowngradeFixture(targetImage string, allowDowngrade bool) (*unleashv1.ReleaseChannel, *unleashv1.Unleash) {
	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:          unleashv1.UnleashImage(targetImage),
			AllowDowngrade: allowDowngrade,
			Strategy:       unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase:          unleashv1.ReleaseChannelPhaseIdle,
			InstanceImages: map[string]string{"instance-1": deployedImage},
		},
	}
	instance := &unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{Name: "instance-1", Namespace: "default"},
		Spec: unleashv1.UnleashSpec{
			ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{Name: releaseChannel.Name},
		},
		Status: unleashv1.UnleashStatus{
			ResolvedReleaseChannelImage: deployedImage,
			Conditions: []metav1.Condition{
				{Type: unleashv1.UnleashStatusConditionTypeReconciled, Status: metav1.ConditionTrue, Reason: "Reconciled", LastTransitionTime: metav1.Now()},
				{Type: unleashv1.UnleashStatusConditionTypeConnected, Status: metav1.ConditionTrue, Reason: "Connected", LastTransitionTime: metav1.Now()},
			},
		},
	}
	return releaseChannel, instance
}

func runIdlePhase(t *testing.T, targetImage string, allowDowngrade bool) *unleashv1.ReleaseChannel {
	t.Helper()

	releaseChannel, instance := newDowngradeFixture(targetImage, allowDowngrade)

	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(releaseChannel, instance).
		WithStatusSubresource(releaseChannel).
		Build()
	reconciler := &ReleaseChannelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(10),
	}

	_, err := reconciler.executeIdlePhase(context.Background(), releaseChannel, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))
	return updated
}

func TestExecuteIdlePhaseRefusesDowngrade(t *testing.T) {
	updated := runIdlePhase(t, olderImage, false)

	assert.Equal(t, unleashv1.ReleaseChannelPhaseIdle, updated.Status.Phase,
		"a refused downgrade must not start a rollout")
	assert.Equal(t, deployedImage, updated.Status.InstanceImages["instance-1"],
		"the instance must keep the image it already runs")

	condition := meta.FindStatusCondition(updated.Status.Conditions, unleashv1.ReleaseChannelStatusConditionTypeReconciled)
	require.NotNil(t, condition)
	assert.Equal(t, "DowngradeRefused", condition.Reason)
}

func TestExecuteIdlePhaseAllowsDowngradeWhenPermitted(t *testing.T) {
	updated := runIdlePhase(t, olderImage, true)

	assert.Equal(t, unleashv1.ReleaseChannelPhaseRolling, updated.Status.Phase,
		"spec.allowDowngrade must let the rollout proceed")
}

func TestExecuteIdlePhaseAllowsUpgrade(t *testing.T) {
	updated := runIdlePhase(t, newerImage, false)

	assert.Equal(t, unleashv1.ReleaseChannelPhaseRolling, updated.Status.Phase,
		"a newer target must still roll out")
}

func instanceRunning(name, image string) unleashv1.Unleash {
	return unleashv1.Unleash{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: unleashv1.UnleashSpec{
			ReleaseChannel: unleashv1.UnleashReleaseChannelConfig{Name: "test-rc"},
		},
		Status: unleashv1.UnleashStatus{ResolvedReleaseChannelImage: image},
	}
}

func TestRollbackBaseline(t *testing.T) {
	tests := []struct {
		name      string
		instances []unleashv1.Unleash
		expected  string
	}{
		{
			name:     "no instances",
			expected: "",
		},
		{
			name:      "nothing deployed yet",
			instances: []unleashv1.Unleash{instanceRunning("a", ""), instanceRunning("b", "")},
			expected:  "",
		},
		{
			name:      "fleet agrees",
			instances: []unleashv1.Unleash{instanceRunning("a", deployedImage), instanceRunning("b", deployedImage)},
			expected:  deployedImage,
		},
		{
			name:      "unresolved instances do not create disagreement",
			instances: []unleashv1.Unleash{instanceRunning("a", deployedImage), instanceRunning("b", "")},
			expected:  deployedImage,
		},
		{
			name:      "fleet disagrees",
			instances: []unleashv1.Unleash{instanceRunning("a", deployedImage), instanceRunning("b", olderImage)},
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, rollbackBaseline(tt.instances))
		})
	}
}

// runEnsurePreviousImageTracked applies the tracker to a channel targeting
// newerImage and returns the PreviousImage it persisted.
func runEnsurePreviousImageTracked(t *testing.T, instances []unleashv1.Unleash) string {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec:       unleashv1.ReleaseChannelSpec{Image: unleashv1.UnleashImage(newerImage)},
		Status:     unleashv1.ReleaseChannelStatus{Phase: unleashv1.ReleaseChannelPhaseIdle},
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
		Recorder: record.NewFakeRecorder(10),
	}

	_, err := reconciler.ensurePreviousImageTracked(context.Background(), releaseChannel, instances, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))
	return updated.Status.PreviousImage
}

func TestEnsurePreviousImageTrackedRefusesAmbiguousBaseline(t *testing.T) {
	// Same fleet, opposite List order. Selecting the first resolved image made
	// the rollback target depend on which instance the API server happened to
	// return first; neither answer is more correct than the other.
	forward := runEnsurePreviousImageTracked(t, []unleashv1.Unleash{
		instanceRunning("instance-a", deployedImage),
		instanceRunning("instance-b", olderImage),
	})
	reverse := runEnsurePreviousImageTracked(t, []unleashv1.Unleash{
		instanceRunning("instance-b", olderImage),
		instanceRunning("instance-a", deployedImage),
	})

	assert.Empty(t, forward, "a fleet that disagrees must not produce a rollback baseline")
	assert.Equal(t, forward, reverse, "the rollback baseline must not depend on List order")
}

func TestEnsurePreviousImageTrackedCapturesUnanimousBaseline(t *testing.T) {
	previous := runEnsurePreviousImageTracked(t, []unleashv1.Unleash{
		instanceRunning("instance-a", deployedImage),
		instanceRunning("instance-b", deployedImage),
	})

	assert.Equal(t, deployedImage, previous,
		"an agreed-upon deployed image is still captured for rollback")
}

func TestDowngradeFromComparesAgainstTheFleetImage(t *testing.T) {
	reconciler := &ReleaseChannelReconciler{}

	fleetOf := func(image string, count int) []unleashv1.Unleash {
		instances := make([]unleashv1.Unleash, 0, count)
		for i := 0; i < count; i++ {
			instances = append(instances, instanceRunning(fmt.Sprintf("%s-%d", image[len(image)-7:], i), image))
		}
		return instances
	}

	t.Run("one instance ahead does not veto the fleet", func(t *testing.T) {
		// Thirty on the old image, one straggler that ended up ahead — the shape
		// left behind by instances outside instanceImages holding what they last
		// resolved. Upgrading the thirty must not be refused on account of the one.
		instances := append(fleetOf(deployedImage, 30), instanceRunning("ahead", newerImage))

		releaseChannel := &unleashv1.ReleaseChannel{
			Spec: unleashv1.ReleaseChannelSpec{Image: unleashv1.UnleashImage(newerImage)},
		}
		_, _, refuse := reconciler.downgradeFrom(releaseChannel, instances)
		assert.False(t, refuse, "the fleet is moving forwards")

		// And the same fleet asked to move to something between the two: still an
		// upgrade for the thirty, so still not a downgrade.
		releaseChannel.Spec.Image = unleashv1.UnleashImage(
			"europe-north1-docker.pkg.dev/nais-io/nais/images/unleash-v7:v7-7.5.5-abcdef0")
		_, _, refuse = reconciler.downgradeFrom(releaseChannel, instances)
		assert.False(t, refuse, "one instance ahead must not hold the other thirty hostage")
	})

	t.Run("rolling the fleet backwards is still refused", func(t *testing.T) {
		instances := append(fleetOf(newerImage, 30), instanceRunning("behind", olderImage))

		releaseChannel := &unleashv1.ReleaseChannel{
			Spec: unleashv1.ReleaseChannelSpec{Image: unleashv1.UnleashImage(olderImage)},
		}
		from, count, refuse := reconciler.downgradeFrom(releaseChannel, instances)
		assert.True(t, refuse)
		assert.Equal(t, newerImage, from)
		assert.Equal(t, 30, count, "the message has to say how much of the fleet is on it")
	})

	t.Run("an even split resolves to the newer image", func(t *testing.T) {
		instances := append(fleetOf(newerImage, 3), fleetOf(deployedImage, 3)...)

		releaseChannel := &unleashv1.ReleaseChannel{
			Spec: unleashv1.ReleaseChannelSpec{Image: unleashv1.UnleashImage(deployedImage)},
		}
		from, _, refuse := reconciler.downgradeFrom(releaseChannel, instances)
		assert.True(t, refuse, "with no majority the cautious answer wins")
		assert.Equal(t, newerImage, from)
	})

	t.Run("allowDowngrade still disables the guard", func(t *testing.T) {
		releaseChannel := &unleashv1.ReleaseChannel{
			Spec: unleashv1.ReleaseChannelSpec{
				Image:          unleashv1.UnleashImage(olderImage),
				AllowDowngrade: true,
			},
		}
		_, _, refuse := reconciler.downgradeFrom(releaseChannel, fleetOf(newerImage, 3))
		assert.False(t, refuse)
	})
}

func TestExecuteRollingPhaseCapturesRollbackBaseline(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	// spec.image changed while the channel was already Rolling, so Idle never ran
	// in between. The fleet still agrees on the old image, which is exactly when
	// the baseline is capturable.
	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(newerImage),
			Strategy: unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase: unleashv1.ReleaseChannelPhaseRolling,
			InstanceImages: map[string]string{
				"instance-a": deployedImage,
				"instance-b": deployedImage,
			},
		},
	}

	instances := []unleashv1.Unleash{
		instanceRunning("instance-a", deployedImage),
		instanceRunning("instance-b", deployedImage),
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

	_, err := reconciler.executeRollingPhase(context.Background(), releaseChannel, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))

	assert.Equal(t, deployedImage, updated.Status.PreviousImage,
		"a rollout that starts in Rolling still needs a rollback baseline")
	require.NotNil(t, updated.Status.LastImageChangeTime)
}

func TestSettleReferenceTracksTheLastDeploy(t *testing.T) {
	rolloutStart := metav1.NewTime(time.Now().Add(-time.Hour))
	lastDeploy := metav1.NewTime(time.Now().Add(-time.Second))

	tests := []struct {
		name     string
		status   unleashv1.ReleaseChannelStatus
		expected *metav1.Time
	}{
		{
			// Measuring from the rollout start let every deploy after the first
			// clear the settle delay instantly, however recently its pods rolled.
			name:     "prefers the last deploy over the rollout start",
			status:   unleashv1.ReleaseChannelStatus{StartTime: &rolloutStart, LastDeployTime: &lastDeploy},
			expected: &lastDeploy,
		},
		{
			name:     "falls back to the rollout start for in-flight rollouts",
			status:   unleashv1.ReleaseChannelStatus{StartTime: &rolloutStart},
			expected: &rolloutStart,
		},
		{
			name:     "nothing to measure from",
			status:   unleashv1.ReleaseChannelStatus{},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, settleReference(&unleashv1.ReleaseChannel{Status: tt.status}))
		})
	}
}

func TestDeployToInstancesStampsLastDeployTime(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	// A rollout that started an hour ago and is now assigning a later batch. The
	// settle delay has to run from this assignment, not from the rollout start.
	rolloutStart := metav1.NewTime(time.Now().Add(-time.Hour))
	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(newerImage),
			Strategy: unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase:          unleashv1.ReleaseChannelPhaseRolling,
			StartTime:      &rolloutStart,
			PreviousImage:  deployedImage,
			InstanceImages: map[string]string{"instance-b": deployedImage},
		},
	}
	instance := instanceRunning("instance-b", deployedImage)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(releaseChannel, &instance).
		WithStatusSubresource(releaseChannel).
		Build()
	reconciler := &ReleaseChannelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(20),
	}

	_, err := reconciler.deployToInstances(context.Background(), releaseChannel,
		[]unleashv1.Unleash{instance}, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))

	require.NotNil(t, updated.Status.LastDeployTime,
		"assigning a batch its image must stamp when that happened")
	assert.WithinDuration(t, time.Now(), updated.Status.LastDeployTime.Time, time.Minute)
	assert.Equal(t, updated.Status.LastDeployTime, settleReference(updated),
		"the settle delay must run from this batch, not from the rollout start")
}

// runPhaseWithDowngrade drives one phase of a channel whose fleet is on
// deployedImage while spec.image has been edited to something older.
func runPhaseWithDowngrade(t *testing.T, phase unleashv1.ReleaseChannelPhase) *unleashv1.ReleaseChannel {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(olderImage),
			Strategy: unleashv1.ReleaseChannelStrategy{MaxParallel: 1},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase: phase,
			InstanceImages: map[string]string{
				"instance-a": deployedImage,
				"instance-b": deployedImage,
			},
		},
	}

	instances := []unleashv1.Unleash{
		instanceRunning("instance-a", deployedImage),
		instanceRunning("instance-b", deployedImage),
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

	log := ctrl.Log.WithName("test")
	var err error
	switch phase {
	case unleashv1.ReleaseChannelPhaseRolling:
		_, err = reconciler.executeRollingPhase(context.Background(), releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseCanary:
		_, err = reconciler.executeCanaryPhase(context.Background(), releaseChannel, log)
	case unleashv1.ReleaseChannelPhaseValidating:
		_, err = reconciler.executeValidatingPhase(context.Background(), releaseChannel, log)
	}
	require.NoError(t, err)

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))
	return updated
}

func TestDowngradeIsRefusedInEveryRolloutPhase(t *testing.T) {
	// A rollout occupies the channel for as long as it takes to batch through the
	// fleet, so editing spec.image while one is running is reachable — and it is
	// the natural reaction to a rollout that looks stuck.
	phases := []unleashv1.ReleaseChannelPhase{
		unleashv1.ReleaseChannelPhaseRolling,
		unleashv1.ReleaseChannelPhaseCanary,
		unleashv1.ReleaseChannelPhaseValidating,
	}

	for _, phase := range phases {
		t.Run("refused during "+string(phase), func(t *testing.T) {
			updated := runPhaseWithDowngrade(t, phase)

			condition := meta.FindStatusCondition(updated.Status.Conditions, unleashv1.ReleaseChannelStatusConditionTypeReconciled)
			require.NotNil(t, condition)
			assert.Equal(t, "DowngradeRefused", condition.Reason)

			for name, image := range updated.Status.InstanceImages {
				assert.Equal(t, deployedImage, image,
					"no instance may be assigned the older target: %s", name)
			}
			assert.Nil(t, updated.Status.ActiveBatch,
				"no batch may be left holding the refused target")
			assert.NotEqual(t, olderImage, updated.Status.PreviousImage,
				"the refused rollout must not leave a rollback baseline behind")
		})
	}
}
