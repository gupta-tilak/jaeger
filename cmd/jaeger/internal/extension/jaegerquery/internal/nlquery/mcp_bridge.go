// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery/querysvc"
	"github.com/jaegertracing/jaeger/internal/jptrace"
)

// TraceSearchResult is a lightweight summary of a trace, mirroring the
// output schema of the MCP search_traces tool (types.TraceSummary).
//
// Using a dedicated type in nlquery (instead of importing the MCP types
// package) avoids a dependency from the Query Service extension to the
// MCP extension. The field names and semantics are identical, so
// JSON-serialized results are interchangeable.
type TraceSearchResult struct {
	TraceID      string `json:"trace_id"`
	RootService  string `json:"root_service"`
	RootSpanName string `json:"root_span_name"`
	StartTime    string `json:"start_time"`
	DurationUs   int64  `json:"duration_us"`
	SpanCount    int    `json:"span_count"`
	ServiceCount int    `json:"service_count"`
	HasErrors    bool   `json:"has_errors"`
}

// ToolCaller abstracts trace query operations that both the MCP tools and
// the nlquery analysis pipeline perform.
//
// Defining this interface in nlquery (rather than importing the MCP
// extension package) provides three benefits:
//
//  1. The Analyzer can enrich analysis context with additional trace
//     data without creating a circular import to the MCP extension.
//  2. Tests inject a mock ToolCaller to verify analysis enrichment
//     without standing up a full MCP server or storage backend.
//  3. The interface is deliberately narrow — only the operations that
//     the analysis pipeline actually needs are exposed, not the full
//     QueryService surface.
//
// The concrete implementation (QueryServiceToolCaller) wraps the same
// QueryService methods that the MCP tool handlers use, ensuring
// behavioral equivalence between an MCP tool call and a direct
// Go function call.
type ToolCaller interface {
	// SearchTraces finds traces matching the given NL-extracted parameters.
	// Results are lightweight summaries (same schema as MCP search_traces output).
	SearchTraces(ctx context.Context, params SearchParams) ([]TraceSearchResult, error)

	// GetServices returns available service names from the storage backend.
	GetServices(ctx context.Context) ([]string, error)
}

// QueryServiceToolCaller implements ToolCaller by calling QueryService directly.
//
// It mirrors the logic in the MCP search_traces and get_services handlers
// without requiring the MCP framing (tool requests, SSE transport, etc.).
// This is intentionally a thin wrapper — all heavy lifting is done by
// QueryService and jptrace helpers.
type QueryServiceToolCaller struct {
	querySvc *querysvc.QueryService
}

// Compile-time check that QueryServiceToolCaller satisfies ToolCaller.
var _ ToolCaller = (*QueryServiceToolCaller)(nil)

// NewQueryServiceToolCaller creates a ToolCaller backed by the given QueryService.
func NewQueryServiceToolCaller(qs *querysvc.QueryService) *QueryServiceToolCaller {
	return &QueryServiceToolCaller{querySvc: qs}
}

const defaultSearchTimeRange = time.Hour

// SearchTraces converts NL-extracted SearchParams to QueryService parameters,
// executes the search, and returns lightweight trace summaries.
//
// Default behavior when time range is not specified:
//   - StartTimeMin defaults to 1 hour ago
//   - StartTimeMax defaults to now
//
// This mirrors the MCP search_traces handler's default of "-1h".
func (tc *QueryServiceToolCaller) SearchTraces(ctx context.Context, params SearchParams) ([]TraceSearchResult, error) {
	tqp, err := params.ToTraceQueryParams()
	if err != nil {
		return nil, fmt.Errorf("invalid search params: %w", err)
	}

	now := time.Now()
	queryParams := querysvc.TraceQueryParams{
		TraceQueryParams: *tqp,
		RawTraces:        false,
	}
	// Apply default time range (mirrors MCP search_traces defaults).
	if queryParams.StartTimeMin.IsZero() {
		queryParams.StartTimeMin = now.Add(-defaultSearchTimeRange)
	}
	if queryParams.StartTimeMax.IsZero() {
		queryParams.StartTimeMax = now
	}

	tracesIter := tc.querySvc.FindTraces(ctx, queryParams)
	aggregatedIter := jptrace.AggregateTraces(tracesIter)

	var results []TraceSearchResult
	for trace, iterErr := range aggregatedIter {
		if iterErr != nil {
			return results, fmt.Errorf("trace iteration error: %w", iterErr)
		}
		results = append(results, buildTraceSearchResult(trace))
	}

	return results, nil
}

// GetServices returns all service names known to the storage backend.
func (tc *QueryServiceToolCaller) GetServices(ctx context.Context) ([]string, error) {
	return tc.querySvc.GetServices(ctx)
}

// buildTraceSearchResult creates a TraceSearchResult from ptrace.Traces.
//
// This mirrors the MCP handler's buildTraceSummary function, extracting
// the same fields: trace ID, root service/span, timing, span count,
// service count, and error status.
func buildTraceSearchResult(trace ptrace.Traces) TraceSearchResult {
	var result TraceSearchResult
	services := make(map[string]struct{})
	var minStart, maxEnd time.Time

	for pos, span := range jptrace.SpanIter(trace) {
		result.SpanCount++
		result.TraceID = span.TraceID().String()

		if svc, ok := pos.Resource.Resource().Attributes().Get("service.name"); ok {
			services[svc.Str()] = struct{}{}
		}

		// Root span detection: a span with an empty parent span ID.
		if span.ParentSpanID().IsEmpty() {
			if svc, ok := pos.Resource.Resource().Attributes().Get("service.name"); ok {
				result.RootService = svc.Str()
			}
			result.RootSpanName = span.Name()
		}

		spanStart := span.StartTimestamp().AsTime()
		spanEnd := span.EndTimestamp().AsTime()

		if minStart.IsZero() || spanStart.Before(minStart) {
			minStart = spanStart
		}
		if maxEnd.IsZero() || spanEnd.After(maxEnd) {
			maxEnd = spanEnd
		}

		if span.Status().Code() == ptrace.StatusCodeError {
			result.HasErrors = true
		}
	}

	result.ServiceCount = len(services)
	if !minStart.IsZero() {
		result.StartTime = minStart.Format(time.RFC3339Nano)
		result.DurationUs = maxEnd.Sub(minStart).Microseconds()
	}

	return result
}
