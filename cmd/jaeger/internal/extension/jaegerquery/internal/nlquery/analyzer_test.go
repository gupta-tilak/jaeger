// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

// mockModel is a test double for llms.Model.
type mockModel struct {
	response string
	err      error
}

func (m *mockModel) GenerateContent(
	_ context.Context,
	_ []llms.MessageContent,
	_ ...llms.CallOption,
) (*llms.ContentResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: m.response},
		},
	}, nil
}

func (m *mockModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return m.response, m.err
}

func newTestAnalyzer(t *testing.T, model llms.Model) *Analyzer {
	t.Helper()
	sm := NewSessionManager(time.Minute)
	t.Cleanup(sm.Close)
	cfg := Config{
		Enabled:     true,
		Temperature: 0.1,
		MaxTokens:   256,
	}
	return NewAnalyzer(model, cfg, sm, zap.NewNop())
}

func TestAnalyzer_AnalyzeSpan_Success(t *testing.T) {
	model := &mockModel{response: "This span processes a payment request."}
	analyzer := newTestAnalyzer(t, model)

	span := PrunedSpan{
		Service:   "payment-service",
		Operation: "ProcessPayment",
		Duration:  "250ms",
		Status:    "OK",
		Kind:      "SERVER",
	}

	analysis, sessionID, err := analyzer.AnalyzeSpan(context.Background(), span, "")
	require.NoError(t, err)
	assert.Equal(t, "This span processes a payment request.", analysis)
	assert.NotEmpty(t, sessionID, "should create a new session")
}

func TestAnalyzer_AnalyzeSpan_WithExistingSession(t *testing.T) {
	model := &mockModel{response: "Analysis response"}
	analyzer := newTestAnalyzer(t, model)

	// Create a session with existing context.
	session, err := analyzer.sessions.Create()
	require.NoError(t, err)
	analyzer.sessions.AddMessage(session.ID, RoleSystem, "system prompt")

	span := PrunedSpan{
		Service:   "svc",
		Operation: "op",
		Duration:  "10ms",
		Status:    "OK",
		Kind:      "SERVER",
	}

	analysis, sessionID, err := analyzer.AnalyzeSpan(context.Background(), span, session.ID)
	require.NoError(t, err)
	assert.Equal(t, "Analysis response", analysis)
	assert.Equal(t, session.ID, sessionID, "should reuse the existing session")
}

func TestAnalyzer_AnalyzeSpan_InvalidSession(t *testing.T) {
	model := &mockModel{response: "ok"}
	analyzer := newTestAnalyzer(t, model)

	span := PrunedSpan{Service: "svc", Operation: "op", Duration: "1ms", Status: "OK", Kind: "SERVER"}

	_, _, err := analyzer.AnalyzeSpan(context.Background(), span, "nonexistent-session-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAnalyzer_AnalyzeTrace_Success(t *testing.T) {
	model := &mockModel{response: "This trace shows a request flow."}
	analyzer := newTestAnalyzer(t, model)

	trace := PrunedTrace{
		TraceID:   "abc123",
		SpanCount: 3,
		Services:  []string{"frontend", "backend"},
		RootSpan:  "GET /",
		Spans: []PrunedSpan{
			{Service: "frontend", Operation: "GET /", Duration: "1s", Status: "OK", Kind: "SERVER"},
		},
	}

	analysis, sessionID, err := analyzer.AnalyzeTrace(context.Background(), trace, "")
	require.NoError(t, err)
	assert.Equal(t, "This trace shows a request flow.", analysis)
	assert.NotEmpty(t, sessionID)
}

func TestAnalyzer_FollowUp_Success(t *testing.T) {
	model := &mockModel{response: "The error was caused by a timeout."}
	analyzer := newTestAnalyzer(t, model)

	// Create a session with some history.
	session, err := analyzer.sessions.Create()
	require.NoError(t, err)
	analyzer.sessions.AddMessage(session.ID, RoleSystem, "system prompt")
	analyzer.sessions.AddMessage(session.ID, RoleUser, "Explain this span")
	analyzer.sessions.AddMessage(session.ID, RoleAssistant, "This span handles requests.")

	analysis, err := analyzer.FollowUp(context.Background(), "Why did it fail?", session.ID)
	require.NoError(t, err)
	assert.Equal(t, "The error was caused by a timeout.", analysis)

	// Verify the follow-up messages were persisted.
	s := analyzer.sessions.Get(session.ID)
	require.NotNil(t, s)
	assert.Equal(t, "Why did it fail?", s.Messages[len(s.Messages)-2].Content)
	assert.Equal(t, "The error was caused by a timeout.", s.Messages[len(s.Messages)-1].Content)
}

func TestAnalyzer_FollowUp_NoSessionID(t *testing.T) {
	model := &mockModel{response: "ok"}
	analyzer := newTestAnalyzer(t, model)

	_, err := analyzer.FollowUp(context.Background(), "question", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session_id is required")
}

func TestAnalyzer_FollowUp_SessionNotFound(t *testing.T) {
	model := &mockModel{response: "ok"}
	analyzer := newTestAnalyzer(t, model)

	_, err := analyzer.FollowUp(context.Background(), "question", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session not found")
}

func TestAnalyzer_LLMError(t *testing.T) {
	model := &mockModel{err: errors.New("model overloaded")}
	analyzer := newTestAnalyzer(t, model)

	span := PrunedSpan{Service: "svc", Operation: "op", Duration: "1ms", Status: "OK", Kind: "SERVER"}

	_, _, err := analyzer.AnalyzeSpan(context.Background(), span, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm generation failed")
}

func TestAnalyzer_EmptyLLMResponse(t *testing.T) {
	model := &mockModel{}
	// Override to return empty choices
	emptyModel := &emptyResponseModel{}
	analyzer := newTestAnalyzer(t, emptyModel)
	_ = model // avoid unused

	span := PrunedSpan{Service: "svc", Operation: "op", Duration: "1ms", Status: "OK", Kind: "SERVER"}

	_, _, err := analyzer.AnalyzeSpan(context.Background(), span, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "llm returned empty response")
}

// emptyResponseModel returns an empty ContentResponse (no choices).
type emptyResponseModel struct{}

func (*emptyResponseModel) GenerateContent(
	_ context.Context,
	_ []llms.MessageContent,
	_ ...llms.CallOption,
) (*llms.ContentResponse, error) {
	return &llms.ContentResponse{Choices: nil}, nil
}

func (*emptyResponseModel) Call(_ context.Context, _ string, _ ...llms.CallOption) (string, error) {
	return "", nil
}

func TestBuildMessagesFromSession(t *testing.T) {
	model := &mockModel{response: "ok"}
	analyzer := newTestAnalyzer(t, model)

	session := &Session{
		Messages: []Message{
			{Role: RoleSystem, Content: "system"},
			{Role: RoleUser, Content: "question"},
			{Role: RoleAssistant, Content: "answer"},
		},
	}

	msgs := analyzer.buildMessagesFromSession(session)
	require.Len(t, msgs, 3)
	assert.Equal(t, llms.ChatMessageTypeSystem, msgs[0].Role)
	assert.Equal(t, llms.ChatMessageTypeHuman, msgs[1].Role)
	assert.Equal(t, llms.ChatMessageTypeAI, msgs[2].Role)
}

func TestAnalyzer_SessionPersistence(t *testing.T) {
	model := &mockModel{response: "span analysis"}
	analyzer := newTestAnalyzer(t, model)

	span := PrunedSpan{Service: "svc", Operation: "op", Duration: "1ms", Status: "OK", Kind: "SERVER"}

	_, sessionID, err := analyzer.AnalyzeSpan(context.Background(), span, "")
	require.NoError(t, err)

	// Verify session was created and has messages.
	session := analyzer.sessions.Get(sessionID)
	require.NotNil(t, session)
	// Should have: system prompt + user query + assistant response = 3 messages
	assert.GreaterOrEqual(t, len(session.Messages), 3)
	assert.Equal(t, RoleSystem, session.Messages[0].Role)
}
