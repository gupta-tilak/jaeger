// Copyright (c) 2026 The Jaeger Authors.
// SPDX-License-Identifier: Apache-2.0

package nlquery

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionManager_Create(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.NotEmpty(t, session.ID)
	assert.Len(t, session.ID, 32, "session ID should be 32 hex characters")
	assert.Empty(t, session.Messages)
	assert.False(t, session.CreatedAt.IsZero())
	assert.False(t, session.UpdatedAt.IsZero())
}

func TestSessionManager_Get_Found(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	got := sm.Get(session.ID)
	require.NotNil(t, got)
	assert.Equal(t, session.ID, got.ID)
}

func TestSessionManager_Get_NotFound(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	got := sm.Get("nonexistent-id")
	assert.Nil(t, got)
}

func TestSessionManager_Get_Expired(t *testing.T) {
	sm := NewSessionManager(10 * time.Millisecond)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	// Wait for the session to expire.
	time.Sleep(20 * time.Millisecond)

	got := sm.Get(session.ID)
	assert.Nil(t, got, "expired session should not be returned")
}

func TestSessionManager_AddMessage(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	ok := sm.AddMessage(session.ID, RoleUser, "hello")
	assert.True(t, ok)

	got := sm.Get(session.ID)
	require.NotNil(t, got)
	require.Len(t, got.Messages, 1)
	assert.Equal(t, RoleUser, got.Messages[0].Role)
	assert.Equal(t, "hello", got.Messages[0].Content)
}

func TestSessionManager_AddMessage_RefreshesTTL(t *testing.T) {
	sm := NewSessionManager(50 * time.Millisecond)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	// Wait 30ms — less than TTL.
	time.Sleep(30 * time.Millisecond)
	ok := sm.AddMessage(session.ID, RoleUser, "ping")
	assert.True(t, ok)

	// Wait another 30ms — total 60ms since creation but only 30ms since last AddMessage.
	time.Sleep(30 * time.Millisecond)
	got := sm.Get(session.ID)
	assert.NotNil(t, got, "TTL should have been refreshed by AddMessage")
}

func TestSessionManager_AddMessage_NotFound(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	ok := sm.AddMessage("nonexistent-id", RoleUser, "hello")
	assert.False(t, ok)
}

func TestSessionManager_AddMessage_Expired(t *testing.T) {
	sm := NewSessionManager(10 * time.Millisecond)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	ok := sm.AddMessage(session.ID, RoleUser, "hello")
	assert.False(t, ok, "should fail for expired session")
}

func TestSessionManager_AddMessage_EvictsOldest(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	// Add a system message first.
	sm.AddMessage(session.ID, RoleSystem, "system prompt")

	// Fill to capacity with user/assistant messages.
	for i := 1; i < maxMessagesPerSession; i++ {
		if i%2 == 1 {
			sm.AddMessage(session.ID, RoleUser, "user msg")
		} else {
			sm.AddMessage(session.ID, RoleAssistant, "assistant msg")
		}
	}

	got := sm.Get(session.ID)
	require.Len(t, got.Messages, maxMessagesPerSession)

	// Add one more — the oldest non-system message should be evicted.
	sm.AddMessage(session.ID, RoleUser, "overflow")

	got = sm.Get(session.ID)
	require.Len(t, got.Messages, maxMessagesPerSession)
	// System message should still be first.
	assert.Equal(t, RoleSystem, got.Messages[0].Role)
	// Last message should be the overflow.
	assert.Equal(t, "overflow", got.Messages[len(got.Messages)-1].Content)
}

func TestSessionManager_Delete(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	defer sm.Close()

	session, err := sm.Create()
	require.NoError(t, err)

	sm.Delete(session.ID)

	got := sm.Get(session.ID)
	assert.Nil(t, got, "deleted session should not be returned")
}

func TestSessionManager_DefaultTTL(t *testing.T) {
	sm := NewSessionManager(0) // Should use defaultSessionTTL
	defer sm.Close()

	assert.Equal(t, defaultSessionTTL, sm.ttl)
}

func TestSessionManager_Sweep(t *testing.T) {
	sm := NewSessionManager(10 * time.Millisecond)
	defer sm.Close()

	_, err := sm.Create()
	require.NoError(t, err)

	time.Sleep(20 * time.Millisecond)

	sm.sweep()

	sm.mu.Lock()
	count := len(sm.sessions)
	sm.mu.Unlock()
	assert.Equal(t, 0, count, "sweep should remove expired sessions")
}

func TestGenerateSessionID(t *testing.T) {
	id1, err := generateSessionID()
	require.NoError(t, err)
	id2, err := generateSessionID()
	require.NoError(t, err)

	assert.NotEqual(t, id1, id2, "session IDs should be unique")
	assert.Len(t, id1, 32, "session ID should be 32 hex characters (16 bytes)")
}
