package resources

import "fmt"

type ValidationError struct {
	// err is the error that caused the validation to fail
	err error

	// resource is the name of the resource that failed validation
	resource string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("validation failed for %s (%s)", e.resource, e.err.Error())
}
