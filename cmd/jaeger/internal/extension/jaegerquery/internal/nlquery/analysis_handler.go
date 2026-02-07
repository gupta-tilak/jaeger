// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"

	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery/querysvc"
	"github.com/jaegertracing/jaeger/internal/storage/v2/api/tracestore"
)

const (
	analyzeSpanPath  = "/api/nlquery/analyze/span"
	analyzeTracePath = "/api/nlquery/analyze/trace"
	followUpPath     = "/api/nlquery/analyze/followup"
)

// analyzeSpanRequest is the JSON body for POST /api/nlquery/analyze/span.
type analyzeSpanRequest struct {
	TraceID   string `json:"trace_id"`
	SpanID    string `json:"span_id"`
	SessionID string `json:"session_id,omitempty"`
}

// analyzeTraceRequest is the JSON body for POST /api/nlquery/analyze/trace.
type analyzeTraceRequest struct {
	TraceID   string `json:"trace_id"`
	SessionID string `json:"session_id,omitempty"`
}

// followUpRequest is the JSON body for POST /api/nlquery/analyze/followup.
type followUpRequest struct {
	SessionID string `json:"session_id"`
	Question  string `json:"question"`
}

// analysisResponse is the JSON response for analysis endpoints.
type analysisResponse struct {
	Analysis  string `json:"analysis"`
	SessionID string `json:"session_id"`
}

// AnalysisHandler serves the contextual analysis endpoints.
//
// It bridges the HTTP layer with:
//   - QueryService: to fetch trace/span data from storage
//   - Analyzer: to send pruned data to the LLM for analysis
//
// Endpoints:
//   - POST /api/nlquery/analyze/span   — Explain a specific span
//   - POST /api/nlquery/analyze/trace  — Explain an entire trace
//   - POST /api/nlquery/analyze/followup — Ask a follow-up question
type AnalysisHandler struct {
	querySvc *querysvc.QueryService
	analyzer *Analyzer
	logger   *zap.Logger
}

// NewAnalysisHandler creates a handler for trace/span analysis.
func NewAnalysisHandler(querySvc *querysvc.QueryService, analyzer *Analyzer, logger *zap.Logger) *AnalysisHandler {
	return &AnalysisHandler{
		querySvc: querySvc,
		analyzer: analyzer,
		logger:   logger,
	}
}

// RegisterRoutes registers the analysis HTTP routes on the given router.
func (h *AnalysisHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc(analyzeSpanPath, h.handleAnalyzeSpan).Methods(http.MethodPost)
	router.HandleFunc(analyzeTracePath, h.handleAnalyzeTrace).Methods(http.MethodPost)
	router.HandleFunc(followUpPath, h.handleFollowUp).Methods(http.MethodPost)
}

func (h *AnalysisHandler) handleAnalyzeSpan(w http.ResponseWriter, r *http.Request) {
	var req analyzeSpanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnalysisError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TraceID == "" || req.SpanID == "" {
		writeAnalysisError(w, "trace_id and span_id are required", http.StatusBadRequest)
		return
	}

	// Parse trace ID.
	traceID, err := parseHexTraceID(req.TraceID)
	if err != nil {
		writeAnalysisError(w, "invalid trace_id format", http.StatusBadRequest)
		return
	}

	// Parse span ID.
	spanID, err := parseHexSpanID(req.SpanID)
	if err != nil {
		writeAnalysisError(w, "invalid span_id format", http.StatusBadRequest)
		return
	}

	// Fetch the trace from storage.
	traces, err := h.fetchTrace(r.Context(), traceID)
	if err != nil {
		h.logger.Error("failed to fetch trace", zap.Error(err))
		writeAnalysisError(w, "failed to fetch trace", http.StatusInternalServerError)
		return
	}
	if traces.SpanCount() == 0 {
		writeAnalysisError(w, "trace not found", http.StatusNotFound)
		return
	}

	// Find the specific span.
	span, resource, found := FindSpanInTrace(traces, spanID)
	if !found {
		writeAnalysisError(w, "span not found in trace", http.StatusNotFound)
		return
	}

	// Prune and analyze.
	prunedSpan := PruneSpan(span, resource)
	analysis, sessionID, err := h.analyzer.AnalyzeSpan(r.Context(), prunedSpan, req.SessionID)
	if err != nil {
		h.logger.Error("span analysis failed", zap.Error(err))
		writeAnalysisError(w, "analysis failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeAnalysisResponse(w, analysis, sessionID)
}

func (h *AnalysisHandler) handleAnalyzeTrace(w http.ResponseWriter, r *http.Request) {
	var req analyzeTraceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnalysisError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TraceID == "" {
		writeAnalysisError(w, "trace_id is required", http.StatusBadRequest)
		return
	}

	// Parse trace ID.
	traceID, err := parseHexTraceID(req.TraceID)
	if err != nil {
		writeAnalysisError(w, "invalid trace_id format", http.StatusBadRequest)
		return
	}

	// Fetch the trace from storage.
	traces, err := h.fetchTrace(r.Context(), traceID)
	if err != nil {
		h.logger.Error("failed to fetch trace", zap.Error(err))
		writeAnalysisError(w, "failed to fetch trace", http.StatusInternalServerError)
		return
	}
	if traces.SpanCount() == 0 {
		writeAnalysisError(w, "trace not found", http.StatusNotFound)
		return
	}

	// Prune and analyze.
	prunedTrace := PruneTrace(traces)
	analysis, sessionID, err := h.analyzer.AnalyzeTrace(r.Context(), prunedTrace, req.SessionID)
	if err != nil {
		h.logger.Error("trace analysis failed", zap.Error(err))
		writeAnalysisError(w, "analysis failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeAnalysisResponse(w, analysis, sessionID)
}

func (h *AnalysisHandler) handleFollowUp(w http.ResponseWriter, r *http.Request) {
	var req followUpRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnalysisError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" || req.Question == "" {
		writeAnalysisError(w, "session_id and question are required", http.StatusBadRequest)
		return
	}

	analysis, err := h.analyzer.FollowUp(r.Context(), req.Question, req.SessionID)
	if err != nil {
		h.logger.Error("follow-up failed", zap.Error(err))
		writeAnalysisError(w, "follow-up failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	writeAnalysisResponse(w, analysis, req.SessionID)
}

// fetchTrace retrieves a single trace from the query service.
// It consumes the iterator and aggregates all chunks into one ptrace.Traces.
func (h *AnalysisHandler) fetchTrace(ctx context.Context, traceID pcommon.TraceID) (ptrace.Traces, error) {
	params := querysvc.GetTraceParams{
		TraceIDs: []tracestore.GetTraceParams{
			{TraceID: traceID},
		},
	}

	result := ptrace.NewTraces()
	for traces, err := range h.querySvc.GetTraces(ctx, params) {
		if err != nil {
			return ptrace.NewTraces(), err
		}
		for _, t := range traces {
			t.ResourceSpans().MoveAndAppendTo(result.ResourceSpans())
		}
	}
	return result, nil
}

func writeAnalysisResponse(w http.ResponseWriter, analysis, sessionID string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(analysisResponse{
		Analysis:  analysis,
		SessionID: sessionID,
	})
}

func writeAnalysisError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(nlQueryErrorResponse{Error: msg})
}

// parseHexTraceID parses a hex string into a pcommon.TraceID.
// TraceID is 16 bytes represented as 32 hex characters.
func parseHexTraceID(s string) (pcommon.TraceID, error) {
	if len(s) != 32 {
		return pcommon.TraceID{}, fmt.Errorf("trace ID must be 32 hex characters, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return pcommon.TraceID{}, fmt.Errorf("invalid hex string: %w", err)
	}
	var tid pcommon.TraceID
	copy(tid[:], b)
	return tid, nil
}

// parseHexSpanID parses a hex string into a pcommon.SpanID.
// SpanID is 8 bytes represented as 16 hex characters.
func parseHexSpanID(s string) (pcommon.SpanID, error) {
	if len(s) != 16 {
		return pcommon.SpanID{}, fmt.Errorf("span ID must be 16 hex characters, got %d", len(s))
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return pcommon.SpanID{}, fmt.Errorf("invalid hex string: %w", err)
	}
	var sid pcommon.SpanID
	copy(sid[:], b)
	return sid, nil
}
