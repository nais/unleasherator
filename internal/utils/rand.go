package utils

import (
	"crypto/rand"
	"encoding/base64"
)

func RandomString(length int) (string, error) {
	// Create an array of bytes with the given length
	b := make([]byte, length)
	// Fill the array with random numbers
	_, err := rand.Read(b)

	// Convert the array to a base64 encoded string
	return base64.StdEncoding.EncodeToString(b), err
}
