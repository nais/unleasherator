package resources

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationError(t *testing.T) {
	err := ValidationError{
		err:      errors.New("some field is required"),
		resource: "Deployment",
	}

	assert.Equal(t, "validation failed for Deployment (some field is required)", err.Error())
}
