// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"fmt"
	"time"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/ollama"
	"go.uber.org/zap"
)

// Components holds all nlquery runtime components created from config.
// This is returned by NewComponentsFromConfig and used by the server
// to wire up both the search extractor and analysis endpoints.
type Components struct {
	// Extractor is the NL → SearchParams converter (heuristic or LLM-backed).
	// Nil when nlquery is disabled.
	Extractor Extractor

	// Analyzer is the trace/span analysis engine (LLM-backed).
	// Nil when no LLM provider is configured (heuristic-only mode).
	Analyzer *Analyzer

	// Sessions is the shared session manager for conversation state.
	// Nil when nlquery is disabled.
	Sessions *SessionManager
}

// NewComponentsFromConfig creates all nlquery components from configuration.
//
// This is the single factory entry point that replaces NewExtractorFromConfig.
// It creates the model once and shares it between the extractor and analyzer,
// avoiding duplicate connections to the same Ollama server.
func NewComponentsFromConfig(cfg Config, logger *zap.Logger) (*Components, error) {
	if !cfg.Enabled {
		return &Components{}, nil
	}

	sessions := NewSessionManager(defaultSessionTTL)

	if cfg.Provider == "" {
		logger.Info("nlquery: no provider configured, using heuristic extractor (analysis disabled)")
		return &Components{
			Extractor: &HeuristicExtractor{},
			Sessions:  sessions,
		}, nil
	}

	model, err := createModel(cfg, logger)
	if err != nil {
		sessions.Close()
		return nil, fmt.Errorf("nlquery: failed to create model: %w", err)
	}

	logger.Info("nlquery: using LLM extractor and analyzer",
		zap.String("provider", cfg.Provider),
		zap.String("model", cfg.Model),
		zap.String("endpoint", cfg.Endpoint),
	)

	// Use a higher max_tokens for analysis (summarization needs more output).
	analysisCfg := cfg
	if analysisCfg.MaxTokens < 512 {
		analysisCfg.MaxTokens = 512
	}

	return &Components{
		Extractor: NewLLMExtractor(model, cfg, logger),
		Analyzer:  NewAnalyzer(model, analysisCfg, sessions, logger),
		Sessions:  sessions,
	}, nil
}

// NewExtractorFromConfig creates just the Extractor (kept for backward compatibility).
// It closes the internal session manager because the caller only needs the Extractor.
func NewExtractorFromConfig(cfg Config, logger *zap.Logger) (Extractor, error) {
	comps, err := NewComponentsFromConfig(cfg, logger)
	if err != nil {
		return nil, err
	}
	// Close sessions since the caller only uses the Extractor.
	comps.Close()
	return comps.Extractor, nil
}

// Close releases resources held by Components.
func (c *Components) Close() {
	if c.Sessions != nil {
		c.Sessions.Close()
	}
}

// SessionTTLFromConfig returns the session TTL. Currently uses the default;
// this is an extension point for future YAML configuration.
func SessionTTLFromConfig(_ Config) time.Duration {
	return defaultSessionTTL
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
		// NOTE: Do NOT use ollama.WithFormat("json") here globally.
		// JSON mode is only needed for the extractor (slot filling),
		// not for the analyzer (free-text summarization).
		// Each caller passes llms.WithJSONMode() when needed.
		//
		// Do NOT use ollama.WithSystemPrompt() here. It only affects
		// the legacy /api/generate path, not /api/chat used by GenerateContent.
	}

	return ollama.New(opts...)
}
