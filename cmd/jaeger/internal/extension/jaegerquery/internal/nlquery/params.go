// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

// Package nlquery implements natural language to trace search parameter extraction.
//
// # Architecture Note
//
// This package lives inside the Query Service extension because the Query Service
// is the orchestration layer between user intent and storage backends. Natural
// language parsing is a form of intent interpretation, which logically belongs
// here - not in the UI (presentation concern) and not in storage (data concern).
//
// AI / LLM components are treated strictly as extraction tools, never as decision
// makers. The LLM (when integrated) produces a structured JSON output that is
// deserialized into a fixed Go struct. json.Unmarshal acts as a safety firewall:
// any hallucinated or unexpected fields are silently dropped, ensuring only
// known parameters reach the query engine.
package nlquery

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/jaegertracing/jaeger/internal/storage/v2/api/tracestore"
)

// SearchParams represents the structured output of natural language query extraction.
//
// These parameters map directly to existing Jaeger trace search parameters
// (see tracestore.TraceQueryParams). This struct is intentionally kept as a
// flat, JSON-serializable type so that:
//
//  1. json.Unmarshal can be used as a safety boundary - unexpected fields from
//     a future LLM extractor are silently dropped.
//  2. String-typed duration fields allow flexible human input ("2s", "500ms")
//     that is validated during conversion to canonical types.
//  3. Tags use map[string]string rather than pcommon.Map, making the struct
//     safe for JSON round-tripping without OTel dependencies in the schema.
type SearchParams struct {
	// Service is the target service name (e.g., "payment-service").
	Service string `json:"service,omitempty"`

	// Operation is the span/operation name (e.g., "GET /api/checkout").
	Operation string `json:"operation,omitempty"`

	// Tags are key-value attribute filters (e.g., {"http.status_code": "500"}).
	Tags map[string]string `json:"tags,omitempty"`

	// MinDuration is the minimum trace duration as a Go duration string (e.g., "2s", "500ms").
	MinDuration string `json:"minDuration,omitempty"`

	// MaxDuration is the maximum trace duration as a Go duration string (e.g., "10s").
	MaxDuration string `json:"maxDuration,omitempty"`

	// SearchDepth is the maximum number of traces to return (maps to "limit").
	SearchDepth int `json:"searchDepth,omitempty"`
}

// ToTraceQueryParams converts the extracted search parameters into the canonical
// tracestore.TraceQueryParams used by the Jaeger Query Service.
//
// This conversion performs validation (e.g., duration parsing) and acts as the
// boundary between the free-form extraction layer and the typed query engine.
// If a duration string is malformed, an error is returned rather than silently
// using a zero value - fail-fast is preferred over silent data loss.
func (p *SearchParams) ToTraceQueryParams() (*tracestore.TraceQueryParams, error) {
	var (
		minDur time.Duration
		maxDur time.Duration
		err    error
	)

	if p.MinDuration != "" {
		minDur, err = time.ParseDuration(p.MinDuration)
		if err != nil {
			return nil, fmt.Errorf("invalid minDuration %q: %w", p.MinDuration, err)
		}
	}

	if p.MaxDuration != "" {
		maxDur, err = time.ParseDuration(p.MaxDuration)
		if err != nil {
			return nil, fmt.Errorf("invalid maxDuration %q: %w", p.MaxDuration, err)
		}
	}

	attrs := pcommon.NewMap()
	for k, v := range p.Tags {
		attrs.PutStr(k, v)
	}

	params := &tracestore.TraceQueryParams{
		ServiceName:   p.Service,
		OperationName: p.Operation,
		Attributes:    attrs,
		DurationMin:   minDur,
		DurationMax:   maxDur,
		SearchDepth:   p.SearchDepth,
	}

	return params, nil
}

// ToMCPArgs converts SearchParams to a map[string]any that matches the
// MCP search_traces tool's input schema (types.SearchTracesInput field names).
//
// This enables NL-extracted parameters to be forwarded to MCP tools
// interchangeably. The key mapping is:
//
//	Service     → service_name
//	Operation   → span_name
//	MinDuration → duration_min
//	MaxDuration → duration_max
//	SearchDepth → search_depth
//	Tags        → attributes
//
// The method only includes non-zero fields, mirroring the "omitempty"
// semantics of the MCP input struct.
func (p *SearchParams) ToMCPArgs() map[string]any {
	args := make(map[string]any)
	if p.Service != "" {
		args["service_name"] = p.Service
	}
	if p.Operation != "" {
		args["span_name"] = p.Operation
	}
	if p.MinDuration != "" {
		args["duration_min"] = p.MinDuration
	}
	if p.MaxDuration != "" {
		args["duration_max"] = p.MaxDuration
	}
	if p.SearchDepth > 0 {
		args["search_depth"] = p.SearchDepth
	}
	if len(p.Tags) > 0 {
		args["attributes"] = p.Tags
	}
	return args
}
