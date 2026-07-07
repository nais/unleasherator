package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFeaturesValidate(t *testing.T) {
	tests := []struct {
		name                            string
		federationNamespaceBoundSecrets bool
		allowLegacyNameBoundSecrets     bool
		wantErr                         bool
	}{
		{
			name:                            "namespace-bound disabled and legacy disabled is invalid",
			federationNamespaceBoundSecrets: false,
			allowLegacyNameBoundSecrets:     false,
			wantErr:                         true,
		},
		{
			name:                            "default combination (namespace-bound disabled, legacy enabled) is valid",
			federationNamespaceBoundSecrets: false,
			allowLegacyNameBoundSecrets:     true,
			wantErr:                         false,
		},
		{
			name:                            "namespace-bound enabled and legacy enabled is valid",
			federationNamespaceBoundSecrets: true,
			allowLegacyNameBoundSecrets:     true,
			wantErr:                         false,
		},
		{
			name:                            "end-state (namespace-bound enabled, legacy disabled) is valid",
			federationNamespaceBoundSecrets: true,
			allowLegacyNameBoundSecrets:     false,
			wantErr:                         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &Features{
				FederationNamespaceBoundSecrets: tt.federationNamespaceBoundSecrets,
				AllowLegacyNameBoundSecrets:     tt.allowLegacyNameBoundSecrets,
			}
			err := f.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := &Config{
		Features: Features{
			FederationNamespaceBoundSecrets: false,
			AllowLegacyNameBoundSecrets:     false,
		},
	}
	assert.Error(t, cfg.Validate(), "contradictory feature flags should fail validation")

	cfg.Features.AllowLegacyNameBoundSecrets = true
	assert.NoError(t, cfg.Validate(), "default configuration should pass validation")
}
