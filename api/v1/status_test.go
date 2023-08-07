package unleash_nais_io_v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetConditionStatus(t *testing.T) {
	conditions := []metav1.Condition{
		{
			Type:   "test",
			Status: metav1.ConditionTrue,
		},
		{
			Type:   "test2",
			Status: metav1.ConditionFalse,
		},
	}

	status := getConditionStatus(conditions, "test")
	assert.Equal(t, metav1.ConditionTrue, status)

	status = getConditionStatus(conditions, "test2")
	assert.Equal(t, metav1.ConditionFalse, status)

	status = getConditionStatus(conditions, "test3")
	assert.Equal(t, metav1.ConditionUnknown, status)
}

func TestConditionStatusIsReady(t *testing.T) {
	conditions := []metav1.Condition{
		{
			Type:   UnleashStatusConditionTypeReconciled,
			Status: metav1.ConditionTrue,
		},
		{
			Type:   UnleashStatusConditionTypeConnected,
			Status: metav1.ConditionTrue,
		},
	}

	ready := conditionStatusIsReady(conditions)
	assert.True(t, ready)

	conditions[0].Status = metav1.ConditionFalse
	ready = conditionStatusIsReady(conditions)
	assert.False(t, ready)

	conditions[0].Status = metav1.ConditionTrue
	conditions[1].Status = metav1.ConditionFalse
	ready = conditionStatusIsReady(conditions)
	assert.False(t, ready)
}
