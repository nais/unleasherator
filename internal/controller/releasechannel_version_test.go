package controller

import (
	"context"
	"testing"

	unleashv1 "github.com/nais/unleasherator/api/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
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
