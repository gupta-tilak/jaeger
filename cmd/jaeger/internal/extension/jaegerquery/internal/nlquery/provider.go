// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
	"go.uber.org/zap"
)

// NewExtractorFromConfig creates the appropriate Extractor implementation
// based on the provided configuration.
//
// Selection logic:
//   - If NLQuery is disabled (Enabled=false): returns nil (caller should not register route)
//   - If no provider is configured: returns HeuristicExtractor (no external dependencies)
//   - If provider is set: creates an LLM-backed extractor via LangChainGo
//
// This function acts as the factory boundary between configuration and runtime.
// Model names, endpoints, and temperatures are NEVER hardcoded - they flow
// from the YAML config provided by the operator.
func NewExtractorFromConfig(cfg Config, logger *zap.Logger) (Extractor, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	if cfg.Provider == "" {
		logger.Info("nlquery: no provider configured, using heuristic extractor")
		return &HeuristicExtractor{}, nil
	}

	model, err := createModel(cfg, logger)
	if err != nil {
		return nil, fmt.Errorf("nlquery: failed to create model: %w", err)
	}

	logger.Info("nlquery: using LLM extractor",
		zap.String("provider", cfg.Provider),
		zap.String("model", cfg.Model),
		zap.String("endpoint", cfg.Endpoint),
	)

	return NewLLMExtractor(model, cfg, logger), nil
}

// createModel instantiates a LangChainGo model based on the provider name.
//
// Supported providers:
//   - "ollama": connects to a local Ollama server
//
// Extension point: add new providers (e.g., "llamacpp", "vllm") by adding
// cases here. Each provider maps to a LangChainGo constructor with
// operator-supplied configuration. No API keys or cloud services.
func createModel(cfg Config, logger *zap.Logger) (llms.Model, error) {
	switch cfg.Provider {
	case "ollama":
		return createOllamaModel(cfg, logger)
	default:
		return nil, fmt.Errorf("unsupported nlquery provider: %q (supported: ollama)", cfg.Provider)
	}
}

// createOllamaModel creates an Ollama LLM client.
// The endpoint and model name come entirely from configuration.
func createOllamaModel(cfg Config, logger *zap.Logger) (llms.Model, error) {
	logger.Debug("creating Ollama model",
		zap.String("endpoint", cfg.Endpoint),
		zap.String("model", cfg.Model),
	)

	opts := []ollama.Option{
		ollama.WithModel(cfg.Model),
		ollama.WithServerURL(cfg.Endpoint),
		// Force JSON output at the Ollama API level.
		// This is complementary to llms.WithJSONMode() used at call time.
		ollama.WithFormat("json"),
		// Inject the system prompt at construction time so every call
		// enforces the schema-restricted extraction behavior.
		ollama.WithSystemPrompt(systemPrompt),
	}

	return ollama.New(opts...)
}
