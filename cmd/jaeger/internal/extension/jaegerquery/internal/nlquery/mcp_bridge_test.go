// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/jaegertracing/jaeger/cmd/jaeger/internal/extension/jaegerquery/querysvc"
	depsmocks "github.com/jaegertracing/jaeger/internal/storage/v2/api/depstore/mocks"
	tracestoremocks "github.com/jaegertracing/jaeger/internal/storage/v2/api/tracestore/mocks"
)

// ---- mockToolCaller ----

type mockToolCaller struct {
	searchResult []TraceSearchResult
	searchErr    error
	services     []string
	servicesErr  error
}

var _ ToolCaller = (*mockToolCaller)(nil)

func (m *mockToolCaller) SearchTraces(context.Context, SearchParams) ([]TraceSearchResult, error) {
	return m.searchResult, m.searchErr
}

func (m *mockToolCaller) GetServices(context.Context) ([]string, error) {
	return m.services, m.servicesErr
}

// ---- QueryServiceToolCaller tests ----

func TestQueryServiceToolCaller_SearchTraces_Success(t *testing.T) {
	trace := buildTestTrace()
	traceReader := &tracestoremocks.Reader{}
	traceReader.On("FindTraces", mock.Anything, mock.Anything).
		Return(tracesIter(trace))

	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	tc := NewQueryServiceToolCaller(qs)

	results, err := tc.SearchTraces(context.Background(), SearchParams{
		Service: "test-service",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "test-service", results[0].RootService)
	assert.Equal(t, "test-operation", results[0].RootSpanName)
	assert.Equal(t, 1, results[0].SpanCount)
	assert.Equal(t, 1, results[0].ServiceCount)
	assert.False(t, results[0].HasErrors)
}

func TestQueryServiceToolCaller_SearchTraces_InvalidParams(t *testing.T) {
	qs := querysvc.NewQueryService(
		&tracestoremocks.Reader{},
		&depsmocks.Reader{},
		querysvc.QueryServiceOptions{},
	)
	tc := NewQueryServiceToolCaller(qs)

	_, err := tc.SearchTraces(context.Background(), SearchParams{
		MinDuration: "not-a-duration",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid search params")
}

func TestQueryServiceToolCaller_SearchTraces_EmptyResults(t *testing.T) {
	traceReader := &tracestoremocks.Reader{}
	traceReader.On("FindTraces", mock.Anything, mock.Anything).
		Return(emptyTracesIter())

	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	tc := NewQueryServiceToolCaller(qs)

	results, err := tc.SearchTraces(context.Background(), SearchParams{Service: "svc"})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestQueryServiceToolCaller_SearchTraces_WithErrors(t *testing.T) {
	// Build a trace with an error span.
	trace := ptrace.NewTraces()
	rs := trace.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "error-service")
	scope := rs.ScopeSpans().AppendEmpty()
	span := scope.Spans().AppendEmpty()
	span.SetName("failing-op")
	span.SetTraceID(testTraceID)
	span.SetSpanID(testSpanID)
	span.Status().SetCode(ptrace.StatusCodeError)
	span.Status().SetMessage("deadline exceeded")
	now := time.Now()
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(now))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(5 * time.Second)))

	traceReader := &tracestoremocks.Reader{}
	traceReader.On("FindTraces", mock.Anything, mock.Anything).
		Return(tracesIter(trace))

	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	tc := NewQueryServiceToolCaller(qs)

	results, err := tc.SearchTraces(context.Background(), SearchParams{Service: "error-service"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].HasErrors)
	assert.Equal(t, "error-service", results[0].RootService)
	assert.Equal(t, "failing-op", results[0].RootSpanName)
}

func TestQueryServiceToolCaller_SearchTraces_MultiService(t *testing.T) {
	// Build a trace with spans from multiple services.
	trace := ptrace.NewTraces()

	rs1 := trace.ResourceSpans().AppendEmpty()
	rs1.Resource().Attributes().PutStr("service.name", "frontend")
	scope1 := rs1.ScopeSpans().AppendEmpty()
	span1 := scope1.Spans().AppendEmpty()
	span1.SetName("GET /home")
	span1.SetTraceID(testTraceID)
	span1.SetSpanID(testSpanID)
	now := time.Now()
	span1.SetStartTimestamp(pcommon.NewTimestampFromTime(now))
	span1.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(200 * time.Millisecond)))

	rs2 := trace.ResourceSpans().AppendEmpty()
	rs2.Resource().Attributes().PutStr("service.name", "backend")
	scope2 := rs2.ScopeSpans().AppendEmpty()
	span2 := scope2.Spans().AppendEmpty()
	span2.SetName("query-db")
	span2.SetTraceID(testTraceID)
	span2.SetSpanID(pcommon.SpanID([8]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11}))
	span2.SetParentSpanID(testSpanID)
	span2.SetStartTimestamp(pcommon.NewTimestampFromTime(now.Add(10 * time.Millisecond)))
	span2.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(180 * time.Millisecond)))

	traceReader := &tracestoremocks.Reader{}
	traceReader.On("FindTraces", mock.Anything, mock.Anything).
		Return(tracesIter(trace))

	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	tc := NewQueryServiceToolCaller(qs)

	results, err := tc.SearchTraces(context.Background(), SearchParams{Service: "frontend"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 2, results[0].SpanCount)
	assert.Equal(t, 2, results[0].ServiceCount)
	assert.Equal(t, "frontend", results[0].RootService)
	assert.Equal(t, "GET /home", results[0].RootSpanName)
}

func TestQueryServiceToolCaller_GetServices(t *testing.T) {
	traceReader := &tracestoremocks.Reader{}
	traceReader.On("GetServices", mock.Anything).
		Return([]string{"svc-a", "svc-b", "svc-c"}, nil)

	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	tc := NewQueryServiceToolCaller(qs)

	services, err := tc.GetServices(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"svc-a", "svc-b", "svc-c"}, services)
}

func TestQueryServiceToolCaller_GetServices_Error(t *testing.T) {
	traceReader := &tracestoremocks.Reader{}
	traceReader.On("GetServices", mock.Anything).
		Return(nil, errors.New("storage unavailable"))

	qs := querysvc.NewQueryService(traceReader, &depsmocks.Reader{}, querysvc.QueryServiceOptions{})
	tc := NewQueryServiceToolCaller(qs)

	_, err := tc.GetServices(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage unavailable")
}

// ---- buildTraceSearchResult tests ----

func TestBuildTraceSearchResult_BasicTrace(t *testing.T) {
	trace := buildTestTrace()
	result := buildTraceSearchResult(trace)

	assert.Equal(t, testTraceID.String(), result.TraceID)
	assert.Equal(t, "test-service", result.RootService)
	assert.Equal(t, "test-operation", result.RootSpanName)
	assert.Equal(t, 1, result.SpanCount)
	assert.Equal(t, 1, result.ServiceCount)
	assert.False(t, result.HasErrors)
	assert.NotEmpty(t, result.StartTime)
	assert.Positive(t, result.DurationUs)
}

func TestBuildTraceSearchResult_EmptyTrace(t *testing.T) {
	trace := ptrace.NewTraces()
	result := buildTraceSearchResult(trace)

	assert.Equal(t, 0, result.SpanCount)
	assert.Equal(t, 0, result.ServiceCount)
	assert.Empty(t, result.StartTime)
}

// ---- formatSearchResultsForLLM tests ----

func TestFormatSearchResultsForLLM_Basic(t *testing.T) {
	params := SearchParams{Service: "frontend"}
	traces := []TraceSearchResult{
		{
			TraceID:      "abc123",
			RootService:  "frontend",
			RootSpanName: "GET /home",
			SpanCount:    5,
			ServiceCount: 2,
			DurationUs:   150000,
			HasErrors:    false,
		},
	}

	result := formatSearchResultsForLLM(params, traces)
	assert.Contains(t, result, `service="frontend"`)
	assert.Contains(t, result, "Found 1 traces")
	assert.Contains(t, result, "abc123")
	assert.Contains(t, result, "GET /home")
}

func TestFormatSearchResultsForLLM_WithOperation(t *testing.T) {
	params := SearchParams{Service: "payment", Operation: "POST /pay"}
	traces := []TraceSearchResult{}

	result := formatSearchResultsForLLM(params, traces)
	assert.Contains(t, result, `service="payment"`)
	assert.Contains(t, result, `operation="POST /pay"`)
	assert.Contains(t, result, "Found 0 traces")
}

// ---- ToMCPArgs tests ----

func TestToMCPArgs_AllFields(t *testing.T) {
	p := SearchParams{
		Service:     "payment-service",
		Operation:   "POST /pay",
		Tags:        map[string]string{"http.status_code": "500"},
		MinDuration: "2s",
		MaxDuration: "10s",
		SearchDepth: 50,
	}

	args := p.ToMCPArgs()
	assert.Equal(t, "payment-service", args["service_name"])
	assert.Equal(t, "POST /pay", args["span_name"])
	assert.Equal(t, "2s", args["duration_min"])
	assert.Equal(t, "10s", args["duration_max"])
	assert.Equal(t, 50, args["search_depth"])
	assert.Equal(t, map[string]string{"http.status_code": "500"}, args["attributes"])
}

func TestToMCPArgs_EmptyParams(t *testing.T) {
	p := SearchParams{}
	args := p.ToMCPArgs()
	assert.Empty(t, args)
}

func TestToMCPArgs_PartialFields(t *testing.T) {
	p := SearchParams{
		Service:     "frontend",
		MinDuration: "100ms",
	}

	args := p.ToMCPArgs()
	assert.Len(t, args, 2)
	assert.Equal(t, "frontend", args["service_name"])
	assert.Equal(t, "100ms", args["duration_min"])

	// Absent fields must not appear.
	_, hasOp := args["span_name"]
	assert.False(t, hasOp)
	_, hasMax := args["duration_max"]
	assert.False(t, hasMax)
}

// ---- ToolCaller interface compliance ----

func TestMockToolCaller_ImplementsInterface(t *testing.T) {
	var tc ToolCaller = &mockToolCaller{}
	assert.NotNil(t, tc)
}
