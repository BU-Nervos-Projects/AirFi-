package db

import (
	"os"
	"testing"
	"time"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	tmpFile, err := os.CreateTemp("", "test_*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	database, err := Open(tmpFile.Name())
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

func TestDB_Open(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	if db == nil {
		t.Error("Database should not be nil")
	}
}

func TestDB_CreateAndGetSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	session := &Session{
		ID:         "test-1",
		WalletID:   "wallet-1",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 500,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	if err := db.CreateSession(session); err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	retrieved, err := db.GetSession("test-1")
	if err != nil {
		t.Fatalf("GetSession failed: %v", err)
	}

	if retrieved.ID != session.ID {
		t.Errorf("ID mismatch: expected %s, got %s", session.ID, retrieved.ID)
	}
}

func TestDB_UpdateSessionStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	session := &Session{
		ID:         "test-2",
		WalletID:   "wallet-2",
		Status:     "channel_opening",
		FundingCKB: 500,
		BalanceCKB: 500,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	db.CreateSession(session)
	db.UpdateSessionStatus("test-2", "active")

	retrieved, _ := db.GetSession("test-2")
	if retrieved.Status != "active" {
		t.Errorf("Status not updated: expected active, got %s", retrieved.Status)
	}
}

func TestDB_UpdateSessionBalance(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	session := &Session{
		ID:         "test-3",
		WalletID:   "wallet-3",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 500,
		SpentCKB:   0,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	db.CreateSession(session)
	db.UpdateSessionBalance("test-3", 400, 100)

	retrieved, _ := db.GetSession("test-3")
	if retrieved.BalanceCKB != 400 {
		t.Errorf("BalanceCKB: expected 400, got %d", retrieved.BalanceCKB)
	}
	if retrieved.SpentCKB != 100 {
		t.Errorf("SpentCKB: expected 100, got %d", retrieved.SpentCKB)
	}
}

func TestDB_SettleSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	session := &Session{
		ID:         "test-4",
		WalletID:   "wallet-4",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 300,
		ExpiresAt:  time.Now().Add(1 * time.Hour),
	}

	db.CreateSession(session)
	db.SettleSession("test-4")

	retrieved, _ := db.GetSession("test-4")
	if retrieved.Status != "settled" {
		t.Errorf("Status: expected settled, got %s", retrieved.Status)
	}
}

func TestDB_ListSessions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.CreateSession(&Session{ID: "s1", WalletID: "w1", Status: "active", ExpiresAt: time.Now().Add(1 * time.Hour)})
	db.CreateSession(&Session{ID: "s2", WalletID: "w2", Status: "active", ExpiresAt: time.Now().Add(1 * time.Hour)})
	db.CreateSession(&Session{ID: "s3", WalletID: "w3", Status: "settled", ExpiresAt: time.Now()})

	active, _ := db.ListSessions("active")
	if len(active) != 2 {
		t.Errorf("Expected 2 active, got %d", len(active))
	}

	all, _ := db.ListSessions("")
	if len(all) != 3 {
		t.Errorf("Expected 3 total, got %d", len(all))
	}
}

func TestDB_CreateAndGetGuestWallet(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wallet := &GuestWallet{
		ID:            "w1",
		Address:       "ckt1test",
		PrivateKeyHex: "0x123",
		Status:        "created",
	}

	if err := db.CreateGuestWallet(wallet); err != nil {
		t.Fatalf("CreateGuestWallet failed: %v", err)
	}

	retrieved, err := db.GetGuestWallet("w1")
	if err != nil {
		t.Fatalf("GetGuestWallet failed: %v", err)
	}

	if retrieved.Address != wallet.Address {
		t.Errorf("Address mismatch")
	}
}

func TestDB_GetGuestWalletByAddress(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wallet := &GuestWallet{
		ID:            "w2",
		Address:       "ckt1unique",
		PrivateKeyHex: "0x456",
		Status:        "created",
	}

	db.CreateGuestWallet(wallet)

	retrieved, err := db.GetGuestWalletByAddress("ckt1unique")
	if err != nil {
		t.Fatalf("GetGuestWalletByAddress failed: %v", err)
	}

	if retrieved.ID != "w2" {
		t.Errorf("ID mismatch")
	}
}

func TestDB_ListPendingWallets(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.CreateGuestWallet(&GuestWallet{ID: "w1", Address: "a1", PrivateKeyHex: "k1", Status: "created"})
	db.CreateGuestWallet(&GuestWallet{ID: "w2", Address: "a2", PrivateKeyHex: "k2", Status: "created"})
	db.CreateGuestWallet(&GuestWallet{ID: "w3", Address: "a3", PrivateKeyHex: "k3", Status: "funded"})

	pending, _ := db.ListPendingWallets()
	if len(pending) != 2 {
		t.Errorf("Expected 2 pending, got %d", len(pending))
	}
}

func TestDB_UpdateWalletFunded(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	wallet := &GuestWallet{ID: "w1", Address: "a1", PrivateKeyHex: "k1", Status: "created"}
	db.CreateGuestWallet(wallet)

	db.UpdateWalletFunded("w1", 500, "session-1")

	retrieved, _ := db.GetGuestWallet("w1")
	if retrieved.Status != "funded" {
		t.Errorf("Status: expected funded, got %s", retrieved.Status)
	}
	if retrieved.BalanceCKB != 500 {
		t.Errorf("BalanceCKB: expected 500, got %d", retrieved.BalanceCKB)
	}
}

func TestDB_GetStats(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	db.CreateSession(&Session{ID: "s1", WalletID: "w1", Status: "active", SpentCKB: 100, ExpiresAt: time.Now().Add(1 * time.Hour)})
	db.CreateSession(&Session{ID: "s2", WalletID: "w2", Status: "active", SpentCKB: 50, ExpiresAt: time.Now().Add(1 * time.Hour)})
	db.CreateSession(&Session{ID: "s3", WalletID: "w3", Status: "settled", SpentCKB: 100, ExpiresAt: time.Now()})

	total, active, earned, err := db.GetStats()
	if err != nil {
		t.Fatalf("GetStats failed: %v", err)
	}

	if total != 3 {
		t.Errorf("Total: expected 3, got %d", total)
	}
	if active != 2 {
		t.Errorf("Active: expected 2, got %d", active)
	}
	if earned != 250 {
		t.Errorf("Earned: expected 250, got %d", earned)
	}
}

func TestDB_ExtendSession(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	session := &Session{
		ID:         "test-ext",
		WalletID:   "wallet-ext",
		Status:     "active",
		FundingCKB: 500,
		BalanceCKB: 500,
		SpentCKB:   0,
		ExpiresAt:  time.Now().Add(30 * time.Minute),
	}

	db.CreateSession(session)
	db.ExtendSession("test-ext", 30, 250)

	retrieved, _ := db.GetSession("test-ext")
	if retrieved.SpentCKB != 250 {
		t.Errorf("SpentCKB: expected 250, got %d", retrieved.SpentCKB)
	}
}
