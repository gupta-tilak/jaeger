// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// defaultSessionTTL is how long a session stays alive without activity.
	defaultSessionTTL = 30 * time.Minute

	// maxMessagesPerSession prevents unbounded memory growth.
	// Older messages are evicted when this limit is reached.
	maxMessagesPerSession = 50

	// gcInterval is how often the background goroutine sweeps expired sessions.
	gcInterval = 5 * time.Minute
)

// MessageRole identifies who sent a message in the conversation.
type MessageRole string

const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleSystem    MessageRole = "system"
)

// Message represents a single turn in the conversation history.
type Message struct {
	Role    MessageRole `json:"role"`
	Content string      `json:"content"`
}

// Session holds the conversation state for a single user interaction.
// Sessions are identified by opaque IDs and expire after a TTL.
type Session struct {
	ID        string    `json:"id"`
	Messages  []Message `json:"messages,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SessionManager provides thread-safe, TTL-based session storage.
//
// Design decisions:
//   - In-memory only — no persistence. Sessions are ephemeral by nature;
//     trace analysis context doesn't need to survive restarts.
//   - TTL-based expiry — sessions auto-expire after inactivity to prevent
//     memory leaks from abandoned sessions.
//   - Message cap — each session stores at most maxMessagesPerSession turns.
//     When exceeded, oldest messages are dropped (sliding window).
//   - Thread-safe — all operations are guarded by a mutex because HTTP
//     handlers may access sessions concurrently.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*Session
	ttl      time.Duration
	done     chan struct{}
}

// NewSessionManager creates a session store with the given TTL.
// Call Close() when done to stop the background cleanup goroutine.
func NewSessionManager(ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	sm := &SessionManager{
		sessions: make(map[string]*Session),
		ttl:      ttl,
		done:     make(chan struct{}),
	}
	go sm.gc()
	return sm
}

// Create starts a new session and returns its ID.
func (sm *SessionManager) Create() (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	s := &Session{
		ID:        id,
		Messages:  nil,
		CreatedAt: now,
		UpdatedAt: now,
	}
	sm.mu.Lock()
	sm.sessions[id] = s
	sm.mu.Unlock()
	return s, nil
}

// Get retrieves a session by ID. Returns nil if not found or expired.
func (sm *SessionManager) Get(id string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.sessions[id]
	if !ok {
		return nil
	}
	if time.Since(s.UpdatedAt) > sm.ttl {
		delete(sm.sessions, id)
		return nil
	}
	return s
}

// AddMessage appends a message to a session and refreshes the TTL.
// If the session has reached maxMessagesPerSession, the oldest non-system
// message is evicted to make room.
func (sm *SessionManager) AddMessage(id string, role MessageRole, content string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.sessions[id]
	if !ok {
		return false
	}
	if time.Since(s.UpdatedAt) > sm.ttl {
		delete(sm.sessions, id)
		return false
	}

	// Evict oldest non-system messages when at capacity.
	if len(s.Messages) >= maxMessagesPerSession {
		// Find first non-system message to evict.
		for i, m := range s.Messages {
			if m.Role != RoleSystem {
				s.Messages = append(s.Messages[:i], s.Messages[i+1:]...)
				break
			}
		}
	}

	s.Messages = append(s.Messages, Message{Role: role, Content: content})
	s.UpdatedAt = time.Now()
	return true
}

// Delete removes a session explicitly.
func (sm *SessionManager) Delete(id string) {
	sm.mu.Lock()
	delete(sm.sessions, id)
	sm.mu.Unlock()
}

// Close stops the background GC goroutine.
func (sm *SessionManager) Close() {
	close(sm.done)
}

// gc periodically sweeps expired sessions.
func (sm *SessionManager) gc() {
	ticker := time.NewTicker(gcInterval)
	defer ticker.Stop()
	for {
		select {
		case <-sm.done:
			return
		case <-ticker.C:
			sm.sweep()
		}
	}
}

func (sm *SessionManager) sweep() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for id, s := range sm.sessions {
		if time.Since(s.UpdatedAt) > sm.ttl {
			delete(sm.sessions, id)
		}
	}
}

func generateSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
