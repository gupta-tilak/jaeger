// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

// systemPrompt instructs the model to behave as a structured extraction tool.
// It explicitly constrains output to JSON matching the SearchParams schema.
//
// Why outputs are schema-restricted:
// The model is used ONLY for slot filling / entity extraction. It must produce
// a strict JSON object matching SearchParams. No free-form text, no explanations,
// no conversational responses. json.Unmarshal into the SearchParams struct acts
// as the final safety firewall - any hallucinated or extra fields are silently
// dropped.
const systemPrompt = `You are a structured data extraction tool for a distributed tracing system (Jaeger).
Your ONLY job is to extract trace search parameters from natural language queries.

You MUST respond with ONLY a valid JSON object. No explanations, no markdown, no extra text.

The JSON object must use ONLY these fields (omit fields that cannot be determined from the input):
{
  "service": "service name string",
  "operation": "operation/span name string",
  "tags": {"key": "value"},
  "minDuration": "duration string like 2s, 500ms, 100us",
  "maxDuration": "duration string like 10s, 1m",
  "searchDepth": integer (number of results to return)
}

Rules:
- "service" is the name of a microservice (e.g., "payment-service", "frontend", "order-service")
- "operation" is an API endpoint or span name (e.g., "GET /api/users", "POST /checkout")
- "tags" maps attribute keys to string values (e.g., {"http.status_code": "500", "http.method": "GET"})
- Duration strings use Go duration format: "ns", "us", "ms", "s", "m", "h"
- HTTP status codes go in tags as {"http.status_code": "NNN"}
- If the user mentions "errors" or "failures" without a specific code, use {"error": "true"}
- If a field cannot be determined from the input, omit it entirely
- NEVER invent or guess values not present in the input`

// LLMExtractor uses a local language model (via LangChainGo) to extract
// structured trace search parameters from natural language input.
//
// Architecture notes:
//   - The LLM is treated as an extraction tool, NOT a decision maker.
//     It produces JSON; it does not execute queries or interpret results.
//   - json.Unmarshal into SearchParams acts as a safety firewall:
//     any hallucinated fields are silently dropped.
//   - Temperature is set to 0.0 by default for deterministic output.
//   - The model runs locally (Ollama / llama.cpp); no cloud APIs, no secrets.
//
// Why this is in the Query Service:
// The Query Service is the orchestration layer between user intent and storage.
// NL parsing is intent interpretation, which belongs here - not in the UI
// (presentation concern) and not in storage (data concern).
type LLMExtractor struct {
	model  llms.Model
	config Config
	logger *zap.Logger
}

var _ Extractor = (*LLMExtractor)(nil)

// NewLLMExtractor creates an extractor backed by the given LangChainGo model.
// The model instance is injected rather than created here, following the
// dependency injection pattern used throughout Jaeger (e.g., storage factories).
// This allows testing with mock models and swapping providers without changing
// extraction logic.
func NewLLMExtractor(model llms.Model, config Config, logger *zap.Logger) *LLMExtractor {
	return &LLMExtractor{
		model:  model,
		config: config,
		logger: logger,
	}
}

// Extract sends the natural language input to the local LLM and parses the
// structured JSON response into SearchParams.
//
// The extraction pipeline:
//  1. Build a prompt with the system prompt (schema instructions) + user input
//  2. Call the LLM with JSON mode enforced and temperature=0 for determinism
//  3. Parse the raw JSON response via json.Unmarshal into SearchParams
//  4. json.Unmarshal acts as the safety firewall: unknown/hallucinated fields
//     are silently dropped because they don't match the struct definition
//  5. Return the validated, schema-restricted SearchParams
//
// If the LLM returns invalid JSON, an error is returned rather than an empty
// struct - fail-fast over silent data loss.
func (e *LLMExtractor) Extract(ctx context.Context, input string) (SearchParams, error) {
	// Use GenerateContent with explicit system + human messages.
	// GenerateFromSinglePrompt / Call only send a human message and
	// ignore the system prompt set at construction time, which means
	// the model has no extraction schema instructions.
	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, systemPrompt),
		llms.TextParts(llms.ChatMessageTypeHuman, input),
	}

	resp, err := e.model.GenerateContent(ctx, messages,
		llms.WithTemperature(e.config.Temperature),
		llms.WithMaxTokens(e.config.MaxTokens),
		llms.WithJSONMode(),
	)
	if err != nil {
		return SearchParams{}, fmt.Errorf("llm generation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return SearchParams{}, errors.New("llm returned empty response")
	}
	completion := resp.Choices[0].Content

	e.logger.Debug("llm raw response",
		zap.String("input", input),
		zap.String("response", completion),
	)

	// json.Unmarshal is the safety firewall:
	// Only fields defined in SearchParams survive deserialization.
	// Any extra fields the model hallucinates (e.g., "confidence", "reasoning")
	// are silently dropped by Go's JSON decoder.
	var params SearchParams
	if err := json.Unmarshal([]byte(completion), &params); err != nil {
		return SearchParams{}, fmt.Errorf("llm returned invalid JSON: %w (raw: %q)", err, completion)
	}

	return params, nil
}
