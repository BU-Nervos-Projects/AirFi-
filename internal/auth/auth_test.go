package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateKeyPair(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	if kp.PrivateKey == nil {
		t.Error("PrivateKey is nil")
	}
	if kp.PublicKey == nil {
		t.Error("PublicKey is nil")
	}
}

func TestSaveAndLoadKeys(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "auth_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	privateKeyPath := filepath.Join(tmpDir, "private.pem")
	publicKeyPath := filepath.Join(tmpDir, "public.pem")

	kp, _ := GenerateKeyPair()
	kp.SaveKeys(privateKeyPath, publicKeyPath)

	loadedKP, err := LoadKeyPair(privateKeyPath, publicKeyPath)
	if err != nil {
		t.Fatalf("LoadKeyPair failed: %v", err)
	}

	if loadedKP.PrivateKey.D.Cmp(kp.PrivateKey.D) != 0 {
		t.Error("Private keys don't match")
	}
}

func TestLoadOrGenerateKeyPair(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "auth_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	privateKeyPath := filepath.Join(tmpDir, "private.pem")
	publicKeyPath := filepath.Join(tmpDir, "public.pem")

	// First call generates
	kp1, _ := LoadOrGenerateKeyPair(privateKeyPath, publicKeyPath)

	// Second call loads
	kp2, _ := LoadOrGenerateKeyPair(privateKeyPath, publicKeyPath)

	if kp1.PrivateKey.D.Cmp(kp2.PrivateKey.D) != 0 {
		t.Error("Keys should be the same")
	}
}

func TestJWTService_GenerateAndValidate(t *testing.T) {
	kp, _ := GenerateKeyPair()
	svc := NewJWTService(kp, "test-issuer")

	token, err := svc.GenerateToken("sess-1", "chan-1", "AA:BB:CC:DD:EE:FF", "192.168.1.1", 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	claims, err := svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if claims.SessionID != "sess-1" {
		t.Errorf("SessionID: expected sess-1, got %s", claims.SessionID)
	}
	if claims.ChannelID != "chan-1" {
		t.Errorf("ChannelID: expected chan-1, got %s", claims.ChannelID)
	}
	if claims.MACAddress != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("MACAddress mismatch")
	}
}

func TestJWTService_InvalidToken(t *testing.T) {
	kp, _ := GenerateKeyPair()
	svc := NewJWTService(kp, "test-issuer")

	_, err := svc.ValidateToken("invalid-token")
	if err == nil {
		t.Error("Expected error for invalid token")
	}
}

func TestJWTService_IsExpired(t *testing.T) {
	kp, _ := GenerateKeyPair()
	svc := NewJWTService(kp, "test-issuer")

	// Short-lived token
	token, _ := svc.GenerateToken("sess-1", "chan-1", "", "", 1*time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	if !svc.IsExpired(token) {
		t.Error("Token should be expired")
	}

	// Long-lived token
	token2, _ := svc.GenerateToken("sess-2", "chan-2", "", "", 1*time.Hour)
	if svc.IsExpired(token2) {
		t.Error("Token should not be expired")
	}
}

func TestJWTService_GetRemainingTime(t *testing.T) {
	kp, _ := GenerateKeyPair()
	svc := NewJWTService(kp, "test-issuer")

	token, _ := svc.GenerateToken("sess-1", "chan-1", "", "", 1*time.Hour)

	remaining, err := svc.GetRemainingTime(token)
	if err != nil {
		t.Fatalf("GetRemainingTime failed: %v", err)
	}

	if remaining < 59*time.Minute {
		t.Errorf("Remaining time too short: %v", remaining)
	}
}

func TestJWTService_RefreshToken(t *testing.T) {
	kp, _ := GenerateKeyPair()
	svc := NewJWTService(kp, "test-issuer")

	token, _ := svc.GenerateToken("sess-1", "chan-1", "AA:BB:CC:DD:EE:FF", "192.168.1.1", 30*time.Minute)

	refreshed, err := svc.RefreshToken(token, 30*time.Minute)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	claims, _ := svc.ValidateToken(refreshed)
	if claims.SessionID != "sess-1" {
		t.Error("Session ID not preserved")
	}
	if claims.MACAddress != "AA:BB:CC:DD:EE:FF" {
		t.Error("MAC address not preserved")
	}
}

func TestJWTService_DifferentKeys(t *testing.T) {
	kp1, _ := GenerateKeyPair()
	kp2, _ := GenerateKeyPair()

	svc1 := NewJWTService(kp1, "test")
	svc2 := NewJWTService(kp2, "test")

	token, _ := svc1.GenerateToken("sess-1", "chan-1", "", "", 1*time.Hour)

	_, err := svc2.ValidateToken(token)
	if err == nil {
		t.Error("Should fail with different key")
	}
}

func TestNewJWTServiceFromKeys(t *testing.T) {
	kp, _ := GenerateKeyPair()
	svc := NewJWTServiceFromKeys(kp.PrivateKey, kp.PublicKey, "test")

	token, err := svc.GenerateToken("sess-1", "chan-1", "", "", 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	_, err = svc.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
}
