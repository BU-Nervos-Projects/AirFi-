package session

import (
	"math/big"
	"testing"
	"time"
)

func TestSessionStore_Create(t *testing.T) {
	store := NewStore()

	sess, err := store.Create("channel-1", "guest-1")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if sess.ID == "" {
		t.Error("Session ID is empty")
	}
	if sess.ChannelID != "channel-1" {
		t.Errorf("ChannelID: expected channel-1, got %s", sess.ChannelID)
	}
	if sess.Status != SessionStatusPending {
		t.Errorf("Status: expected pending, got %s", sess.Status)
	}
}

func TestSessionStore_Get(t *testing.T) {
	store := NewStore()

	created, _ := store.Create("channel-1", "guest-1")
	retrieved, err := store.Get(created.ID)

	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.ID != created.ID {
		t.Error("ID mismatch")
	}
}

func TestSessionStore_GetNotFound(t *testing.T) {
	store := NewStore()

	_, err := store.Get("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent session")
	}
}

func TestSessionStore_GetByChannel(t *testing.T) {
	store := NewStore()

	created, _ := store.Create("channel-1", "guest-1")
	retrieved, err := store.GetByChannel("channel-1")

	if err != nil {
		t.Fatalf("GetByChannel failed: %v", err)
	}
	if retrieved.ID != created.ID {
		t.Error("ID mismatch")
	}
}

func TestSessionStore_Activate(t *testing.T) {
	store := NewStore()

	sess, _ := store.Create("channel-1", "guest-1")
	err := store.Activate(sess.ID, 1*time.Hour, "test-token", big.NewInt(500))

	if err != nil {
		t.Fatalf("Activate failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)
	if updated.Status != SessionStatusActive {
		t.Errorf("Status: expected active, got %s", updated.Status)
	}
	if updated.Token != "test-token" {
		t.Errorf("Token: expected test-token, got %s", updated.Token)
	}
}

func TestSessionStore_Extend(t *testing.T) {
	store := NewStore()

	sess, _ := store.Create("channel-1", "guest-1")
	store.Activate(sess.ID, 1*time.Hour, "token", big.NewInt(500))

	err := store.Extend(sess.ID, 30*time.Minute, big.NewInt(250))
	if err != nil {
		t.Fatalf("Extend failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)
	expectedDuration := 1*time.Hour + 30*time.Minute
	if updated.Duration != expectedDuration {
		t.Errorf("Duration: expected %v, got %v", expectedDuration, updated.Duration)
	}
}

func TestSessionStore_End(t *testing.T) {
	store := NewStore()

	sess, _ := store.Create("channel-1", "guest-1")
	store.Activate(sess.ID, 1*time.Hour, "token", big.NewInt(500))

	err := store.End(sess.ID)
	if err != nil {
		t.Fatalf("End failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)
	if updated.Status != SessionStatusEnded {
		t.Errorf("Status: expected ended, got %s", updated.Status)
	}
}

func TestSessionStore_MarkExpired(t *testing.T) {
	store := NewStore()

	sess, _ := store.Create("channel-1", "guest-1")
	store.Activate(sess.ID, 1*time.Hour, "token", big.NewInt(500))

	err := store.MarkExpired(sess.ID)
	if err != nil {
		t.Fatalf("MarkExpired failed: %v", err)
	}

	updated, _ := store.Get(sess.ID)
	if updated.Status != SessionStatusExpired {
		t.Errorf("Status: expected expired, got %s", updated.Status)
	}
}

func TestSessionStore_ListActive(t *testing.T) {
	store := NewStore()

	s1, _ := store.Create("c1", "g1")
	store.Activate(s1.ID, 1*time.Hour, "t1", big.NewInt(500))

	s2, _ := store.Create("c2", "g2")
	store.Activate(s2.ID, 1*time.Hour, "t2", big.NewInt(500))

	store.Create("c3", "g3") // pending

	active := store.ListActive()
	if len(active) != 2 {
		t.Errorf("Expected 2 active, got %d", len(active))
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewStore()

	sess, _ := store.Create("channel-1", "guest-1")
	store.Delete(sess.ID)

	_, err := store.Get(sess.ID)
	if err == nil {
		t.Error("Session should be deleted")
	}
}

func TestSessionStore_Count(t *testing.T) {
	store := NewStore()

	if store.Count() != 0 {
		t.Error("Initial count should be 0")
	}

	store.Create("c1", "g1")
	store.Create("c2", "g2")

	if store.Count() != 2 {
		t.Errorf("Count: expected 2, got %d", store.Count())
	}
}

func TestSession_IsActive(t *testing.T) {
	sess := &Session{
		Status:    SessionStatusActive,
		StartTime: time.Now(),
		Duration:  1 * time.Hour,
	}

	if !sess.IsActive() {
		t.Error("Session should be active")
	}

	sess.Status = SessionStatusEnded
	if sess.IsActive() {
		t.Error("Ended session should not be active")
	}
}

func TestSession_IsActive_Expired(t *testing.T) {
	sess := &Session{
		Status:    SessionStatusActive,
		StartTime: time.Now().Add(-2 * time.Hour),
		Duration:  1 * time.Hour,
	}

	if sess.IsActive() {
		t.Error("Expired session should not be active")
	}
}

func TestSession_RemainingTime(t *testing.T) {
	sess := &Session{
		Status:    SessionStatusActive,
		StartTime: time.Now(),
		Duration:  1 * time.Hour,
	}

	remaining := sess.RemainingTime()
	if remaining < 59*time.Minute {
		t.Errorf("Remaining time too short: %v", remaining)
	}
}

func TestSession_RemainingTimeFormatted(t *testing.T) {
	// Expired session
	sess := &Session{
		Status:    SessionStatusActive,
		StartTime: time.Now().Add(-2 * time.Hour),
		Duration:  1 * time.Hour,
	}

	if sess.RemainingTimeFormatted() != "0s" {
		t.Errorf("Expected '0s' for expired, got %s", sess.RemainingTimeFormatted())
	}

	// Inactive session
	sess2 := &Session{
		Status:    SessionStatusEnded,
		StartTime: time.Now(),
		Duration:  1 * time.Hour,
	}

	if sess2.RemainingTimeFormatted() != "0s" {
		t.Errorf("Expected '0s' for inactive, got %s", sess2.RemainingTimeFormatted())
	}
}

func TestDefaultRateConfig(t *testing.T) {
	config := DefaultRateConfig()

	if config.CKBytesPerMinute.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("CKBytesPerMinute: expected 1, got %s", config.CKBytesPerMinute.String())
	}
	if config.MinSessionTime != 5*time.Minute {
		t.Errorf("MinSessionTime: expected 5m, got %v", config.MinSessionTime)
	}
	if config.MaxSessionTime != 24*time.Hour {
		t.Errorf("MaxSessionTime: expected 24h, got %v", config.MaxSessionTime)
	}
}
