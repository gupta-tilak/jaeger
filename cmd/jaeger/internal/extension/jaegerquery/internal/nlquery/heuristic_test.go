// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeuristicExtractor_Extract(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantService string
		wantOp      string
		wantTags    map[string]string
		wantMinDur  string
		wantMaxDur  string
	}{
		{
			name:        "canonical example from requirements",
			input:       "Show me 500 errors from payment-service taking more than 2 seconds",
			wantService: "payment-service",
			wantTags:    map[string]string{"http.status_code": "500"},
			wantMinDur:  "2s",
		},
		{
			name:        "canonical example with duration shorthand",
			input:       "Show me 500 errors from payment-service taking over 2s",
			wantService: "payment-service",
			wantTags:    map[string]string{"http.status_code": "500"},
			wantMinDur:  "2s",
		},
		{
			name:        "service from pattern",
			input:       "show traces from order-service",
			wantService: "order-service",
		},
		{
			name:        "service in pattern",
			input:       "find errors in payment-service",
			wantService: "payment-service",
		},
		{
			name:     "HTTP status code - NNN errors",
			input:    "show 404 errors",
			wantTags: map[string]string{"http.status_code": "404"},
		},
		{
			name:     "HTTP status code - status NNN",
			input:    "traces with status code 502",
			wantTags: map[string]string{"http.status_code": "502"},
		},
		{
			name:     "HTTP status code - HTTP NNN",
			input:    "HTTP status 503",
			wantTags: map[string]string{"http.status_code": "503"},
		},
		{
			name:       "min duration - more than",
			input:      "requests more than 500ms",
			wantMinDur: "500ms",
		},
		{
			name:       "min duration - slower than",
			input:      "traces slower than 3s",
			wantMinDur: "3s",
		},
		{
			name:       "max duration - less than",
			input:      "traces less than 100ms",
			wantMaxDur: "100ms",
		},
		{
			name:       "max duration - faster than",
			input:      "requests faster than 50ms",
			wantMaxDur: "50ms",
		},
		{
			name:       "max duration - under",
			input:      "spans under 200ms",
			wantMaxDur: "200ms",
		},
		{
			name:   "operation - GET path",
			input:  "show traces for GET /api/users",
			wantOp: "GET /api/users",
		},
		{
			name:   "operation - POST path",
			input:  "find POST /checkout requests",
			wantOp: "POST /checkout",
		},
		{
			name:        "combined extraction",
			input:       "find 500 errors from frontend-service for GET /api/checkout slower than 1s",
			wantService: "frontend-service",
			wantOp:      "GET /api/checkout",
			wantTags:    map[string]string{"http.status_code": "500"},
			wantMinDur:  "1s",
		},
		{
			name: "empty input",
		},
		{
			name:  "no recognizable patterns",
			input: "hello world",
		},
		{
			name:        "service name without -service suffix via from",
			input:       "traces from redis",
			wantService: "redis",
		},
	}

	extractor := &HeuristicExtractor{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := extractor.Extract(context.Background(), tt.input)
			require.NoError(t, err)

			assert.Equal(t, tt.wantService, params.Service, "service")
			assert.Equal(t, tt.wantOp, params.Operation, "operation")
			assert.Equal(t, tt.wantMinDur, params.MinDuration, "minDuration")
			assert.Equal(t, tt.wantMaxDur, params.MaxDuration, "maxDuration")

			if tt.wantTags != nil {
				require.NotNil(t, params.Tags)
				assert.Equal(t, tt.wantTags, params.Tags, "tags")
			} else {
				assert.Nil(t, params.Tags, "tags should be nil when no tags extracted")
			}
		})
	}
}

func TestHeuristicExtractor_ImplementsInterface(t *testing.T) {
	var e Extractor = &HeuristicExtractor{}
	_, err := e.Extract(context.Background(), "test")
	require.NoError(t, err)
}

func TestHeuristicExtractor_Determinism(t *testing.T) {
	// The same input must always produce the same output.
	// This is a core requirement: no randomness, no model inference.
	extractor := &HeuristicExtractor{}
	input := "show 500 errors from payment-service slower than 2s"

	first, err := extractor.Extract(context.Background(), input)
	require.NoError(t, err)

	for range 10 {
		result, err := extractor.Extract(context.Background(), input)
		require.NoError(t, err)
		assert.Equal(t, first, result, "heuristic extractor must be deterministic")
	}
}
