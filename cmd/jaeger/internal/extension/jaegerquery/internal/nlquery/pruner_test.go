// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// newTestTrace builds a minimal trace with one resource span and one span.
func newTestTrace(serviceName, operationName string) (ptrace.Traces, ptrace.Span, ptrace.ResourceSpans) {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", serviceName)
	scope := rs.ScopeSpans().AppendEmpty()
	span := scope.Spans().AppendEmpty()
	span.SetName(operationName)
	span.SetTraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	span.SetSpanID([8]byte{1, 2, 3, 4, 5, 6, 7, 8})
	span.SetKind(ptrace.SpanKindServer)
	span.Status().SetCode(ptrace.StatusCodeOk)

	now := time.Now()
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(now))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(150 * time.Millisecond)))

	return traces, span, rs
}

func TestPruneSpan_BasicFields(t *testing.T) {
	_, span, rs := newTestTrace("order-service", "POST /api/orders")

	result := PruneSpan(span, rs)

	assert.Equal(t, "order-service", result.Service)
	assert.Equal(t, "POST /api/orders", result.Operation)
	assert.Equal(t, "OK", result.Status)
	assert.Equal(t, "SERVER", result.Kind)
	assert.Equal(t, "150ms", result.Duration)
	assert.NotEmpty(t, result.SpanID)
	assert.Empty(t, result.ParentSpanID)
}

func TestPruneSpan_WithParentSpan(t *testing.T) {
	_, span, rs := newTestTrace("my-service", "handler")
	span.SetParentSpanID([8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0x11, 0x22, 0x33, 0x44})

	result := PruneSpan(span, rs)
	assert.NotEmpty(t, result.ParentSpanID)
}

func TestPruneSpan_ErrorStatus(t *testing.T) {
	_, span, rs := newTestTrace("failing-service", "GET /fail")
	span.Status().SetCode(ptrace.StatusCodeError)
	span.Status().SetMessage("connection refused")

	result := PruneSpan(span, rs)
	assert.Equal(t, "ERROR", result.Status)
	assert.Equal(t, "connection refused", result.StatusMsg)
}

func TestPruneSpan_Attributes(t *testing.T) {
	_, span, rs := newTestTrace("web-service", "GET /users")
	span.Attributes().PutStr("http.method", "GET")
	span.Attributes().PutStr("http.url", "http://example.com/users")
	span.Attributes().PutInt("http.status_code", 200)

	result := PruneSpan(span, rs)
	require.NotNil(t, result.Attributes)
	assert.Equal(t, "GET", result.Attributes["http.method"])
	assert.Equal(t, "200", result.Attributes["http.status_code"])
}

func TestPruneSpan_AttributeLimit(t *testing.T) {
	_, span, rs := newTestTrace("service", "op")
	for i := 0; i < maxAttributesPerSpan+5; i++ {
		span.Attributes().PutStr("key"+string(rune('A'+i)), "value")
	}

	result := PruneSpan(span, rs)
	assert.LessOrEqual(t, len(result.Attributes), maxAttributesPerSpan)
}

func TestPruneSpan_Events(t *testing.T) {
	_, span, rs := newTestTrace("service", "op")
	event := span.Events().AppendEmpty()
	event.SetName("exception")
	event.Attributes().PutStr("exception.type", "NullPointerException")

	result := PruneSpan(span, rs)
	require.Len(t, result.Events, 1)
	assert.Equal(t, "exception", result.Events[0].Name)
	assert.Equal(t, "NullPointerException", result.Events[0].Attributes["exception.type"])
}

func TestPruneSpan_NoServiceName(t *testing.T) {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	// Don't set service.name
	scope := rs.ScopeSpans().AppendEmpty()
	span := scope.Spans().AppendEmpty()
	span.SetName("op")

	result := PruneSpan(span, rs)
	assert.Equal(t, "unknown", result.Service)
}

func TestPruneTrace_BasicFields(t *testing.T) {
	traces, _, _ := newTestTrace("order-service", "POST /api/orders")

	result := PruneTrace(traces)

	assert.NotEmpty(t, result.TraceID)
	assert.Equal(t, 1, result.SpanCount)
	assert.Contains(t, result.Services, "order-service")
	assert.Equal(t, "POST /api/orders", result.RootSpan)
	assert.NotEmpty(t, result.Duration)
	require.Len(t, result.Spans, 1)
}

func TestPruneTrace_MultipleSpans(t *testing.T) {
	traces := ptrace.NewTraces()
	rs := traces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().PutStr("service.name", "service-a")
	scope := rs.ScopeSpans().AppendEmpty()

	// Root span.
	root := scope.Spans().AppendEmpty()
	root.SetName("root-op")
	root.SetTraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	root.SetSpanID([8]byte{1})
	root.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now()))
	root.SetEndTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(200 * time.Millisecond)))

	// Child span.
	child := scope.Spans().AppendEmpty()
	child.SetName("child-op")
	child.SetTraceID([16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16})
	child.SetSpanID([8]byte{2})
	child.SetParentSpanID([8]byte{1})
	child.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(10 * time.Millisecond)))
	child.SetEndTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(100 * time.Millisecond)))

	result := PruneTrace(traces)
	assert.Equal(t, 2, result.SpanCount)
	assert.Equal(t, "root-op", result.RootSpan)
}

func TestPruneTrace_MultipleServices(t *testing.T) {
	traces := ptrace.NewTraces()

	rs1 := traces.ResourceSpans().AppendEmpty()
	rs1.Resource().Attributes().PutStr("service.name", "frontend")
	scope1 := rs1.ScopeSpans().AppendEmpty()
	s1 := scope1.Spans().AppendEmpty()
	s1.SetName("GET /page")
	s1.SetTraceID([16]byte{1})
	s1.SetSpanID([8]byte{1})
	s1.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now()))
	s1.SetEndTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(time.Second)))

	rs2 := traces.ResourceSpans().AppendEmpty()
	rs2.Resource().Attributes().PutStr("service.name", "backend")
	scope2 := rs2.ScopeSpans().AppendEmpty()
	s2 := scope2.Spans().AppendEmpty()
	s2.SetName("DB query")
	s2.SetTraceID([16]byte{1})
	s2.SetSpanID([8]byte{2})
	s2.SetParentSpanID([8]byte{1})
	s2.SetStartTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(50 * time.Millisecond)))
	s2.SetEndTimestamp(pcommon.NewTimestampFromTime(time.Now().Add(800 * time.Millisecond)))

	result := PruneTrace(traces)
	assert.Equal(t, 2, result.SpanCount)
	assert.Contains(t, result.Services, "frontend")
	assert.Contains(t, result.Services, "backend")
}

func TestFindSpanInTrace_Found(t *testing.T) {
	traces, _, _ := newTestTrace("svc", "op")
	targetSpanID := pcommon.SpanID([8]byte{1, 2, 3, 4, 5, 6, 7, 8})

	span, resource, found := FindSpanInTrace(traces, targetSpanID)
	assert.True(t, found)
	assert.Equal(t, "op", span.Name())

	svcName, ok := resource.Resource().Attributes().Get("service.name")
	assert.True(t, ok)
	assert.Equal(t, "svc", svcName.AsString())
}

func TestFindSpanInTrace_NotFound(t *testing.T) {
	traces, _, _ := newTestTrace("svc", "op")
	missingSpanID := pcommon.SpanID([8]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})

	_, _, found := FindSpanInTrace(traces, missingSpanID)
	assert.False(t, found)
}

func TestFormatPrunedSpanForLLM(t *testing.T) {
	ps := PrunedSpan{
		SpanID:    "abc123",
		Service:   "payment-service",
		Operation: "ProcessPayment",
		Duration:  "250ms",
		Status:    "ERROR",
		StatusMsg: "insufficient funds",
		Kind:      "SERVER",
		Attributes: map[string]string{
			"payment.amount": "99.99",
		},
		Events: []PrunedEvent{
			{Name: "exception", Attributes: map[string]string{"exception.type": "PaymentError"}},
		},
	}

	text := FormatPrunedSpanForLLM(ps)

	assert.Contains(t, text, "ProcessPayment")
	assert.Contains(t, text, "payment-service")
	assert.Contains(t, text, "250ms")
	assert.Contains(t, text, "ERROR")
	assert.Contains(t, text, "insufficient funds")
	assert.Contains(t, text, "payment.amount: 99.99")
	assert.Contains(t, text, "exception")
	assert.Contains(t, text, "PaymentError")
}

func TestFormatPrunedTraceForLLM(t *testing.T) {
	pt := PrunedTrace{
		TraceID:   "abc123def456",
		SpanCount: 3,
		Services:  []string{"frontend", "backend"},
		RootSpan:  "GET /",
		Duration:  "1.20s",
		Spans: []PrunedSpan{
			{Service: "frontend", Operation: "GET /", Duration: "1.20s", Status: "OK", Kind: "SERVER"},
			{Service: "backend", Operation: "query", Duration: "500ms", Status: "OK", Kind: "CLIENT"},
		},
	}

	text := FormatPrunedTraceForLLM(pt)
	assert.Contains(t, text, "abc123def456")
	assert.Contains(t, text, "Total Spans: 3")
	assert.Contains(t, text, "frontend")
	assert.Contains(t, text, "backend")
	assert.Contains(t, text, "GET /")
}

func TestStatusCodeString(t *testing.T) {
	assert.Equal(t, "OK", statusCodeString(ptrace.StatusCodeOk))
	assert.Equal(t, "ERROR", statusCodeString(ptrace.StatusCodeError))
	assert.Equal(t, "UNSET", statusCodeString(ptrace.StatusCodeUnset))
}

func TestSpanKindString(t *testing.T) {
	tests := []struct {
		kind     ptrace.SpanKind
		expected string
	}{
		{ptrace.SpanKindClient, "CLIENT"},
		{ptrace.SpanKindServer, "SERVER"},
		{ptrace.SpanKindProducer, "PRODUCER"},
		{ptrace.SpanKindConsumer, "CONSUMER"},
		{ptrace.SpanKindInternal, "INTERNAL"},
		{ptrace.SpanKindUnspecified, "UNSPECIFIED"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, spanKindString(tc.kind))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{500 * time.Microsecond, "500us"},
		{150 * time.Millisecond, "150ms"},
		{2500 * time.Millisecond, "2.50s"},
		{90 * time.Second, "1.50m"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.expected, formatDuration(tc.d))
	}
}

func TestPruneAttributes_Empty(t *testing.T) {
	m := pcommon.NewMap()
	result := pruneAttributes(m)
	assert.Nil(t, result)
}

func TestPruneEvents_Empty(t *testing.T) {
	events := ptrace.NewSpanEventSlice()
	result := pruneEvents(events)
	assert.Nil(t, result)
}

func TestPruneEvents_WithTimestamp(t *testing.T) {
	events := ptrace.NewSpanEventSlice()
	ev := events.AppendEmpty()
	ev.SetName("log")
	ev.SetTimestamp(pcommon.NewTimestampFromTime(time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)))

	result := pruneEvents(events)
	require.Len(t, result, 1)
	assert.Equal(t, "log", result[0].Name)
	assert.Contains(t, result[0].Time, "2025")
}
