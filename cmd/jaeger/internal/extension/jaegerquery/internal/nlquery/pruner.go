// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/jaegertracing/jaeger/internal/jptrace"
)

// PrunedSpan is a lightweight representation of a span, containing only
// the fields relevant for LLM analysis. This dramatically reduces token
// usage compared to sending raw OTLP protobuf or full JSON.
//
// Design rationale:
//   - Small models (0.5B–3B) have tight context windows (2K–8K tokens).
//   - Raw trace data includes binary IDs, timestamps as nanoseconds,
//     internal OTEL fields — none of which help the model understand
//     what happened in the request.
//   - We keep: service name, operation, duration, status, key attributes,
//     and event/log summaries — the essentials for root cause analysis.
type PrunedSpan struct {
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	Service      string            `json:"service"`
	Operation    string            `json:"operation"`
	Duration     string            `json:"duration"`
	Status       string            `json:"status"`
	StatusMsg    string            `json:"status_message,omitempty"`
	Kind         string            `json:"kind"`
	Attributes   map[string]string `json:"attributes,omitempty"`
	Events       []PrunedEvent     `json:"events,omitempty"`
}

// PrunedEvent is a lightweight representation of a span event/log.
type PrunedEvent struct {
	Name       string            `json:"name"`
	Time       string            `json:"time,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// PrunedTrace is a lightweight representation of an entire trace.
type PrunedTrace struct {
	TraceID   string       `json:"trace_id"`
	SpanCount int          `json:"span_count"`
	Services  []string     `json:"services"`
	RootSpan  string       `json:"root_span,omitempty"`
	Duration  string       `json:"total_duration,omitempty"`
	Spans     []PrunedSpan `json:"spans"`
}

// maxAttributesPerSpan limits the number of attributes sent to the model.
const maxAttributesPerSpan = 15

// PruneSpan extracts the essential information from an OTEL span.
// The resource parameter provides service-level attributes (service.name).
func PruneSpan(span ptrace.Span, resource ptrace.ResourceSpans) PrunedSpan {
	serviceName := extractServiceName(resource)
	duration := span.EndTimestamp().AsTime().Sub(span.StartTimestamp().AsTime())

	ps := PrunedSpan{
		SpanID:       span.SpanID().String(),
		ParentSpanID: parentSpanIDString(span),
		Service:      serviceName,
		Operation:    span.Name(),
		Duration:     formatDuration(duration),
		Status:       statusCodeString(span.Status().Code()),
		StatusMsg:    span.Status().Message(),
		Kind:         spanKindString(span.Kind()),
		Attributes:   pruneAttributes(span.Attributes()),
		Events:       pruneEvents(span.Events()),
	}
	return ps
}

// PruneTrace extracts a lightweight summary of an entire trace.
// It iterates all spans, prunes each, and computes trace-level metadata.
func PruneTrace(traces ptrace.Traces) PrunedTrace {
	pt := PrunedTrace{
		Spans: make([]PrunedSpan, 0),
	}

	serviceSet := make(map[string]struct{})
	var earliestStart, latestEnd time.Time

	for pos, span := range jptrace.SpanIter(traces) {
		// Set trace ID from first span.
		if pt.TraceID == "" {
			pt.TraceID = span.TraceID().String()
		}

		ps := PruneSpan(span, pos.Resource)
		pt.Spans = append(pt.Spans, ps)
		serviceSet[ps.Service] = struct{}{}

		// Track root span (empty parent).
		if span.ParentSpanID().IsEmpty() {
			pt.RootSpan = ps.Operation
		}

		// Track overall trace duration.
		start := span.StartTimestamp().AsTime()
		end := span.EndTimestamp().AsTime()
		if earliestStart.IsZero() || start.Before(earliestStart) {
			earliestStart = start
		}
		if end.After(latestEnd) {
			latestEnd = end
		}
	}

	pt.SpanCount = len(pt.Spans)
	if !earliestStart.IsZero() && !latestEnd.IsZero() {
		pt.Duration = formatDuration(latestEnd.Sub(earliestStart))
	}
	for svc := range serviceSet {
		pt.Services = append(pt.Services, svc)
	}

	return pt
}

// FindSpanInTrace locates a specific span by its SpanID within a trace.
// Returns the span and its resource, or false if not found.
func FindSpanInTrace(traces ptrace.Traces, spanID pcommon.SpanID) (ptrace.Span, ptrace.ResourceSpans, bool) {
	for pos, span := range jptrace.SpanIter(traces) {
		if span.SpanID() == spanID {
			return span, pos.Resource, true
		}
	}
	return ptrace.NewSpan(), ptrace.NewResourceSpans(), false
}

func extractServiceName(resource ptrace.ResourceSpans) string {
	val, ok := resource.Resource().Attributes().Get("service.name")
	if ok {
		return val.AsString()
	}
	return "unknown"
}

func parentSpanIDString(span ptrace.Span) string {
	if span.ParentSpanID().IsEmpty() {
		return ""
	}
	return span.ParentSpanID().String()
}

func statusCodeString(code ptrace.StatusCode) string {
	switch code {
	case ptrace.StatusCodeOk:
		return "OK"
	case ptrace.StatusCodeError:
		return "ERROR"
	default:
		return "UNSET"
	}
}

func spanKindString(kind ptrace.SpanKind) string {
	switch kind {
	case ptrace.SpanKindClient:
		return "CLIENT"
	case ptrace.SpanKindServer:
		return "SERVER"
	case ptrace.SpanKindProducer:
		return "PRODUCER"
	case ptrace.SpanKindConsumer:
		return "CONSUMER"
	case ptrace.SpanKindInternal:
		return "INTERNAL"
	default:
		return "UNSPECIFIED"
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%dus", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.2fm", d.Minutes())
}

func pruneAttributes(attrs pcommon.Map) map[string]string {
	if attrs.Len() == 0 {
		return nil
	}
	result := make(map[string]string)
	count := 0
	attrs.Range(func(k string, v pcommon.Value) bool {
		if count >= maxAttributesPerSpan {
			return false
		}
		result[k] = v.AsString()
		count++
		return true
	})
	return result
}

func pruneEvents(events ptrace.SpanEventSlice) []PrunedEvent {
	if events.Len() == 0 {
		return nil
	}
	result := make([]PrunedEvent, 0, events.Len())
	for i := 0; i < events.Len(); i++ {
		ev := events.At(i)
		pe := PrunedEvent{
			Name: ev.Name(),
		}
		if !ev.Timestamp().AsTime().IsZero() {
			pe.Time = ev.Timestamp().AsTime().Format(time.RFC3339)
		}
		attrs := pruneAttributes(ev.Attributes())
		if len(attrs) > 0 {
			pe.Attributes = attrs
		}
		result = append(result, pe)
	}
	return result
}

// FormatPrunedSpanForLLM converts a PrunedSpan into a human-readable
// text block suitable for inclusion in an LLM prompt.
func FormatPrunedSpanForLLM(ps PrunedSpan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Span: %s\n", ps.Operation)
	fmt.Fprintf(&b, "  Service: %s\n", ps.Service)
	fmt.Fprintf(&b, "  Duration: %s\n", ps.Duration)
	fmt.Fprintf(&b, "  Status: %s", ps.Status)
	if ps.StatusMsg != "" {
		fmt.Fprintf(&b, " (%s)", ps.StatusMsg)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Kind: %s\n", ps.Kind)
	if ps.ParentSpanID != "" {
		fmt.Fprintf(&b, "  Parent: %s\n", ps.ParentSpanID)
	}
	if len(ps.Attributes) > 0 {
		b.WriteString("  Attributes:\n")
		for k, v := range ps.Attributes {
			fmt.Fprintf(&b, "    %s: %s\n", k, v)
		}
	}
	if len(ps.Events) > 0 {
		b.WriteString("  Events:\n")
		for _, ev := range ps.Events {
			fmt.Fprintf(&b, "    - %s", ev.Name)
			if ev.Time != "" {
				fmt.Fprintf(&b, " @ %s", ev.Time)
			}
			b.WriteString("\n")
			for k, v := range ev.Attributes {
				fmt.Fprintf(&b, "      %s: %s\n", k, v)
			}
		}
	}
	return b.String()
}

// FormatPrunedTraceForLLM converts a PrunedTrace into a human-readable
// text block for LLM prompt inclusion.
func FormatPrunedTraceForLLM(pt PrunedTrace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trace ID: %s\n", pt.TraceID)
	fmt.Fprintf(&b, "Total Spans: %d\n", pt.SpanCount)
	fmt.Fprintf(&b, "Services: %s\n", strings.Join(pt.Services, ", "))
	if pt.RootSpan != "" {
		fmt.Fprintf(&b, "Root Operation: %s\n", pt.RootSpan)
	}
	if pt.Duration != "" {
		fmt.Fprintf(&b, "Total Duration: %s\n", pt.Duration)
	}
	b.WriteString("\n--- Spans ---\n")
	for i := range pt.Spans {
		b.WriteString(FormatPrunedSpanForLLM(pt.Spans[i]))
		b.WriteString("\n")
	}
	return b.String()
}
