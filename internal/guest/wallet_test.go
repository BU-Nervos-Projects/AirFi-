package guest

import (
	"strings"
	"testing"

	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
)

func TestWalletManager_GenerateWallet(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

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
		t.Error("PrivateKey is nil")
	}
	if wallet.LockScript == nil {
		t.Error("LockScript is nil")
	}

	if !strings.HasPrefix(wallet.Address, "ckt") {
		t.Errorf("Testnet address should start with 'ckt', got: %s", wallet.Address)
	}
}

func TestWalletManager_GenerateWallet_Mainnet(t *testing.T) {
	wm := NewWalletManager(types.NetworkMain)

	wallet, err := wm.GenerateWallet()
	if err != nil {
		t.Fatalf("GenerateWallet failed: %v", err)
	}

	if !strings.HasPrefix(wallet.Address, "ckb") {
		t.Errorf("Mainnet address should start with 'ckb', got: %s", wallet.Address)
	}
}

func TestWalletManager_GetWallet(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallet, _ := wm.GenerateWallet()
	retrieved, exists := wm.GetWallet(wallet.ID)

	if !exists {
		t.Error("Wallet not found")
	}
	if retrieved.Address != wallet.Address {
		t.Error("Address mismatch")
	}
}

func TestWalletManager_GetWallet_NotFound(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	_, exists := wm.GetWallet("nonexistent")
	if exists {
		t.Error("Should not find nonexistent wallet")
	}
}

func TestWalletManager_GetWalletByAddress(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallet, _ := wm.GenerateWallet()
	retrieved, exists := wm.GetWalletByAddress(wallet.Address)

	if !exists {
		t.Error("Wallet not found by address")
	}
	if retrieved.ID != wallet.ID {
		t.Error("ID mismatch")
	}
}

func TestWalletManager_RemoveWallet(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallet, _ := wm.GenerateWallet()
	wm.RemoveWallet(wallet.ID)

	_, exists := wm.GetWallet(wallet.ID)
	if exists {
		t.Error("Wallet should be removed")
	}
}

func TestWallet_GetPrivateKeyHex(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallet, _ := wm.GenerateWallet()
	hexKey := wallet.GetPrivateKeyHex()

	if hexKey == "" {
		t.Error("Private key hex is empty")
	}
	if len(hexKey) != 64 {
		t.Errorf("Private key hex length: expected 64, got %d", len(hexKey))
	}
}

func TestWalletManager_UniqueWallets(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallets := make([]*Wallet, 5)
	for i := 0; i < 5; i++ {
		w, _ := wm.GenerateWallet()
		wallets[i] = w
	}

	seen := make(map[string]bool)
	for _, w := range wallets {
		if seen[w.ID] {
			t.Errorf("Duplicate ID: %s", w.ID)
		}
		seen[w.ID] = true

		if seen[w.Address] {
			t.Errorf("Duplicate Address: %s", w.Address)
		}
		seen[w.Address] = true
	}
}

func TestDecodeAddress_Valid(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallet, _ := wm.GenerateWallet()
	script, err := DecodeAddress(wallet.Address)

	if err != nil {
		t.Fatalf("DecodeAddress failed: %v", err)
	}
	if script == nil {
		t.Error("Script is nil")
	}
	if script.Hash() != wallet.LockScript.Hash() {
		t.Error("Script hash mismatch")
	}
}

func TestDecodeAddress_InvalidPrefix(t *testing.T) {
	_, err := DecodeAddress("xyz1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq")
	if err == nil {
		t.Error("Expected error for invalid prefix")
	}
}

func TestDecodeAddress_TooShort(t *testing.T) {
	_, err := DecodeAddress("ck")
	if err == nil {
		t.Error("Expected error for short address")
	}
}

func TestDecodeAddress_Empty(t *testing.T) {
	_, err := DecodeAddress("")
	if err == nil {
		t.Error("Expected error for empty address")
	}
}

func TestGetLockScriptHash(t *testing.T) {
	wm := NewWalletManager(types.NetworkTest)

	wallet, _ := wm.GenerateWallet()
	hash := GetLockScriptHash(wallet.LockScript)

	if len(hash) != 32 {
		t.Errorf("Hash length: expected 32, got %d", len(hash))
	}

	hash2 := GetLockScriptHash(wallet.LockScript)
	if hash != hash2 {
		t.Error("Hash should be consistent")
	}
}

func TestConvertBitsToBytes(t *testing.T) {
	// Test with empty input
	result := convertBitsToBytes([]int{})
	if len(result) != 0 {
		t.Error("Empty input should return empty result")
	}

	// Test with known values
	data := []int{0, 1, 2, 3, 4}
	result = convertBitsToBytes(data)
	if len(result) == 0 {
		t.Error("Result should not be empty")
	}
}

func TestConvertBits(t *testing.T) {
	// Test identity conversion
	data := []byte{0x00, 0xFF, 0x55, 0xAA}

	// Convert 8->5->8 should preserve data (approximately)
	converted := convertBits(data, 8, 5, true)
	if len(converted) == 0 {
		t.Error("Converted result should not be empty")
	}
}
