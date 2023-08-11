package controllers

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// promGaugeValueForStatus returns 1 if status is true, 0 otherwise.
// This is used to set the value of a Prometheus gauge (which can only be a float64
// between 0 and 1) based on the status of a Kubernetes condition (which can be
// true, false or unknown (nil).
func promGaugeValueForStatus(status metav1.ConditionStatus) float64 {
	if status == metav1.ConditionTrue {
		return 1
	}
	return 0
}

// hasValue returns true if s is in list, false otherwise.
func hasValue(s string, list []string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
