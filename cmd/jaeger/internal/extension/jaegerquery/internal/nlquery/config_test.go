// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.False(t, cfg.Enabled)
	assert.Empty(t, cfg.Provider)
	assert.Empty(t, cfg.Endpoint)
	assert.Empty(t, cfg.Model)
	assert.InDelta(t, 0.0, cfg.Temperature, 1e-9)
	assert.Equal(t, 256, cfg.MaxTokens)
}

func TestConfig_Validate_Disabled(t *testing.T) {
	cfg := &Config{Enabled: false, Provider: "ollama"} // even with provider set
	require.NoError(t, cfg.Validate(), "disabled config should always validate")
}

func TestConfig_Validate_EnabledNoProvider(t *testing.T) {
	cfg := &Config{
		Enabled:     true,
		Temperature: 0.0,
		MaxTokens:   256,
	}
	require.NoError(t, cfg.Validate(), "enabled with no provider should be valid (heuristic fallback)")
}

func TestConfig_Validate_ProviderRequiresEndpoint(t *testing.T) {
	cfg := &Config{
		Enabled:  true,
		Provider: "ollama",
		Model:    "llama3.2",
		// Endpoint missing
		MaxTokens: 256,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "endpoint is required")
}

func TestConfig_Validate_ProviderRequiresModel(t *testing.T) {
	cfg := &Config{
		Enabled:  true,
		Provider: "ollama",
		Endpoint: "http://localhost:11434",
		// Model missing
		MaxTokens: 256,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model is required")
}

func TestConfig_Validate_InvalidTemperature(t *testing.T) {
	tests := []struct {
		name        string
		temperature float64
	}{
		{"negative", -0.1},
		{"above one", 1.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Enabled:     true,
				Temperature: tt.temperature,
				MaxTokens:   256,
			}
			err := cfg.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "temperature")
		})
	}
}

func TestConfig_Validate_NegativeMaxTokens(t *testing.T) {
	cfg := &Config{
		Enabled:   true,
		MaxTokens: -1,
	}
	err := cfg.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_tokens")
}

func TestConfig_Validate_FullyConfigured(t *testing.T) {
	cfg := &Config{
		Enabled:     true,
		Provider:    "ollama",
		Endpoint:    "http://localhost:11434",
		Model:       "llama3.2",
		Temperature: 0.1,
		MaxTokens:   512,
	}
	require.NoError(t, cfg.Validate())
}
