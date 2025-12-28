package tests

import (
	"math/big"
	"testing"
	"time"

	"github.com/airfi/airfi-perun-nervous/internal/session"
)

func TestSessionStore_Create(t *testing.T) {
	store := session.NewStore()

	sess, err := store.Create("channel-1", "guest-addr-1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if sess.ID == "" {
		t.Error("Session ID is empty")
	}
	if sess.ChannelID != "channel-1" {
		t.Errorf("ChannelID mismatch: expected channel-1, got %s", sess.ChannelID)
	}
	if sess.GuestAddr != "guest-addr-1" {
		t.Errorf("GuestAddr mismatch: expected guest-addr-1, got %s", sess.GuestAddr)
	}
	if sess.Status != session.SessionStatusPending {
		t.Errorf("Status should be pending, got %s", sess.Status)
	}
}

func TestSessionStore_Get(t *testing.T) {
	store := session.NewStore()

	created, _ := store.Create("channel-1", "guest-addr-1")

	retrieved, err := store.Get(created.ID)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("ID mismatch: expected %s, got %s", created.ID, retrieved.ID)
	}
}

func TestSessionStore_GetNotFound(t *testing.T) {
	store := session.NewStore()

	_, err := store.Get("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent session")
	}
}

func TestSessionStore_GetByChannel(t *testing.T) {
	store := session.NewStore()

	created, _ := store.Create("channel-1", "guest-addr-1")

	retrieved, err := store.GetByChannel("channel-1")
	if err != nil {
		t.Fatalf("GetByChannel failed: %v", err)
	}

	if retrieved.ID != created.ID {
		t.Errorf("ID mismatch: expected %s, got %s", created.ID, retrieved.ID)
	}
}

func TestSessionStore_Activate(t *testing.T) {
	store := session.NewStore()

	sess, _ := store.Create("channel-1", "guest-addr-1")

	duration := 1 * time.Hour
	payment := big.NewInt(500 * 100000000)
	token := "test-token"

	err := store.Activate(sess.ID, duration, token, payment)
	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	// Get updated session
	updated, _ := store.Get(sess.ID)

	if updated.Status != session.SessionStatusActive {
		t.Errorf("Status should be active, got %s", updated.Status)
	}
	if updated.Duration != duration {
		t.Errorf("Duration mismatch: expected %v, got %v", duration, updated.Duration)
	}
	if updated.Token != token {
		t.Errorf("Token mismatch: expected %s, got %s", token, updated.Token)
	}
	if updated.TotalPaid.Cmp(payment) != 0 {
		t.Errorf("TotalPaid mismatch: expected %s, got %s", payment.String(), updated.TotalPaid.String())
	}
}

func TestSessionStore_Extend(t *testing.T) {
	store := session.NewStore()

	sess, _ := store.Create("channel-1", "guest-addr-1")
	initialPayment := big.NewInt(500 * 100000000)
	store.Activate(sess.ID, 1*time.Hour, "token", initialPayment)

	// Extend by 30 minutes
	additionalPayment := big.NewInt(250 * 100000000)
	err := store.Extend(sess.ID, 30*time.Minute, additionalPayment)
	if err != nil {
		t.Fatalf("Extend failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)

	expectedDuration := 1*time.Hour + 30*time.Minute
	if updated.Duration != expectedDuration {
		t.Errorf("Duration mismatch: expected %v, got %v", expectedDuration, updated.Duration)
	}

	expectedTotal := new(big.Int).Add(initialPayment, additionalPayment)
	if updated.TotalPaid.Cmp(expectedTotal) != 0 {
		t.Errorf("TotalPaid mismatch: expected %s, got %s", expectedTotal.String(), updated.TotalPaid.String())
	}
}

func TestSessionStore_End(t *testing.T) {
	store := session.NewStore()

	sess, _ := store.Create("channel-1", "guest-addr-1")
	store.Activate(sess.ID, 1*time.Hour, "token", big.NewInt(500*100000000))

	err := store.End(sess.ID)
	if err != nil {
		t.Fatalf("End failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)

	if updated.Status != session.SessionStatusEnded {
		t.Errorf("Status should be ended, got %s", updated.Status)
	}
	if updated.EndTime == nil {
		t.Error("EndTime should be set")
	}
}

func TestSessionStore_MarkExpired(t *testing.T) {
	store := session.NewStore()

	sess, _ := store.Create("channel-1", "guest-addr-1")
	store.Activate(sess.ID, 1*time.Hour, "token", big.NewInt(500*100000000))

	err := store.MarkExpired(sess.ID)
	if err != nil {
		t.Fatalf("MarkExpired failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)

	if updated.Status != session.SessionStatusExpired {
		t.Errorf("Status should be expired, got %s", updated.Status)
	}
}

func TestSessionStore_ListActive(t *testing.T) {
	store := session.NewStore()

	// Create and activate 2 sessions
	sess1, _ := store.Create("channel-1", "guest-1")
	store.Activate(sess1.ID, 1*time.Hour, "token1", big.NewInt(500*100000000))

	sess2, _ := store.Create("channel-2", "guest-2")
	store.Activate(sess2.ID, 1*time.Hour, "token2", big.NewInt(500*100000000))

	// Create pending session
	store.Create("channel-3", "guest-3")

	active := store.ListActive()
	if len(active) != 2 {
		t.Errorf("Expected 2 active sessions, got %d", len(active))
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := session.NewStore()

	sess, _ := store.Create("channel-1", "guest-addr-1")

	err := store.Delete(sess.ID)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = store.Get(sess.ID)
	if err == nil {
		t.Error("Session should be deleted")
	}
}

func TestSessionStore_Count(t *testing.T) {
	store := session.NewStore()

	if store.Count() != 0 {
		t.Error("Initial count should be 0")
	}

	store.Create("channel-1", "guest-1")
	store.Create("channel-2", "guest-2")

	if store.Count() != 2 {
		t.Errorf("Count should be 2, got %d", store.Count())
	}
}

func TestSession_IsActive(t *testing.T) {
	sess := &session.Session{
		Status:    session.SessionStatusActive,
		StartTime: time.Now(),
		Duration:  1 * time.Hour,
	}

	if !sess.IsActive() {
		t.Error("Session should be active")
	}

	// Change status
	sess.Status = session.SessionStatusEnded
	if sess.IsActive() {
		t.Error("Session should not be active after ending")
	}
}

func TestSession_IsActive_Expired(t *testing.T) {
	sess := &session.Session{
		Status:    session.SessionStatusActive,
		StartTime: time.Now().Add(-2 * time.Hour),
		Duration:  1 * time.Hour,
	}

	if sess.IsActive() {
		t.Error("Session should not be active (time expired)")
	}
}

func TestSession_RemainingTime(t *testing.T) {
	sess := &session.Session{
		Status:    session.SessionStatusActive,
		StartTime: time.Now(),
		Duration:  1 * time.Hour,
	}

	remaining := sess.RemainingTime()
	if remaining < 59*time.Minute || remaining > 1*time.Hour {
		t.Errorf("Remaining time should be close to 1 hour, got %v", remaining)
	}
}

func TestSession_RemainingTime_Expired(t *testing.T) {
	sess := &session.Session{
		Status:    session.SessionStatusActive,
		StartTime: time.Now().Add(-2 * time.Hour),
		Duration:  1 * time.Hour,
	}

	remaining := sess.RemainingTime()
	if remaining != 0 {
		t.Errorf("Remaining time should be 0, got %v", remaining)
	}
}

func TestSession_RemainingTimeFormatted(t *testing.T) {
	tests := []struct {
		name      string
		startTime time.Time
		duration  time.Duration
		status    session.SessionStatus
		expected  string
	}{
		{
			name:      "Expired",
			startTime: time.Now().Add(-2 * time.Hour),
			duration:  1 * time.Hour,
			status:    session.SessionStatusActive,
			expected:  "0s",
		},
		{
			name:      "Not active",
			startTime: time.Now(),
			duration:  1 * time.Hour,
			status:    session.SessionStatusEnded,
			expected:  "0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &session.Session{
				Status:    tt.status,
				StartTime: tt.startTime,
				Duration:  tt.duration,
			}

			result := sess.RemainingTimeFormatted()
			if result != tt.expected {
				t.Errorf("RemainingTimeFormatted: expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestDefaultRateConfig(t *testing.T) {
	config := session.DefaultRateConfig()

	if config.CKBytesPerMinute == nil {
		t.Error("CKBytesPerMinute is nil")
	}
	if config.CKBytesPerMinute.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("CKBytesPerMinute should be 1, got %s", config.CKBytesPerMinute.String())
	}
	if config.MinSessionTime != 5*time.Minute {
		t.Errorf("MinSessionTime should be 5m, got %v", config.MinSessionTime)
	}
	if config.MaxSessionTime != 24*time.Hour {
		t.Errorf("MaxSessionTime should be 24h, got %v", config.MaxSessionTime)
	}
}
