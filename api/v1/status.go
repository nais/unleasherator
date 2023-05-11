package unleash_nais_io_v1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

func getConditionStatus(conditions []metav1.Condition, t string) metav1.ConditionStatus {
	for _, c := range conditions {
		if c.Type == t {
			return c.Status
		}
	}

	return metav1.ConditionUnknown
}

func conditionStatusIsReady(conditions []metav1.Condition) bool {
	statusAvailable := getConditionStatus(conditions, UnleashStatusConditionTypeAvailable)
	statusConnection := getConditionStatus(conditions, UnleashStatusConditionTypeConnection)

	return statusAvailable == metav1.ConditionTrue && statusConnection == metav1.ConditionTrue
}
