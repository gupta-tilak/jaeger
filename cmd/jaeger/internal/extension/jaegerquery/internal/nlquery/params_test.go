// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchParams_ToTraceQueryParams(t *testing.T) {
	tests := []struct {
		name        string
		params      SearchParams
		wantService string
		wantOp      string
		wantTags    map[string]string
		wantMinDur  string
		wantMaxDur  string
		wantDepth   int
		wantErr     string
	}{
		{
			name:   "empty params",
			params: SearchParams{},
		},
		{
			name: "full params",
			params: SearchParams{
				Service:     "payment-service",
				Operation:   "GET /api/checkout",
				Tags:        map[string]string{"http.status_code": "500"},
				MinDuration: "2s",
				MaxDuration: "10s",
				SearchDepth: 20,
			},
			wantService: "payment-service",
			wantOp:      "GET /api/checkout",
			wantTags:    map[string]string{"http.status_code": "500"},
			wantMinDur:  "2s",
			wantMaxDur:  "10s",
			wantDepth:   20,
		},
		{
			name:        "service only",
			params:      SearchParams{Service: "frontend"},
			wantService: "frontend",
		},
		{
			name:    "invalid minDuration",
			params:  SearchParams{MinDuration: "not-a-duration"},
			wantErr: "invalid minDuration",
		},
		{
			name:    "invalid maxDuration",
			params:  SearchParams{MaxDuration: "bad"},
			wantErr: "invalid maxDuration",
		},
		{
			name: "multiple tags",
			params: SearchParams{
				Service: "order-service",
				Tags: map[string]string{
					"http.status_code": "500",
					"http.method":      "POST",
				},
			},
			wantService: "order-service",
			wantTags: map[string]string{
				"http.status_code": "500",
				"http.method":      "POST",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := tt.params.ToTraceQueryParams()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)

			assert.Equal(t, tt.wantService, result.ServiceName)
			assert.Equal(t, tt.wantOp, result.OperationName)
			assert.Equal(t, tt.wantDepth, result.SearchDepth)

			if tt.wantMinDur != "" {
				assert.NotZero(t, result.DurationMin)
			}
			if tt.wantMaxDur != "" {
				assert.NotZero(t, result.DurationMax)
			}

			if tt.wantTags != nil {
				for k, v := range tt.wantTags {
					val, ok := result.Attributes.Get(k)
					assert.True(t, ok, "expected attribute %q", k)
					assert.Equal(t, v, val.Str())
				}
			}
		})
	}
}

func TestSearchParams_JSONRoundTrip(t *testing.T) {
	// Verify that SearchParams can be marshaled to JSON and back,
	// which validates that json.Unmarshal works as a safety firewall.
	original := SearchParams{
		Service:     "payment-service",
		Operation:   "POST /pay",
		Tags:        map[string]string{"http.status_code": "500"},
		MinDuration: "2s",
		MaxDuration: "10s",
		SearchDepth: 50,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored SearchParams
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original, restored)
}

func TestSearchParams_JSONSafetyFirewall(t *testing.T) {
	// Simulate an LLM response with extra/hallucinated fields.
	// json.Unmarshal must silently drop unknown fields, ensuring only
	// known SearchParams fields survive.
	jsonWithExtra := `{
		"service": "payment-service",
		"tags": {"http.status_code": "500"},
		"minDuration": "2s",
		"hallucinated_field": "should be dropped",
		"confidence": 0.95,
		"reasoning": "I think this is what you want"
	}`

	var params SearchParams
	err := json.Unmarshal([]byte(jsonWithExtra), &params)
	require.NoError(t, err)

	assert.Equal(t, "payment-service", params.Service)
	assert.Equal(t, "2s", params.MinDuration)
	assert.Equal(t, map[string]string{"http.status_code": "500"}, params.Tags)
	// Hallucinated fields are silently dropped — this is the safety firewall.
	assert.Empty(t, params.Operation)
	assert.Empty(t, params.MaxDuration)
	assert.Zero(t, params.SearchDepth)
}
