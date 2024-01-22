package utils

import (
	"testing"
)

func TestStringSliceEquals(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{
			name: "Equal slices",
			a:    []string{"apple", "banana", "cherry"},
			b:    []string{"apple", "banana", "cherry"},
			want: true,
		},
		{
			name: "Equal slices, different order",
			a:    []string{"apple", "banana", "cherry"},
			b:    []string{"banana", "cherry", "apple"},
			want: true,
		},
		{
			name: "Different lengths",
			a:    []string{"apple", "banana", "cherry"},
			b:    []string{"apple", "banana"},
			want: false,
		},
		{
			name: "Different elements",
			a:    []string{"apple", "banana", "cherry"},
			b:    []string{"apple", "orange", "cherry"},
			want: false,
		},
		{
			name: "Empty slices",
			a:    []string{},
			b:    []string{},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StringSliceEquals(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("StringSliceEquals() = %v, want %v", got, tt.want)
			}
		})
	}
}
