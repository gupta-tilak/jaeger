// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

const (
	// routePath is the HTTP endpoint for natural language query extraction.
	// This endpoint accepts a natural language string and returns structured
	// trace search parameters. It does NOT execute the search.
	routePath = "/api/nlquery"
)

// nlQueryRequest is the expected JSON body for the NL query endpoint.
type nlQueryRequest struct {
	Query string `json:"query"`
}

// nlQueryResponse wraps the extracted SearchParams for JSON serialization.
// The "params" field contains only known, schema-restricted fields.
type nlQueryResponse struct {
	Params SearchParams `json:"params"`
}

// nlQueryErrorResponse is returned when extraction or validation fails.
type nlQueryErrorResponse struct {
	Error string `json:"error"`
}

// HTTPHandler serves the natural language query extraction endpoint.
//
// Why this is in the Query Service (not UI, not storage):
// The Query Service is the orchestration layer between user intent and storage.
// NL query parsing is intent interpretation — it transforms a user's natural
// language into structured search parameters. The UI should remain a thin
// presentation layer, and storage should remain unaware of query sources.
type HTTPHandler struct {
	extractor Extractor
	logger    *zap.Logger
}

// NewHTTPHandler creates a handler that uses the given Extractor to process
// natural language queries. The extractor can be swapped (stub → heuristic → LLM)
// without changing the handler or route registration.
func NewHTTPHandler(extractor Extractor, logger *zap.Logger) *HTTPHandler {
	return &HTTPHandler{
		extractor: extractor,
		logger:    logger,
	}
}

// RegisterRoutes registers the NL query endpoint on the given router.
// This is additive — it does not modify or replace any existing routes.
func (h *HTTPHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc(routePath, h.handleNLQuery).Methods(http.MethodPost)
}

// handleNLQuery processes a natural language query request through the extractor
// and returns structured search parameters as JSON.
//
// The response always contains a valid SearchParams struct (possibly empty).
// json.Unmarshal is used as a safety firewall in the extraction pipeline:
// any unexpected or hallucinated fields from a future LLM implementation
// are dropped because they do not match the SearchParams struct definition.
func (h *HTTPHandler) handleNLQuery(w http.ResponseWriter, r *http.Request) {
	var req nlQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if req.Query == "" {
		h.writeError(w, http.StatusBadRequest, "query field is required")
		return
	}

	params, err := h.extractor.Extract(r.Context(), req.Query)
	if err != nil {
		h.logger.Error("nlquery extraction failed", zap.String("query", req.Query), zap.Error(err))
		h.writeError(w, http.StatusInternalServerError, "extraction failed: "+err.Error())
		return
	}

	h.writeJSON(w, http.StatusOK, nlQueryResponse{Params: params})
}

func (h *HTTPHandler) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.Error("failed to write response", zap.Error(err))
	}
}

func (h *HTTPHandler) writeError(w http.ResponseWriter, status int, msg string) {
	h.writeJSON(w, status, nlQueryErrorResponse{Error: msg})
}
