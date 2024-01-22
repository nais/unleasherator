package utils

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomString(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{
			name:   "Test with length 16",
			length: 16,
		},
		{
			name:   "Test with length 32",
			length: 32,
		},
		// Add more test cases as needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			randomString, err := RandomString(tt.length)
			assert.NoError(t, err)

			base64String, err := base64.StdEncoding.DecodeString(randomString)
			assert.NoError(t, err)

			assert.Equal(t, tt.length, len(base64String))
		})
	}
}
