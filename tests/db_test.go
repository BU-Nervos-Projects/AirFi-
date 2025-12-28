package tests

import (
	"os"
	"testing"
	"time"

	"github.com/airfi/airfi-perun-nervous/internal/db"
)

func setupTestDB(t *testing.T) (*db.DB, func()) {
	tmpFile, err := os.CreateTemp("", "test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	database, err := db.Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to open database: %v", err)
	}

	cleanup := func() {
		database.Close()
		os.Remove(tmpFile.Name())
	}

	return database, cleanup
}

func TestDB_CreateAndGetSession(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	session := &db.Session{
		ID:           "test-session-1",
		WalletID:     "wallet-1",
		GuestAddress: "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq123",
		HostAddress:  "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq456",
		ChannelID:    "channel-1",
		FundingCKB:   500,
		BalanceCKB:   500,
		SpentCKB:     0,
		Status:       "active",
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}

	err := database.CreateSession(session)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	retrieved, err := database.GetSession("test-session-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("ID mismatch: expected %s, got %s", session.ID, retrieved.ID)
	}
	if retrieved.FundingCKB != session.FundingCKB {
		t.Errorf("FundingCKB mismatch: expected %d, got %d", session.FundingCKB, retrieved.FundingCKB)
	}
	if retrieved.Status != session.Status {
		t.Errorf("Status mismatch: expected %s, got %s", session.Status, retrieved.Status)
	}
}

func TestDB_UpdateSessionStatus(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	session := &db.Session{
		ID:         "test-session-2",
		WalletID:   "wallet-2",
		Status:     "channel_opening",
		FundingCKB: 500,
		BalanceCKB: 500,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	err := database.CreateSession(session)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	err = database.UpdateSessionStatus("test-session-2", "active")
	if err != nil {
		t.Fatalf("UpdateSessionStatus failed: %v", err)
	}

	retrieved, err := database.GetSession("test-session-2")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.Status != "active" {
		t.Errorf("Status not updated: expected active, got %s", retrieved.Status)
	}
}

func TestDB_UpdateSessionBalance(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	session := &db.Session{
		ID:         "test-session-3",
		WalletID:   "wallet-3",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 500,
		SpentCKB:   0,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	err := database.CreateSession(session)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	err = database.UpdateSessionBalance("test-session-3", 400, 100)
	if err != nil {
		t.Fatalf("UpdateSessionBalance failed: %v", err)
	}

	retrieved, err := database.GetSession("test-session-3")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.BalanceCKB != 400 {
		t.Errorf("BalanceCKB not updated: expected 400, got %d", retrieved.BalanceCKB)
	}
	if retrieved.SpentCKB != 100 {
		t.Errorf("SpentCKB not updated: expected 100, got %d", retrieved.SpentCKB)
	}
}

func TestDB_ListSessions(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	sessions := []*db.Session{
		{ID: "session-a", WalletID: "wallet-a", Status: "active", FundingCKB: 500, BalanceCKB: 500, ExpiresAt: time.Now().Add(1 * time.Hour)},
		{ID: "session-b", WalletID: "wallet-b", Status: "active", FundingCKB: 250, BalanceCKB: 250, ExpiresAt: time.Now().Add(30 * time.Minute)},
		{ID: "session-c", WalletID: "wallet-c", Status: "settled", FundingCKB: 100, BalanceCKB: 0, ExpiresAt: time.Now().Add(-1 * time.Hour)},
	}

	for _, s := range sessions {
		err := database.CreateSession(s)
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
	}

	activeSessions, err := database.ListSessions("active")
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	if len(activeSessions) != 2 {
		t.Errorf("Expected 2 active sessions, got %d", len(activeSessions))
	}

	allSessions, err := database.ListSessions("")
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	if len(allSessions) != 3 {
		t.Errorf("Expected 3 total sessions, got %d", len(allSessions))
	}
}

func TestDB_SettleSession(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	session := &db.Session{
		ID:         "test-session-settle",
		WalletID:   "wallet-settle",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 300,
		SpentCKB:   200,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	err := database.CreateSession(session)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	err = database.SettleSession("test-session-settle")
	if err != nil {
		t.Fatalf("SettleSession failed: %v", err)
	}

	retrieved, err := database.GetSession("test-session-settle")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.Status != "settled" {
		t.Errorf("Status not settled: expected settled, got %s", retrieved.Status)
	}
}

func TestDB_CreateAndGetGuestWallet(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	wallet := &db.GuestWallet{
		ID:            "wallet-test-1",
		Address:       "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq789",
		PrivateKeyHex: "0x1234567890abcdef",
		Status:        "pending",
	}

	err := database.CreateGuestWallet(wallet)
	if err != nil {
		t.Fatalf("CreateGuestWallet failed: %v", err)
	}

	retrieved, err := database.GetGuestWallet("wallet-test-1")
	if err != nil {
		t.Fatalf("GetGuestWallet failed: %v", err)
	}

	if retrieved.Address != wallet.Address {
		t.Errorf("Address mismatch: expected %s, got %s", wallet.Address, retrieved.Address)
	}
	if retrieved.Status != "pending" {
		t.Errorf("Status mismatch: expected pending, got %s", retrieved.Status)
	}
}

func TestDB_GetGuestWalletByAddress(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	wallet := &db.GuestWallet{
		ID:            "wallet-addr-test",
		Address:       "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsqabc",
		PrivateKeyHex: "0xabcdef1234567890",
		Status:        "pending",
	}

	err := database.CreateGuestWallet(wallet)
	if err != nil {
		t.Fatalf("CreateGuestWallet failed: %v", err)
	}

	retrieved, err := database.GetGuestWalletByAddress(wallet.Address)
	if err != nil {
		t.Fatalf("GetGuestWalletByAddress failed: %v", err)
	}

	if retrieved.ID != wallet.ID {
		t.Errorf("ID mismatch: expected %s, got %s", wallet.ID, retrieved.ID)
	}
}

func TestDB_UpdateWalletFunded(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	wallet := &db.GuestWallet{
		ID:            "wallet-fund-test",
		Address:       "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsqdef",
		PrivateKeyHex: "0xfedcba0987654321",
		Status:        "pending",
	}

	err := database.CreateGuestWallet(wallet)
	if err != nil {
		t.Fatalf("CreateGuestWallet failed: %v", err)
	}

	err = database.UpdateWalletFunded("wallet-fund-test", 500, "session-xyz")
	if err != nil {
		t.Fatalf("UpdateWalletFunded failed: %v", err)
	}

	retrieved, err := database.GetGuestWallet("wallet-fund-test")
	if err != nil {
		t.Fatalf("GetGuestWallet failed: %v", err)
	}

	if retrieved.Status != "funded" {
		t.Errorf("Status not funded: expected funded, got %s", retrieved.Status)
	}
	if retrieved.BalanceCKB != 500 {
		t.Errorf("BalanceCKB not updated: expected 500, got %d", retrieved.BalanceCKB)
	}
	if retrieved.SessionID != "session-xyz" {
		t.Errorf("SessionID not updated: expected session-xyz, got %s", retrieved.SessionID)
	}
}

func TestDB_ListPendingWallets(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	wallets := []*db.GuestWallet{
		{ID: "w1", Address: "addr1", PrivateKeyHex: "pk1", Status: "created"},
		{ID: "w2", Address: "addr2", PrivateKeyHex: "pk2", Status: "created"},
		{ID: "w3", Address: "addr3", PrivateKeyHex: "pk3", Status: "funded"},
	}

	for _, w := range wallets {
		err := database.CreateGuestWallet(w)
		if err != nil {
			t.Fatalf("CreateGuestWallet failed: %v", err)
		}
	}

	pending, err := database.ListPendingWallets()
	if err != nil {
		t.Fatalf("ListPendingWallets failed: %v", err)
	}

	if len(pending) != 2 {
		t.Errorf("Expected 2 pending wallets, got %d", len(pending))
	}
}

func TestDB_ExtendSession(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	session := &db.Session{
		ID:         "test-extend",
		WalletID:   "wallet-extend",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 500,
		SpentCKB:   0,
		ExpiresAt:  time.Now().Add(30 * time.Minute),
	}

	err := database.CreateSession(session)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	err = database.ExtendSession("test-extend", 30, 250)
	if err != nil {
		t.Fatalf("ExtendSession failed: %v", err)
	}

	retrieved, err := database.GetSession("test-extend")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.SpentCKB != 250 {
		t.Errorf("SpentCKB not updated: expected 250, got %d", retrieved.SpentCKB)
	}
}

func TestDB_GetSessionNotFound(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := database.GetSession("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent session")
	}
}

func TestDB_GetGuestWalletNotFound(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	_, err := database.GetGuestWallet("nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent wallet")
	}
}

func TestDB_GetStats(t *testing.T) {
	database, cleanup := setupTestDB(t)
	defer cleanup()

	sessions := []*db.Session{
		{ID: "s1", WalletID: "w1", Status: "active", FundingCKB: 500, BalanceCKB: 400, SpentCKB: 100, ExpiresAt: time.Now().Add(1 * time.Hour)},
		{ID: "s2", WalletID: "w2", Status: "active", FundingCKB: 250, BalanceCKB: 200, SpentCKB: 50, ExpiresAt: time.Now().Add(30 * time.Minute)},
		{ID: "s3", WalletID: "w3", Status: "settled", FundingCKB: 100, BalanceCKB: 0, SpentCKB: 100, ExpiresAt: time.Now().Add(-1 * time.Hour)},
	}

	for _, s := range sessions {
		err := database.CreateSession(s)
		if err != nil {
			t.Fatalf("CreateSession failed: %v", err)
		}
	}

	total, active, totalEarned, err := database.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if total != 3 {
		t.Errorf("Total sessions: expected 3, got %d", total)
	}
	if active != 2 {
		t.Errorf("Active sessions: expected 2, got %d", active)
	}
	if totalEarned != 250 {
		t.Errorf("Total earned: expected 250, got %d", totalEarned)
	}
}
