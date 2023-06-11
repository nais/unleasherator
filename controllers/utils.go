package controllers

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func promGaugeValueForStatus(status metav1.ConditionStatus) float64 {
	if status == metav1.ConditionTrue {
		return 1
	}
	return 0
}
