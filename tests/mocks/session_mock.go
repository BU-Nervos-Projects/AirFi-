// Package mocks provides mock implementations for testing.
package mocks

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/airfi/airfi-perun-nervous/internal/session"
)

// MockSessionManager is a mock implementation of session.Manager for testing.
type MockSessionManager struct {
	sessions map[string]*session.Session
	mu       sync.RWMutex
}

// NewMockSessionManager creates a new mock session manager.
func NewMockSessionManager() *MockSessionManager {
	return &MockSessionManager{
		sessions: make(map[string]*session.Session),
	}
}

// CreateSession creates a new session.
func (m *MockSessionManager) CreateSession(channelID, guestAddr string) (*session.Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("session-%d", len(m.sessions)+1)
	sess := &session.Session{
		ID:        id,
		ChannelID: channelID,
		GuestAddr: guestAddr,
		Status:    session.SessionStatusPending,
		TotalPaid: big.NewInt(0),
		CreatedAt: time.Now(),
	}

	m.sessions[id] = sess
	return sess, nil
}

// GetSession retrieves a session by ID.
func (m *MockSessionManager) GetSession(sessionID string) (*session.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	sess, exists := m.sessions[sessionID]
	if !exists {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	return sess, nil
}

// GetSessionByChannel retrieves a session by channel ID.
func (m *MockSessionManager) GetSessionByChannel(channelID string) (*session.Session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sess := range m.sessions {
		if sess.ChannelID == channelID {
			return sess, nil
		}
	}
	return nil, fmt.Errorf("session not found for channel: %s", channelID)
}

// ActivateSession activates a session with initial payment.
func (m *MockSessionManager) ActivateSession(sessionID string, amount *big.Int) (*session.Session, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[sessionID]
	if !exists {
		return nil, "", fmt.Errorf("session not found: %s", sessionID)
	}

	sess.Status = session.SessionStatusActive
	sess.TotalPaid = amount
	sess.StartTime = time.Now()
	sess.Duration = time.Duration(amount.Int64()) * time.Minute / 100000000
	sess.Token = "mock-token-" + sessionID

	return sess, sess.Token, nil
}

// ExtendSession extends a session with additional payment.
func (m *MockSessionManager) ExtendSession(sessionID string, amount *big.Int) (*session.Session, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[sessionID]
	if !exists {
		return nil, "", fmt.Errorf("session not found: %s", sessionID)
	}

	sess.TotalPaid = new(big.Int).Add(sess.TotalPaid, amount)
	additionalTime := time.Duration(amount.Int64()) * time.Minute / 100000000
	sess.Duration += additionalTime

	return sess, sess.Token, nil
}

// EndSession ends a session.
func (m *MockSessionManager) EndSession(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sess, exists := m.sessions[sessionID]
	if !exists {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	sess.Status = session.SessionStatusEnded
	return nil
}

// ListActiveSessions returns all active sessions.
func (m *MockSessionManager) ListActiveSessions() []*session.Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var active []*session.Session
	for _, sess := range m.sessions {
		if sess.Status == session.SessionStatusActive {
			active = append(active, sess)
		}
	}
	return active
}

// CalculatePrice calculates the price for a duration.
func (m *MockSessionManager) CalculatePrice(duration time.Duration) *big.Int {
	// 1 CKB per minute
	minutes := int64(duration.Minutes())
	return big.NewInt(minutes * 100000000)
}

// AddTestSession adds a test session directly (for testing).
func (m *MockSessionManager) AddTestSession(sess *session.Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sess.ID] = sess
}
