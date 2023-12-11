package utils

import (
	"errors"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestRemoveEmptyErrs(t *testing.T) {
	tests := []struct {
		name   string
		slice  []error
		result []error
	}{
		{
			name:   "No empty errors",
			slice:  []error{errors.New("error 1"), errors.New("error 2")},
			result: []error{errors.New("error 1"), errors.New("error 2")},
		},
		{
			name:   "Some empty errors",
			slice:  []error{errors.New("error 1"), nil, errors.New("error 2"), nil},
			result: []error{errors.New("error 1"), errors.New("error 2")},
		},
		{
			name:   "All empty errors",
			slice:  []error{nil, nil, nil},
			result: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := removeEmptyErrs(tt.slice)
			if !reflect.DeepEqual(got, tt.result) {
				t.Errorf("removeEmptyErrs() = %v, want %v", got, tt.result)
			}
		})
	}
}
func TestEnvVar(t *testing.T) {
	tests := []struct {
		name     string
		nameArg  string
		valueArg string
		expected corev1.EnvVar
	}{
		{
			name:     "Test case 1",
			nameArg:  "testName",
			valueArg: "testValue",
			expected: corev1.EnvVar{
				Name:  "testName",
				Value: "testValue",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnvVar(tt.nameArg, tt.valueArg)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("EnvVar() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestSecretEnvVar(t *testing.T) {
	tests := []struct {
		name       string
		nameArg    string
		secretName string
		secretKey  string
		expected   corev1.EnvVar
	}{
		{
			name:       "Test case 1",
			nameArg:    "testName",
			secretName: "testSecret",
			secretKey:  "testKey",
			expected: corev1.EnvVar{
				Name: "testName",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "testSecret",
						},
						Key: "testKey",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SecretEnvVar(tt.nameArg, tt.secretName, tt.secretKey)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("SecretEnvVar() = %v, want %v", got, tt.expected)
			}
		})
	}
}
