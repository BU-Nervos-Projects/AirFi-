package tests

import (
	"strings"
	"testing"

	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
)

func TestWalletManager_GenerateWallet(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	if wallet.ID == "" {
		t.Error("Wallet ID is empty")
	}
	if wallet.Address == "" {
		t.Error("Wallet Address is empty")
	}
	if wallet.PrivateKey == nil {
		t.Error("Wallet PrivateKey is nil")
	}
	if wallet.LockScript == nil {
		t.Error("Wallet LockScript is nil")
	}

	// Testnet address should start with "ckt"
	if !strings.HasPrefix(wallet.Address, "ckt") {
		t.Errorf("Testnet address should start with 'ckt', got: %s", wallet.Address)
	}
}

func TestWalletManager_GenerateWallet_Mainnet(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkMain)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	// Mainnet address should start with "ckb"
	if !strings.HasPrefix(wallet.Address, "ckb") {
		t.Errorf("Mainnet address should start with 'ckb', got: %s", wallet.Address)
	}
}

func TestWalletManager_GetWallet(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	// Get by ID
	retrieved, exists := wm.GetWallet(wallet.ID)
	if !exists {
		t.Error("Wallet not found by ID")
	}
	if retrieved.Address != wallet.Address {
		t.Errorf("Address mismatch: expected %s, got %s", wallet.Address, retrieved.Address)
	}
}

func TestWalletManager_GetWalletByAddress(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	// Get by address
	retrieved, exists := wm.GetWalletByAddress(wallet.Address)
	if !exists {
		t.Error("Wallet not found by address")
	}
	if retrieved.ID != wallet.ID {
		t.Errorf("ID mismatch: expected %s, got %s", wallet.ID, retrieved.ID)
	}
}

func TestWalletManager_GetWallet_NotFound(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	_, exists := wm.GetWallet("nonexistent")
	if exists {
		t.Error("Should not find nonexistent wallet")
	}
}

func TestWalletManager_RemoveWallet(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	// Verify exists
	_, exists := wm.GetWallet(wallet.ID)
	if !exists {
		t.Error("Wallet should exist before removal")
	}

	// Remove
	wm.RemoveWallet(wallet.ID)

	// Verify removed
	_, exists = wm.GetWallet(wallet.ID)
	if exists {
		t.Error("Wallet should not exist after removal")
	}
}

func TestWallet_GetPrivateKeyHex(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	hexKey := wallet.GetPrivateKeyHex()
	if hexKey == "" {
		t.Error("Private key hex is empty")
	}

	// Should be 64 hex characters (32 bytes)
	if len(hexKey) != 64 {
		t.Errorf("Private key hex length should be 64, got %d", len(hexKey))
	}
}

func TestWalletManager_MultipleWallets(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	// Generate multiple wallets
	wallets := make([]*guest.Wallet, 5)
	for i := 0; i < 5; i++ {
		wallet, err := wm.GenerateWallet()
		if err != nil {
			t.Fatalf("GenerateWallet %d failed: %v", i, err)
		}
		wallets[i] = wallet
	}

	// Verify all are unique
	seen := make(map[string]bool)
	for _, w := range wallets {
		if seen[w.ID] {
			t.Errorf("Duplicate wallet ID: %s", w.ID)
		}
		seen[w.ID] = true

		if seen[w.Address] {
			t.Errorf("Duplicate wallet address: %s", w.Address)
		}
		seen[w.Address] = true
	}

	// Verify all can be retrieved
	for _, w := range wallets {
		retrieved, exists := wm.GetWallet(w.ID)
		if !exists {
			t.Errorf("Wallet %s not found", w.ID)
		}
		if retrieved.Address != w.Address {
			t.Errorf("Address mismatch for wallet %s", w.ID)
		}
	}
}

func TestDecodeAddress_Testnet(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	// Generate a wallet to get a valid address
	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	// Decode the address
	script, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		t.Fatalf("DecodeAddress failed: %v", err)
	}

	if script == nil {
		t.Error("Decoded script is nil")
	}

	// Script hash should match original
	if script.Hash() != wallet.LockScript.Hash() {
		t.Error("Decoded script hash doesn't match original")
	}
}

func TestDecodeAddress_InvalidPrefix(t *testing.T) {
	_, err := guest.DecodeAddress("xyz1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq")
	if err == nil {
		t.Error("Expected error for invalid prefix")
	}
}

func TestDecodeAddress_TooShort(t *testing.T) {
	_, err := guest.DecodeAddress("ck")
	if err == nil {
		t.Error("Expected error for short address")
	}
}

func TestDecodeAddress_Empty(t *testing.T) {
	_, err := guest.DecodeAddress("")
	if err == nil {
		t.Error("Expected error for empty address")
	}
}

func TestGetLockScriptHash(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	hash := guest.GetLockScriptHash(wallet.LockScript)

	// Hash should be 32 bytes
	if len(hash) != 32 {
		t.Errorf("Hash length should be 32, got %d", len(hash))
	}

	// Hash should be consistent
	hash2 := guest.GetLockScriptHash(wallet.LockScript)
	if hash != hash2 {
		t.Error("Hash should be consistent")
	}
}

func TestWalletManager_ConcurrentAccess(t *testing.T) {
	wm := guest.NewWalletManager(types.NetworkTest)

	// Generate wallets concurrently
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, err := wm.GenerateWallet()
			if err != nil {
				t.Errorf("GenerateWallet failed: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
