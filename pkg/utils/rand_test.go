package utils

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomString(t *testing.T) {
	length := 32
	randomString, err := RandomString(length)
	assert.NoError(t, err)

	base64String, err := base64.StdEncoding.DecodeString(randomString)
	assert.NoError(t, err)

	assert.Equal(t, length, len(base64String))
}
