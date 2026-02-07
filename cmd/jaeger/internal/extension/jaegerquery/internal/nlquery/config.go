// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"errors"
)

// Config holds configuration for the natural language query extraction feature.
//
// This config is designed to be embedded in the Query Service's YAML configuration.
// All model-specific values (endpoint, model name) are user-provided and NEVER
// hardcoded, ensuring the system works with any local model provider.
//
// Example YAML:
//
//	jaeger_query:
//	  nlquery:
//	    enabled: true
//	    provider: "ollama"
//	    endpoint: "http://localhost:11434"
//	    model: "llama3.2"
//	    temperature: 0.0
//	    max_tokens: 256
type Config struct {
	// Enabled controls whether the NL query endpoint is active.
	// When false, the endpoint returns 404 and no LLM is initialized.
	Enabled bool `mapstructure:"enabled"`

	// Provider identifies the LLM backend. Currently supported: "ollama".
	// This field selects which LangChainGo provider to instantiate.
	// Empty string means disabled (uses heuristic fallback).
	Provider string `mapstructure:"provider" valid:"optional"`

	// Endpoint is the URL of the local model server (e.g., "http://localhost:11434").
	// NEVER hardcoded — must be explicitly configured by the operator.
	Endpoint string `mapstructure:"endpoint" valid:"optional"`

	// Model is the name of the model to use (e.g., "llama3.2", "mistral", "phi3").
	// NEVER hardcoded — must be explicitly configured by the operator.
	Model string `mapstructure:"model" valid:"optional"`

	// Temperature controls the randomness of model output.
	// 0.0 = fully deterministic (recommended for extraction tasks).
	// Range: 0.0 - 1.0.
	Temperature float64 `mapstructure:"temperature"`

	// MaxTokens limits the number of tokens in the model's response.
	// Lower values are preferred since we expect compact JSON output.
	// Default: 256.
	MaxTokens int `mapstructure:"max_tokens"`
}

// DefaultConfig returns sensible defaults with NL query disabled.
// All model-specific fields are intentionally empty, requiring explicit
// operator configuration before an LLM-backed extractor is activated.
func DefaultConfig() Config {
	return Config{
		Enabled:     false,
		Temperature: 0.0,
		MaxTokens:   256,
	}
}

// Validate checks that the configuration is internally consistent.
// If a provider is specified, endpoint and model must also be set.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider != "" {
		if c.Endpoint == "" {
			return errors.New("nlquery: endpoint is required when provider is set")
		}
		if c.Model == "" {
			return errors.New("nlquery: model is required when provider is set")
		}
	}
	if c.Temperature < 0 || c.Temperature > 1 {
		return errors.New("nlquery: temperature must be between 0.0 and 1.0")
	}
	if c.MaxTokens < 0 {
		return errors.New("nlquery: max_tokens must be non-negative")
	}
	return nil
}
