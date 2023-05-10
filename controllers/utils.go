package controllers

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func unsetConditionLastTransitionTime(conditions []metav1.Condition) {
	for i := range conditions {
		conditions[i].LastTransitionTime = metav1.Time{}
	}
}
