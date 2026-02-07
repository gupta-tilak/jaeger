// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
)

// Extractor extracts structured trace search parameters from natural language input.
//
// This interface is deliberately minimal:
//   - It accepts a context and a raw string.
//   - It returns a deterministic, schema-restricted SearchParams struct.
//   - It never executes a search — it only produces parameters.
//
// Implementations may be backed by:
//   - A stub that returns empty params (Phase 1 scaffold)
//   - A heuristic/regex parser (deterministic, no external dependencies)
//   - A local SLM via LangChainGo or Ollama (future Phase 3)
//
// Why AI is treated as an extraction tool, not a decision maker:
// The extractor transforms unstructured text into a fixed schema. It does not
// decide what to do with the parameters, does not execute queries, and does not
// interpret results. All downstream actions are handled by existing Jaeger
// primitives (QueryService.FindTraces, MCP tools, etc.).
type Extractor interface {
	Extract(ctx context.Context, input string) (SearchParams, error)
}

// StubExtractor is a no-op implementation that returns empty SearchParams.
// It exists to validate the structural scaffold before any extraction logic
// is implemented. This allows end-to-end wiring to be tested independently
// of the parsing implementation.
//
// Future extension point: replace StubExtractor with HeuristicExtractor or
// an LLM-backed extractor without changing any callers.
type StubExtractor struct{}

var _ Extractor = (*StubExtractor)(nil)

// Extract returns empty SearchParams regardless of input.
// This stub validates that the interface contract, JSON serialization, and
// HTTP handler wiring all work before any real extraction is added.
func (*StubExtractor) Extract(context.Context, string) (SearchParams, error) {
	return SearchParams{}, nil
}
