package utils

import (
	"encoding/base64"
	"math/rand"
)

func RandomString(length int) string {
	// Create an array of bytes with the given length
	b := make([]byte, length)
	// Fill the array with random numbers
	rand.Read(b)
	// Convert the array to a base64 encoded string
	return base64.StdEncoding.EncodeToString(b)
}
