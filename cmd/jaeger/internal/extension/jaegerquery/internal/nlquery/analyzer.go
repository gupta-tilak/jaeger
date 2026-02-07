// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"context"
	"errors"
	"fmt"

	"github.com/tmc/langchaingo/llms"
	"go.uber.org/zap"
)

// Analysis prompts for different analysis tasks.
// These are kept as constants to ensure consistency and testability.

const spanAnalysisSystemPrompt = `You are an expert distributed systems engineer analyzing trace data from Jaeger, a distributed tracing platform.

Your task is to analyze a single span and provide a clear, concise explanation of:
1. What this span represents (the operation it performs)
2. Whether it completed successfully or failed, and why
3. Any performance concerns (is the duration unusually long?)
4. Key attributes that indicate important behavior
5. Any events/logs that provide additional context

Keep your response concise and actionable. Focus on what would help a developer debug or understand this span.
Do NOT make up information that is not present in the span data.
If you see error status or error-related attributes, highlight them prominently.`

const traceAnalysisSystemPrompt = `You are an expert distributed systems engineer analyzing trace data from Jaeger, a distributed tracing platform.

Your task is to analyze a complete distributed trace and provide:
1. A high-level summary of the request flow across services
2. The critical path (which spans contribute most to total latency)
3. Any errors or failures and their likely root cause
4. Performance bottlenecks (spans with disproportionately long durations)
5. Service interaction patterns (which services call which)

Keep your response structured and actionable. Use the span hierarchy (parent-child relationships) to understand the call flow.
Do NOT make up information that is not present in the trace data.
Highlight the most important findings first.`

// Analyzer provides LLM-backed contextual analysis of traces and spans.
//
// It uses the same LangChainGo model as the NL query extractor but with
// different system prompts optimized for analysis/summarization tasks.
//
// Session integration:
//   - Each analysis request can optionally include a session ID.
//   - If a session is active, the conversation history is included,
//     enabling follow-up questions like "why did that span fail?"
//   - If no session is provided, the analysis is stateless.
//
// Why analysis belongs in the Query Service:
// The Query Service already owns trace retrieval and transformation.
// Analysis is a transformation of trace data into human-readable insights.
// Placing it here avoids duplicating trace fetching logic and keeps the
// LLM integration centralized.
type Analyzer struct {
	model    llms.Model
	config   Config
	sessions *SessionManager
	logger   *zap.Logger
}

// NewAnalyzer creates an analyzer backed by the given LangChainGo model.
// The session manager is shared with other components that need session state.
func NewAnalyzer(model llms.Model, config Config, sessions *SessionManager, logger *zap.Logger) *Analyzer {
	return &Analyzer{
		model:    model,
		config:   config,
		sessions: sessions,
		logger:   logger,
	}
}

// AnalyzeSpan generates an explanation of a single span using the LLM.
// If sessionID is provided and valid, the conversation context is maintained.
func (a *Analyzer) AnalyzeSpan(ctx context.Context, span PrunedSpan, sessionID string) (analysis string, sid string, err error) {
	spanText := FormatPrunedSpanForLLM(span)
	userPrompt := "Explain this span:\n\n" + spanText

	return a.analyze(ctx, spanAnalysisSystemPrompt, userPrompt, sessionID)
}

// AnalyzeTrace generates an explanation of an entire trace using the LLM.
// If sessionID is provided and valid, the conversation context is maintained.
func (a *Analyzer) AnalyzeTrace(ctx context.Context, trace PrunedTrace, sessionID string) (analysis string, sid string, err error) {
	traceText := FormatPrunedTraceForLLM(trace)
	userPrompt := "Analyze this trace:\n\n" + traceText

	return a.analyze(ctx, traceAnalysisSystemPrompt, userPrompt, sessionID)
}

// FollowUp sends a follow-up question in an existing session.
// The session must exist and contain prior conversation context.
func (a *Analyzer) FollowUp(ctx context.Context, question string, sessionID string) (string, error) {
	if sessionID == "" {
		return "", errors.New("session_id is required for follow-up questions")
	}
	session := a.sessions.Get(sessionID)
	if session == nil {
		return "", errors.New("session not found or expired")
	}

	// Build messages from session history + new question.
	messages := a.buildMessagesFromSession(session)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, question))

	response, err := a.callModel(ctx, messages)
	if err != nil {
		return "", err
	}

	// Persist the exchange in the session.
	a.sessions.AddMessage(sessionID, RoleUser, question)
	a.sessions.AddMessage(sessionID, RoleAssistant, response)

	return response, nil
}

// analyze is the core analysis pipeline shared by AnalyzeSpan and AnalyzeTrace.
// It handles session creation/reuse and message construction.
//
// Returns: (response, sessionID, error)
func (a *Analyzer) analyze(ctx context.Context, systemPrompt, userPrompt, sessionID string) (response string, sid string, err error) {
	// Resolve or create session.
	var session *Session
	if sessionID != "" {
		session = a.sessions.Get(sessionID)
		if session == nil {
			return "", "", errors.New("session not found or expired")
		}
	} else {
		session, err = a.sessions.Create()
		if err != nil {
			return "", "", fmt.Errorf("failed to create session: %w", err)
		}
		// Store the system prompt as the first message.
		a.sessions.AddMessage(session.ID, RoleSystem, systemPrompt)
	}

	// Build LLM messages: system prompt + history + current question.
	messages := a.buildMessagesFromSession(session)
	messages = append(messages, llms.TextParts(llms.ChatMessageTypeHuman, userPrompt))

	response, err = a.callModel(ctx, messages)
	if err != nil {
		return "", session.ID, err
	}

	// Persist the exchange.
	a.sessions.AddMessage(session.ID, RoleUser, userPrompt)
	a.sessions.AddMessage(session.ID, RoleAssistant, response)

	return response, session.ID, nil
}

// buildMessagesFromSession converts stored session messages to LangChainGo message format.
func (*Analyzer) buildMessagesFromSession(session *Session) []llms.MessageContent {
	messages := make([]llms.MessageContent, 0, len(session.Messages))
	for _, msg := range session.Messages {
		role := llms.ChatMessageTypeHuman
		switch msg.Role {
		case RoleSystem:
			role = llms.ChatMessageTypeSystem
		case RoleAssistant:
			role = llms.ChatMessageTypeAI
		default:
			// RoleUser and any unknown role map to Human.
		}
		messages = append(messages, llms.TextParts(role, msg.Content))
	}
	return messages
}

// callModel sends messages to the LLM and extracts the response text.
func (a *Analyzer) callModel(ctx context.Context, messages []llms.MessageContent) (string, error) {
	resp, err := a.model.GenerateContent(ctx, messages,
		llms.WithTemperature(a.config.Temperature),
		llms.WithMaxTokens(a.config.MaxTokens),
	)
	if err != nil {
		return "", fmt.Errorf("llm generation failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", errors.New("llm returned empty response")
	}

	response := resp.Choices[0].Content
	a.logger.Debug("analyzer llm response",
		zap.String("response", response),
	)

	return response, nil
}
