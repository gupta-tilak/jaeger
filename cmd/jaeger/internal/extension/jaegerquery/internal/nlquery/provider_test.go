// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestNewExtractorFromConfig_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	ext, err := NewExtractorFromConfig(cfg, zap.NewNop())
	require.NoError(t, err)
	assert.Nil(t, ext, "disabled config should return nil extractor")
}

func TestNewExtractorFromConfig_NoProvider(t *testing.T) {
	cfg := Config{Enabled: true, MaxTokens: 256}
	ext, err := NewExtractorFromConfig(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, ext)
	_, ok := ext.(*HeuristicExtractor)
	assert.True(t, ok, "no provider should produce HeuristicExtractor")
}

func TestNewExtractorFromConfig_UnsupportedProvider(t *testing.T) {
	cfg := Config{
		Enabled:  true,
		Provider: "unsupported-provider",
		Endpoint: "http://localhost:11434",
		Model:    "test",
	}
	_, err := NewExtractorFromConfig(cfg, zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported nlquery provider")
}

func TestCreateModel_UnsupportedProvider(t *testing.T) {
	cfg := Config{Provider: "magic-llm", Endpoint: "http://localhost:1234", Model: "m"}
	_, err := createModel(cfg, zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported nlquery provider")
	assert.Contains(t, err.Error(), "magic-llm")
}

func TestNewComponentsFromConfig_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	comps, err := NewComponentsFromConfig(cfg, zap.NewNop())
	require.NoError(t, err)
	assert.Nil(t, comps.Extractor)
	assert.Nil(t, comps.Analyzer)
	assert.Nil(t, comps.Sessions)
}

func TestNewComponentsFromConfig_NoProvider(t *testing.T) {
	cfg := Config{Enabled: true}
	comps, err := NewComponentsFromConfig(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NotNil(t, comps.Extractor)
	_, ok := comps.Extractor.(*HeuristicExtractor)
	assert.True(t, ok, "should produce HeuristicExtractor when no provider")
	assert.Nil(t, comps.Analyzer, "analyzer requires a provider")
	assert.NotNil(t, comps.Sessions)
	comps.Close()
}

func TestNewComponentsFromConfig_UnsupportedProvider(t *testing.T) {
	cfg := Config{
		Enabled:  true,
		Provider: "unsupported",
		Endpoint: "http://localhost:11434",
		Model:    "model",
	}
	_, err := NewComponentsFromConfig(cfg, zap.NewNop())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported nlquery provider")
}

func TestComponents_Close_NilSessions(_ *testing.T) {
	comps := &Components{}
	comps.Close() // Should not panic
}

func TestSessionTTLFromConfig(t *testing.T) {
	ttl := SessionTTLFromConfig(Config{})
	assert.Equal(t, defaultSessionTTL, ttl)
}
