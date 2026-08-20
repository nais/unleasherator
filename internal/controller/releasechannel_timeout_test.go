package controller

import (
	"context"
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
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// newBudgetChannel pins every input to batchAllowance so the expected budget does
// not depend on package-level timing vars, which the envtest suite rewrites.
// settle 1m + verify 4m + interval 1m gives a 6m allowance per batch.
func newBudgetChannel(instances, maxParallel int, maxUpgradeTime *metav1.Duration) *unleashv1.ReleaseChannel {
	return &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image: unleashv1.UnleashImage(deployedImage),
			Strategy: unleashv1.ReleaseChannelStrategy{
				MaxParallel:    maxParallel,
				BatchInterval:  &metav1.Duration{Duration: time.Minute},
				MaxUpgradeTime: maxUpgradeTime,
			},
			HealthChecks: unleashv1.HealthCheckConfig{
				Enabled:      true,
				InitialDelay: &metav1.Duration{Duration: time.Minute},
				Timeout:      &metav1.Duration{Duration: 4 * time.Minute},
			},
		},
		Status: unleashv1.ReleaseChannelStatus{Instances: instances},
	}
}

func TestUpgradeTimeBudget(t *testing.T) {
	tests := []struct {
		name           string
		instances      int
		maxParallel    int
		maxUpgradeTime *metav1.Duration
		expected       time.Duration
		expectDerived  bool
	}{
		{
			// An operator who pinned a value gets exactly it, however large the
			// fleet — the derivation must never quietly override a hard ceiling.
			name:           "explicit value is used exactly",
			instances:      65,
			maxParallel:    1,
			maxUpgradeTime: &metav1.Duration{Duration: 3 * time.Minute},
			expected:       3 * time.Minute,
			expectDerived:  false,
		},
		{
			name:          "scales with the number of batches",
			instances:     5,
			maxParallel:   1,
			expected:      30 * time.Minute,
			expectDerived: true,
		},
		{
			// The same fleet finishes in fewer batches when more run at once.
			name:          "maxParallel divides the work",
			instances:     65,
			maxParallel:   10,
			expected:      42 * time.Minute,
			expectDerived: true,
		},
		{
			// A single batch derives 6m, which would be tighter than the flat
			// default this replaces. Small fleets must not lose budget.
			name:          "floored at the previous flat default",
			instances:     1,
			maxParallel:   1,
			expected:      releaseChannelDefaultMaxUpgradeTime,
			expectDerived: true,
		},
		{
			// 65 serial batches derive 6h30m. A wedged rollout still has to be
			// caught, so the derivation cannot grow without bound.
			name:          "capped for a large serial fleet",
			instances:     65,
			maxParallel:   1,
			expected:      releaseChannelMaxDerivedUpgradeTime,
			expectDerived: true,
		},
		{
			name:          "no instances counted yet falls back to the floor",
			instances:     0,
			maxParallel:   1,
			expected:      releaseChannelDefaultMaxUpgradeTime,
			expectDerived: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget, derivation := upgradeTimeBudget(newBudgetChannel(tt.instances, tt.maxParallel, tt.maxUpgradeTime))
			assert.Equal(t, tt.expected, budget)
			if tt.expectDerived {
				assert.NotEmpty(t, derivation, "a derived budget must say where it came from")
			} else {
				assert.Empty(t, derivation, "a configured budget has no derivation")
			}
		})
	}
}

func TestCheckMaxUpgradeTimeExceededReportsDerivation(t *testing.T) {
	r := &ReleaseChannelReconciler{}

	// 5 batches of 6m is a 30m budget, so a rollout 20 minutes in is still fine
	// where the old flat 10m default would already have failed it.
	releaseChannel := newBudgetChannel(5, 1, nil)
	releaseChannel.Status.StartTime = &metav1.Time{Time: time.Now().Add(-20 * time.Minute)}

	exceeded, reason := r.checkMaxUpgradeTimeExceeded(releaseChannel)
	assert.False(t, exceeded, "a five batch rollout must get more than the flat default")
	assert.Empty(t, reason)

	releaseChannel.Status.StartTime = &metav1.Time{Time: time.Now().Add(-31 * time.Minute)}
	exceeded, reason = r.checkMaxUpgradeTimeExceeded(releaseChannel)
	require.True(t, exceeded)
	// An operator seeing a limit they never set needs the arithmetic and the
	// name of the field that overrides it.
	assert.Contains(t, reason, "maxUpgradeTime")
	assert.Contains(t, reason, "5 batch(es)")
	assert.Contains(t, reason, "spec.strategy.maxUpgradeTime")
}

func TestReleasePhaseOnFailureRequiresRollbackTarget(t *testing.T) {
	rollbackOn := unleashv1.RollbackConfig{Enabled: true, OnFailure: true}

	t.Run("no baseline goes straight to Failed", func(t *testing.T) {
		phase := releasePhaseOnFailure(&unleashv1.ReleaseChannel{
			Spec: unleashv1.ReleaseChannelSpec{Rollback: rollbackOn},
		})
		assert.Equal(t, unleashv1.ReleaseChannelPhaseFailed, phase,
			"RollingBack with nothing to roll back to only loses the real failure reason")
	})

	// spec.rollback.previousImage is deliberately not covered here. Asserting the
	// helper honoured it while the only caller read status.previousImage instead
	// is what hid that the documented remedy did nothing; it is verified against
	// the caller in TestExecuteFailedPhaseRollsBackOnExplicitPreviousImage.

	t.Run("a tracked baseline is a target", func(t *testing.T) {
		phase := releasePhaseOnFailure(&unleashv1.ReleaseChannel{
			Spec:   unleashv1.ReleaseChannelSpec{Rollback: rollbackOn},
			Status: unleashv1.ReleaseChannelStatus{PreviousImage: deployedImage},
		})
		assert.Equal(t, unleashv1.ReleaseChannelPhaseRollingBack, phase)
	})
}

func TestExecuteFailedPhaseTerminatesWithoutRollbackBaseline(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	const timeoutReason = "Rollout exceeded maxUpgradeTime (31m0s elapsed, limit 30m)"

	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(deployedImage),
			Rollback: unleashv1.RollbackConfig{Enabled: true, OnFailure: true},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase:         unleashv1.ReleaseChannelPhaseFailed,
			FailureReason: timeoutReason,
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(releaseChannel).
		WithStatusSubresource(releaseChannel).
		Build()
	recorder := record.NewFakeRecorder(10)
	reconciler := &ReleaseChannelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: recorder,
	}

	result, err := reconciler.executeFailedPhase(context.Background(), releaseChannel, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter,
		"nothing the controller can do changes this, so it must stop rather than reprint forever")

	updated := &unleashv1.ReleaseChannel{}
	require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), updated))

	condition := meta.FindStatusCondition(updated.Status.Conditions, unleashv1.ReleaseChannelStatusConditionTypeReconciled)
	require.NotNil(t, condition)
	assert.Equal(t, "RollbackUnavailable", condition.Reason)
	assert.Contains(t, condition.Message, timeoutReason,
		"the reason the rollout actually failed must survive")
	assert.Contains(t, condition.Message, "spec.rollback.previousImage")

	// A second pass must not re-emit the warning.
	drained := len(recorder.Events)
	require.Positive(t, drained, "the first pass tells the operator once")

	result, err = reconciler.executeFailedPhase(context.Background(), updated, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
	assert.Len(t, recorder.Events, drained, "the warning is not repeated on every pass")
}

// failedChannelFixture is a channel that failed with nothing to roll back to,
// which is where both documented remedies have to work.
func failedChannelFixture(t *testing.T) (*ReleaseChannelReconciler, *unleashv1.ReleaseChannel, func() *unleashv1.ReleaseChannel) {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, unleashv1.AddToScheme(scheme))

	releaseChannel := &unleashv1.ReleaseChannel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-rc", Namespace: "default"},
		Spec: unleashv1.ReleaseChannelSpec{
			Image:    unleashv1.UnleashImage(newerImage),
			Rollback: unleashv1.RollbackConfig{Enabled: true, OnFailure: true},
		},
		Status: unleashv1.ReleaseChannelStatus{
			Phase:         unleashv1.ReleaseChannelPhaseFailed,
			FailureReason: "Rollout exceeded maxUpgradeTime (31m0s elapsed, limit 30m)",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(releaseChannel).
		WithStatusSubresource(releaseChannel).
		Build()
	reconciler := &ReleaseChannelReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Recorder: record.NewFakeRecorder(20),
	}

	reload := func() *unleashv1.ReleaseChannel {
		fresh := &unleashv1.ReleaseChannel{}
		require.NoError(t, fakeClient.Get(context.Background(), releaseChannel.NamespacedName(), fresh))
		return fresh
	}
	return reconciler, releaseChannel, reload
}

// settleIntoTerminalFailure runs the failed phase until it stops changing, which
// is the state an operator actually finds the channel in.
func settleIntoTerminalFailure(t *testing.T, r *ReleaseChannelReconciler, rc *unleashv1.ReleaseChannel, reload func() *unleashv1.ReleaseChannel) *unleashv1.ReleaseChannel {
	t.Helper()

	current := rc
	for i := 0; i < 4; i++ {
		_, err := r.executeFailedPhase(context.Background(), current, ctrl.Log.WithName("test"))
		require.NoError(t, err)
		current = reload()
	}
	require.Equal(t, unleashv1.ReleaseChannelPhaseFailed, current.Status.Phase)
	return current
}

func TestExecuteFailedPhaseRollsBackOnExplicitPreviousImage(t *testing.T) {
	reconciler, releaseChannel, reload := failedChannelFixture(t)
	current := settleIntoTerminalFailure(t, reconciler, releaseChannel, reload)

	// The condition tells the operator to do exactly this.
	current.Spec.Rollback.PreviousImage = deployedImage
	require.NoError(t, reconciler.Update(context.Background(), current))

	_, err := reconciler.executeFailedPhase(context.Background(), current, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	assert.Equal(t, unleashv1.ReleaseChannelPhaseRollingBack, reload().Status.Phase,
		"setting spec.rollback.previousImage must actually start a rollback")
}

func TestExecuteFailedPhaseRetriesOnCorrectedImage(t *testing.T) {
	reconciler, releaseChannel, reload := failedChannelFixture(t)
	current := settleIntoTerminalFailure(t, reconciler, releaseChannel, reload)
	require.Equal(t, newerImage, current.Status.FailedImage)

	// The other remedy the condition offers: point spec.image somewhere else.
	current.Spec.Image = unleashv1.UnleashImage(deployedImage)
	require.NoError(t, reconciler.Update(context.Background(), current))

	_, err := reconciler.executeFailedPhase(context.Background(), current, ctrl.Log.WithName("test"))
	require.NoError(t, err)

	updated := reload()
	assert.Equal(t, unleashv1.ReleaseChannelPhaseIdle, updated.Status.Phase,
		"correcting spec.image must let the channel try again")
	assert.Empty(t, updated.Status.FailureReason)
	assert.Empty(t, updated.Status.FailedImage)
}

func TestExecuteFailedPhaseStaysPutWhenNothingChanged(t *testing.T) {
	reconciler, releaseChannel, reload := failedChannelFixture(t)
	current := settleIntoTerminalFailure(t, reconciler, releaseChannel, reload)

	result, err := reconciler.executeFailedPhase(context.Background(), current, ctrl.Log.WithName("test"))
	require.NoError(t, err)
	assert.Zero(t, result.RequeueAfter)
	assert.Equal(t, unleashv1.ReleaseChannelPhaseFailed, reload().Status.Phase,
		"an untouched failure must not churn")
}
