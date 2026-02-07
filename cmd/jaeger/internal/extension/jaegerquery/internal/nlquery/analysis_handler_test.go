// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"bytes"
	"encoding/json"
	"iter"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.uber.org/zap"

	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery/querysvc"
	depsmocks "github.com/jaegertracing/jaeger/internal/storage/v2/api/depstore/mocks"
	tracestoremocks "github.com/jaegertracing/jaeger/internal/storage/v2/api/tracestore/mocks"
)

// ---- helpers ----

func tracesIter(traces ...ptrace.Traces) iter.Seq2[[]ptrace.Traces, error] {
	return func(yield func([]ptrace.Traces, error) bool) {
		yield(traces, nil)
	}
}

func emptyTracesIter() iter.Seq2[[]ptrace.Traces, error] {
	return func(func([]ptrace.Traces, error) bool) {}
}

var testTraceID = pcommon.TraceID([16]byte{
	0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
	0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef,
})
var testSpanID = pcommon.SpanID([8]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef})

func buildTestTrace() ptrace.Traces {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "test-service")
	scope := rs.ScopeSpans().AppendEmpty()
	span := scope.Spans().AppendEmpty()
	span.SetName("test-operation")
	span.SetTraceID(testTraceID)
	span.SetSpanID(testSpanID)
	span.SetKind(ptrace.SpanKindServer)
	span.Status().SetCode(ptrace.StatusCodeOk)
	now := time.Now()
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(now))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(100 * time.Millisecond)))
	return traces
}

func newTestAnalysisHandler(t *testing.T, traces ptrace.Traces, modelResp string) *AnalysisHandler {
	t.Helper()
	traceReader := &tracestoremocks.Reader{}
	if traces.SpanCount() > 0 {
		traceReader.On("GetTraces", mock.Anything, mock.Anything).
			Return(tracesIter(traces)).Maybe()
	} else {
		traceReader.On("GetTraces", mock.Anything, mock.Anything).
			Return(emptyTracesIter()).Maybe()
	}
	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	model := &mockModel{response: modelResp}
	sm := NewSessionManager(time.Minute)
	t.Cleanup(sm.Close)
	cfg := Config{Enabled: true, Temperature: 0.1, MaxTokens: 256}
	analyzer := NewAnalyzer(model, cfg, sm, zap.NewNop())
	return NewAnalysisHandler(qs, analyzer, zap.NewNop())
}

// ---- parseHexTraceID / parseHexSpanID tests ----

func TestParseHexTraceID_Valid(t *testing.T) {
	tid, err := parseHexTraceID("0123456789abcdef0123456789abcdef")
	require.NoError(t, err)
	assert.Equal(t, testTraceID, tid)
}

func TestParseHexTraceID_WrongLength(t *testing.T) {
	_, err := parseHexTraceID("0123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "32 hex characters")
}

func TestParseHexTraceID_InvalidHex(t *testing.T) {
	_, err := parseHexTraceID("zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hex")
}

func TestParseHexSpanID_Valid(t *testing.T) {
	sid, err := parseHexSpanID("0123456789abcdef")
	require.NoError(t, err)
	assert.Equal(t, testSpanID, sid)
}

func TestParseHexSpanID_WrongLength(t *testing.T) {
	_, err := parseHexSpanID("0123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "16 hex characters")
}

func TestParseHexSpanID_InvalidHex(t *testing.T) {
	_, err := parseHexSpanID("zzzzzzzzzzzzzzzz")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid hex")
}

// ---- HTTP handler endpoint tests ----

func TestAnalyzeSpan_Success(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "This span handles test-operation.")

	body := `{"trace_id":"0123456789abcdef0123456789abcdef","span_id":"0123456789abcdef"}`
	req := httptest.NewRequest(http.MethodPost, analyzeSpanPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp analysisResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "This span handles test-operation.", resp.Analysis)
	assert.NotEmpty(t, resp.SessionID)
}

func TestAnalyzeSpan_MissingFields(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	body := `{"trace_id":"0123456789abcdef0123456789abcdef"}`
	req := httptest.NewRequest(http.MethodPost, analyzeSpanPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAnalyzeSpan_InvalidTraceID(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	body := `{"trace_id":"badid","span_id":"0123456789abcdef"}`
	req := httptest.NewRequest(http.MethodPost, analyzeSpanPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAnalyzeSpan_SpanNotFound(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	// Trace exists but span ID doesn't match.
	body := `{"trace_id":"0123456789abcdef0123456789abcdef","span_id":"ffffffffffffffff"}`
	req := httptest.NewRequest(http.MethodPost, analyzeSpanPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestAnalyzeSpan_InvalidJSON(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	req := httptest.NewRequest(http.MethodPost, analyzeSpanPath, bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAnalyzeTrace_Success(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "This trace shows a request.")

	body := `{"trace_id":"0123456789abcdef0123456789abcdef"}`
	req := httptest.NewRequest(http.MethodPost, analyzeTracePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp analysisResponse
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "This trace shows a request.", resp.Analysis)
	assert.NotEmpty(t, resp.SessionID)
}

func TestAnalyzeTrace_MissingTraceID(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, analyzeTracePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAnalyzeTrace_TraceNotFound(t *testing.T) {
	emptyTraces := ptrace.NewTraces()
	h := newTestAnalysisHandler(t, emptyTraces, "")

	body := `{"trace_id":"0123456789abcdef0123456789abcdef"}`
	req := httptest.NewRequest(http.MethodPost, analyzeTracePath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFollowUp_Success(t *testing.T) {
	traces := buildTestTrace()
	traceReader := &tracestoremocks.Reader{}
	traceReader.On("GetTraces", mock.Anything, mock.Anything).
		Return(tracesIter(traces)).Maybe()
	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	model := &mockModel{response: "The timeout was caused by a slow DB."}
	sm := NewSessionManager(time.Minute)
	defer sm.Close()
	cfg := Config{Enabled: true, Temperature: 0.1, MaxTokens: 256}
	analyzer := NewAnalyzer(model, cfg, sm, zap.NewNop())
	h := NewAnalysisHandler(qs, analyzer, zap.NewNop())

	// Create a session with prior context.
	session, err := sm.Create()
	require.NoError(t, err)
	sm.AddMessage(session.ID, RoleSystem, "system prompt")
	sm.AddMessage(session.ID, RoleUser, "Explain this span")
	sm.AddMessage(session.ID, RoleAssistant, "This span processes payments.")

	body, _ := json.Marshal(followUpRequest{SessionID: session.ID, Question: "Why the timeout?"})
	req := httptest.NewRequest(http.MethodPost, followUpPath, bytes.NewBuffer(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var resp analysisResponse
	err = json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "The timeout was caused by a slow DB.", resp.Analysis)
	assert.Equal(t, session.ID, resp.SessionID)
}

func TestFollowUp_MissingFields(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	body := `{"session_id":"","question":""}`
	req := httptest.NewRequest(http.MethodPost, followUpPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFollowUp_SessionNotFound(t *testing.T) {
	h := newTestAnalysisHandler(t, buildTestTrace(), "")

	body := `{"session_id":"nonexistent","question":"why?"}`
	req := httptest.NewRequest(http.MethodPost, followUpPath, bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	h.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// (end of tests)
