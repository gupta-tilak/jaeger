// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

// fakeModel is a minimal test double implementing llms.Model.
// It returns a configurable string response or error, without
// calling any external service.
type fakeModel struct {
	response string
	err      error
}

func (f *fakeModel) GenerateContent(
	_ context.Context,
	_ []llms.MessageContent,
	_ ...llms.CallOption,
) (*llms.ContentResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: f.response},
		},
	}, nil
}

func (f *fakeModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return f.response, f.err
}

func TestLLMExtractor_Extract_ValidJSON(t *testing.T) {
	model := &fakeModel{
		response: `{"service":"payment-service","tags":{"http.status_code":"500"},"minDuration":"2s"}`,
	}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(model, cfg, zap.NewNop())

	params, err := ext.Extract(context.Background(), "show me 500 errors from payment-service")
	require.NoError(t, err)
	assert.Equal(t, "payment-service", params.Service)
	assert.Equal(t, "2s", params.MinDuration)
	assert.Equal(t, map[string]string{"http.status_code": "500"}, params.Tags)
}

func TestLLMExtractor_Extract_EmptyJSON(t *testing.T) {
	model := &fakeModel{response: `{}`}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(model, cfg, zap.NewNop())

	params, err := ext.Extract(context.Background(), "hello")
	require.NoError(t, err)
	assert.Empty(t, params.Service)
	assert.Empty(t, params.Operation)
}

func TestLLMExtractor_Extract_HallucinatedFields(t *testing.T) {
	// The model returns extra fields that don't exist in SearchParams.
	// json.Unmarshal should silently drop them (the safety firewall).
	model := &fakeModel{
		response: `{"service":"frontend","confidence":0.99,"reasoning":"I found the service","unknown_field":true}`,
	}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(model, cfg, zap.NewNop())

	params, err := ext.Extract(context.Background(), "show traces from frontend")
	require.NoError(t, err)
	assert.Equal(t, "frontend", params.Service)
	// Hallucinated fields must not appear in the struct
	assert.Empty(t, params.Operation)
	assert.Empty(t, params.Tags)
}

func TestLLMExtractor_Extract_InvalidJSON(t *testing.T) {
	model := &fakeModel{response: `this is not json at all`}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(model, cfg, zap.NewNop())

	_, err := ext.Extract(context.Background(), "show traces")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON")
}

func TestLLMExtractor_Extract_ModelError(t *testing.T) {
	model := &fakeModel{err: assert.AnError}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(model, cfg, zap.NewNop())

	_, err := ext.Extract(context.Background(), "show traces")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm generation failed")
}

func TestLLMExtractor_Extract_EmptyChoices(t *testing.T) {
	emptyModel := &emptyChoicesModel{}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(emptyModel, cfg, zap.NewNop())

	_, err := ext.Extract(context.Background(), "show traces")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")
}

// emptyChoicesModel returns a response with no choices.
type emptyChoicesModel struct{}

func (*emptyChoicesModel) GenerateContent(
	_ context.Context,
	_ []llms.MessageContent,
	_ ...llms.CallOption,
) (*llms.ContentResponse, error) {
	return &llms.ContentResponse{Choices: []*llms.ContentChoice{}}, nil
}

func (*emptyChoicesModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", nil
}

func TestLLMExtractor_Extract_AllFields(t *testing.T) {
	model := &fakeModel{
		response: `{
			"service": "order-service",
			"operation": "POST /checkout",
			"tags": {"http.status_code": "200", "http.method": "POST"},
			"minDuration": "100ms",
			"maxDuration": "5s",
			"searchDepth": 50
		}`,
	}
	cfg := Config{Temperature: 0.0, MaxTokens: 256}
	ext := NewLLMExtractor(model, cfg, zap.NewNop())

	params, err := ext.Extract(context.Background(), "POST /checkout from order-service 100ms-5s top 50")
	require.NoError(t, err)
	assert.Equal(t, "order-service", params.Service)
	assert.Equal(t, "POST /checkout", params.Operation)
	assert.Equal(t, "100ms", params.MinDuration)
	assert.Equal(t, "5s", params.MaxDuration)
	assert.Equal(t, 50, params.SearchDepth)
	assert.Equal(t, map[string]string{"http.status_code": "200", "http.method": "POST"}, params.Tags)
}

func TestLLMExtractor_ImplementsExtractor(t *testing.T) {
	t.Parallel()
	var _ Extractor = (*LLMExtractor)(nil)
}
