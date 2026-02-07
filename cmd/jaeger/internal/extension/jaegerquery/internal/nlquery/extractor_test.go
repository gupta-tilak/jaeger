// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStubExtractor_Extract(t *testing.T) {
	extractor := &StubExtractor{}
	params, err := extractor.Extract(context.Background(), "show me traces from payment-service")
	require.NoError(t, err)

	// StubExtractor always returns empty params, regardless of input.
	assert.Empty(t, params.Service)
	assert.Empty(t, params.Operation)
	assert.Nil(t, params.Tags)
	assert.Empty(t, params.MinDuration)
	assert.Empty(t, params.MaxDuration)
	assert.Zero(t, params.SearchDepth)
}

func TestStubExtractor_ImplementsInterface(t *testing.T) {
	// Compile-time check is in extractor.go via var _ Extractor = (*StubExtractor)(nil).
	// This test verifies at runtime that the contract holds.
	var e Extractor = &StubExtractor{}
	_, err := e.Extract(context.Background(), "")
	require.NoError(t, err)
}
