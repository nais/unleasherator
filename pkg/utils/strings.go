package utils

func StringSliceEquals(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for _, item := range a {
		if !StringInSlice(item, b) {
			return false
		}
	}

	return true
}

func StringInSlice(item string, slice []string) bool {
	for _, s := range slice {
		if item == s {
			return true
		}
	}

	return false
}
