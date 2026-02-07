// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockExtractor is a test double that returns configurable results.
type mockExtractor struct {
	params SearchParams
	err    error
}

func (m *mockExtractor) Extract(context.Context, string) (SearchParams, error) {
	return m.params, m.err
}

func TestHTTPHandler_HandleNLQuery_Success(t *testing.T) {
	extractor := &mockExtractor{
		params: SearchParams{
			Service:     "payment-service",
			Tags:        map[string]string{"http.status_code": "500"},
			MinDuration: "2s",
		},
	}
	handler := NewHTTPHandler(extractor, zap.NewNop())

	body := `{"query": "show me 500 errors from payment-service taking more than 2 seconds"}`
	req := httptest.NewRequest(http.MethodPost, routePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.handleNLQuery(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var resp nlQueryResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	assert.Equal(t, "payment-service", resp.Params.Service)
	assert.Equal(t, "2s", resp.Params.MinDuration)
	assert.Equal(t, map[string]string{"http.status_code": "500"}, resp.Params.Tags)
}

func TestHTTPHandler_HandleNLQuery_EmptyQuery(t *testing.T) {
	handler := NewHTTPHandler(&StubExtractor{}, zap.NewNop())

	body := `{"query": ""}`
	req := httptest.NewRequest(http.MethodPost, routePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.handleNLQuery(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp nlQueryErrorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Error, "query field is required")
}

func TestHTTPHandler_HandleNLQuery_InvalidJSON(t *testing.T) {
	handler := NewHTTPHandler(&StubExtractor{}, zap.NewNop())

	req := httptest.NewRequest(http.MethodPost, routePath, bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	handler.handleNLQuery(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var resp nlQueryErrorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Error, "invalid request body")
}

func TestHTTPHandler_HandleNLQuery_ExtractorError(t *testing.T) {
	extractor := &mockExtractor{
		err: errors.New("model unavailable"),
	}
	handler := NewHTTPHandler(extractor, zap.NewNop())

	body := `{"query": "show me traces"}`
	req := httptest.NewRequest(http.MethodPost, routePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.handleNLQuery(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp nlQueryErrorResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Error, "extraction failed")
}

func TestHTTPHandler_HandleNLQuery_StubExtractor(t *testing.T) {
	handler := NewHTTPHandler(&StubExtractor{}, zap.NewNop())

	body := `{"query": "any input"}`
	req := httptest.NewRequest(http.MethodPost, routePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.handleNLQuery(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp nlQueryResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)

	// StubExtractor returns empty params.
	assert.Empty(t, resp.Params.Service)
	assert.Nil(t, resp.Params.Tags)
}

func TestHTTPHandler_RegisterRoutes(t *testing.T) {
	handler := NewHTTPHandler(&StubExtractor{}, zap.NewNop())
	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	// Verify the route was registered by attempting a request.
	body := `{"query": "test"}`
	req := httptest.NewRequest(http.MethodPost, routePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Should get 200 OK from the stub extractor, not 404.
	assert.Equal(t, http.StatusOK, rec.Code)
}
