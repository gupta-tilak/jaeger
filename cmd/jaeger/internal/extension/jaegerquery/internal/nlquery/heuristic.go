// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"regexp"
	"strings"
)

// HeuristicExtractor extracts trace search parameters from natural language
// using deterministic regex and keyword matching. No AI or LLM is involved.
//
// This extractor demonstrates that the Extractor interface can be satisfied
// by a purely rule-based implementation, validating the architecture before
// any model integration. It also serves as a useful fallback when no local
// model is available.
//
// Supported patterns:
//   - Service names: "from <service>", "in <service>", "<service>-service"
//   - HTTP status codes: "500 errors", "status 404", "HTTP 503"
//   - Durations: "more than 2s", "slower than 500ms", "taking over 1s",
//     "faster than 100ms", "under 200ms", "less than 3s"
//   - Operations: HTTP methods followed by paths ("GET /api/...", "POST /checkout")
//
// Extension point: additional patterns can be added without changing the
// interface contract or any callers.
type HeuristicExtractor struct{}

var _ Extractor = (*HeuristicExtractor)(nil)

// Precompiled patterns for deterministic extraction.
// Each pattern targets a specific slot in SearchParams.
var (
	// serviceFromPattern matches "from <service-name>" where service names
	// are typically lowercase with hyphens (e.g., "payment-service").
	serviceFromPattern = regexp.MustCompile(`(?i)\bfrom\s+([a-z][a-z0-9_-]*(?:-service)?)\b`)

	// serviceInPattern matches "in <service-name>".
	serviceInPattern = regexp.MustCompile(`(?i)\bin\s+([a-z][a-z0-9_-]*-service)\b`)

	// httpStatusPattern matches HTTP status codes in various forms:
	// "500 errors", "status code 404", "HTTP 503", "status 502".
	httpStatusPattern = regexp.MustCompile(`(?i)\b(?:(?:http\s+)?status(?:\s+code)?\s+(\d{3}))|(?:(\d{3})\s+errors?)\b`)

	// minDurationPattern matches minimum duration constraints:
	// "more than 2s", "slower than 500ms", "taking over 1s", "longer than 100ms",
	// "at least 2s", "> 500ms", "more than 2 seconds".
	minDurationPattern = regexp.MustCompile(`(?i)(?:(?:more|slower|longer|over)\s+than|taking\s+over|at\s+least|>\s*)\s*(\d+(?:\.\d+)?)\s*(milliseconds|seconds|minutes|hours|ms|s|m|h|us|ns)`)

	// maxDurationPattern matches maximum duration constraints:
	// "less than 3s", "faster than 100ms", "under 200ms", "within 1s",
	// "at most 5s", "< 500ms", "less than 3 seconds".
	maxDurationPattern = regexp.MustCompile(`(?i)(?:(?:less|faster|shorter)\s+than|under|within|at\s+most|<\s*)\s*(\d+(?:\.\d+)?)\s*(milliseconds|seconds|minutes|hours|ms|s|m|h|us|ns)`)

	// operationPattern matches HTTP method + path combinations:
	// "GET /api/users", "POST /checkout".
	operationPattern = regexp.MustCompile(`\b(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS)\s+(/[/a-zA-Z0-9_{}.*-]+)`)
)

// Extract parses the input string using regex patterns and returns
// the extracted SearchParams. Each field is extracted independently;
// missing fields remain at their zero values.
//
// This function is deterministic: the same input always produces the
// same output. No randomness, no model inference, no external calls.
func (*HeuristicExtractor) Extract(_ context.Context, input string) (SearchParams, error) {
	var params SearchParams

	params.Service = extractService(input)
	params.Operation = extractOperation(input)
	params.Tags = extractTags(input)
	params.MinDuration = extractDuration(input, minDurationPattern)
	params.MaxDuration = extractDuration(input, maxDurationPattern)

	return params, nil
}

// extractService attempts to find a service name in the input text.
func extractService(input string) string {
	// Try "from <service>" first (most explicit).
	if m := serviceFromPattern.FindStringSubmatch(input); len(m) > 1 {
		return m[1]
	}
	// Fall back to "in <service>-service".
	if m := serviceInPattern.FindStringSubmatch(input); len(m) > 1 {
		return m[1]
	}
	return ""
}

// extractOperation looks for HTTP method + path patterns.
func extractOperation(input string) string {
	if m := operationPattern.FindStringSubmatch(input); len(m) > 2 {
		return m[1] + " " + m[2]
	}
	return ""
}

// extractTags extracts structured attributes like HTTP status codes.
func extractTags(input string) map[string]string {
	tags := make(map[string]string)

	if code := extractHTTPStatusCode(input); code != "" {
		tags["http.status_code"] = code
	}

	if len(tags) == 0 {
		return nil
	}
	return tags
}

// extractHTTPStatusCode finds a 3-digit HTTP status code in the input.
func extractHTTPStatusCode(input string) string {
	matches := httpStatusPattern.FindStringSubmatch(input)
	if len(matches) > 2 {
		// Group 1: "status code 500" or "HTTP 500"
		if matches[1] != "" {
			return matches[1]
		}
		// Group 2: "500 errors"
		if matches[2] != "" {
			return matches[2]
		}
	}
	return ""
}

// extractDuration finds a duration value matching the given pattern.
func extractDuration(input string, pattern *regexp.Regexp) string {
	if m := pattern.FindStringSubmatch(input); len(m) > 2 {
		unit := normalizeDurationUnit(m[2])
		return strings.TrimSpace(m[1]) + unit
	}
	return ""
}

// normalizeDurationUnit converts full unit names to Go duration suffixes.
func normalizeDurationUnit(unit string) string {
	switch strings.ToLower(unit) {
	case "milliseconds":
		return "ms"
	case "seconds":
		return "s"
	case "minutes":
		return "m"
	case "hours":
		return "h"
	default:
		return unit
	}
}
