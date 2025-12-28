package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/airfi/airfi-perun-nervous/internal/auth"
)

func TestGenerateKeyPair(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
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

	// Generate and save
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	err = kp.SaveKeys(privateKeyPath, publicKeyPath)
	if err != nil {
		t.Fatalf("SaveKeys failed: %v", err)
	}

	// Verify files exist
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Error("Private key file not created")
	}
	if _, err := os.Stat(publicKeyPath); os.IsNotExist(err) {
		t.Error("Public key file not created")
	}

	// Load and verify
	loadedKP, err := auth.LoadKeyPair(privateKeyPath, publicKeyPath)
	if err != nil {
		t.Fatalf("LoadKeyPair failed: %v", err)
	}

	if loadedKP.PrivateKey == nil {
		t.Error("Loaded PrivateKey is nil")
	}
	if loadedKP.PublicKey == nil {
		t.Error("Loaded PublicKey is nil")
	}
}

func TestLoadOrGenerateKeyPair_Generate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "auth_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	privateKeyPath := filepath.Join(tmpDir, "private.pem")
	publicKeyPath := filepath.Join(tmpDir, "public.pem")

	// Should generate new keys
	kp, err := auth.LoadOrGenerateKeyPair(privateKeyPath, publicKeyPath)
	if err != nil {
		t.Fatalf("LoadOrGenerateKeyPair failed: %v", err)
	}

	if kp.PrivateKey == nil || kp.PublicKey == nil {
		t.Error("Keys not generated properly")
	}

	// Files should be created
	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		t.Error("Private key file not created")
	}
}

func TestLoadOrGenerateKeyPair_Load(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "auth_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	privateKeyPath := filepath.Join(tmpDir, "private.pem")
	publicKeyPath := filepath.Join(tmpDir, "public.pem")

	// First call generates
	kp1, err := auth.LoadOrGenerateKeyPair(privateKeyPath, publicKeyPath)
	if err != nil {
		t.Fatalf("First LoadOrGenerateKeyPair failed: %v", err)
	}

	// Second call should load
	kp2, err := auth.LoadOrGenerateKeyPair(privateKeyPath, publicKeyPath)
	if err != nil {
		t.Fatalf("Second LoadOrGenerateKeyPair failed: %v", err)
	}

	// Keys should be the same
	if kp1.PrivateKey.D.Cmp(kp2.PrivateKey.D) != 0 {
		t.Error("Loaded private key differs from original")
	}
}

func TestJWTService_GenerateAndValidate(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	jwtService := auth.NewJWTService(kp, "airfi-test")

	// Generate token
	token, err := jwtService.GenerateToken("session-123", "channel-456", "AA:BB:CC:DD:EE:FF", "192.168.1.100", 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	if token == "" {
		t.Error("Token is empty")
	}

	// Validate token
	claims, err := jwtService.ValidateToken(token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if claims.SessionID != "session-123" {
		t.Errorf("SessionID mismatch: expected session-123, got %s", claims.SessionID)
	}
	if claims.ChannelID != "channel-456" {
		t.Errorf("ChannelID mismatch: expected channel-456, got %s", claims.ChannelID)
	}
	if claims.MACAddress != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("MACAddress mismatch: expected AA:BB:CC:DD:EE:FF, got %s", claims.MACAddress)
	}
	if claims.IPAddress != "192.168.1.100" {
		t.Errorf("IPAddress mismatch: expected 192.168.1.100, got %s", claims.IPAddress)
	}
}

func TestJWTService_InvalidToken(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	jwtService := auth.NewJWTService(kp, "airfi-test")

	// Try to validate invalid token
	_, err = jwtService.ValidateToken("invalid-token")
	if err == nil {
		t.Error("Expected error for invalid token")
	}
}

func TestJWTService_ExpiredToken(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	jwtService := auth.NewJWTService(kp, "airfi-test")

	// Generate token with very short duration
	token, err := jwtService.GenerateToken("session-123", "channel-456", "", "", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Should be expired
	if !jwtService.IsExpired(token) {
		t.Error("Token should be expired")
	}
}

func TestJWTService_NotExpired(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	jwtService := auth.NewJWTService(kp, "airfi-test")

	// Generate token with long duration
	token, err := jwtService.GenerateToken("session-123", "channel-456", "", "", 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	// Should not be expired
	if jwtService.IsExpired(token) {
		t.Error("Token should not be expired")
	}
}

func TestJWTService_GetRemainingTime(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	jwtService := auth.NewJWTService(kp, "airfi-test")

	duration := 1 * time.Hour
	token, err := jwtService.GenerateToken("session-123", "channel-456", "", "", duration)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	remaining, err := jwtService.GetRemainingTime(token)
	if err != nil {
		t.Fatalf("GetRemainingTime failed: %v", err)
	}

	// Should be close to 1 hour (within 5 seconds tolerance)
	if remaining < duration-5*time.Second || remaining > duration {
		t.Errorf("Remaining time unexpected: %v (expected ~%v)", remaining, duration)
	}
}

func TestJWTService_RefreshToken(t *testing.T) {
	kp, err := auth.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair failed: %v", err)
	}

	jwtService := auth.NewJWTService(kp, "airfi-test")

	// Generate initial token
	token, err := jwtService.GenerateToken("session-123", "channel-456", "AA:BB:CC:DD:EE:FF", "192.168.1.100", 30*time.Minute)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	// Refresh with additional 30 minutes
	refreshedToken, err := jwtService.RefreshToken(token, 30*time.Minute)
	if err != nil {
		t.Fatalf("RefreshToken failed: %v", err)
	}

	if refreshedToken == "" {
		t.Error("Refreshed token is empty")
	}

	// Verify claims are preserved
	claims, err := jwtService.ValidateToken(refreshedToken)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	if claims.SessionID != "session-123" {
		t.Errorf("SessionID not preserved: got %s", claims.SessionID)
	}
	if claims.MACAddress != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("MACAddress not preserved: got %s", claims.MACAddress)
	}
}

func TestJWTService_DifferentKeys(t *testing.T) {
	kp1, _ := auth.GenerateKeyPair()
	kp2, _ := auth.GenerateKeyPair()

	jwtService1 := auth.NewJWTService(kp1, "airfi-test")
	jwtService2 := auth.NewJWTService(kp2, "airfi-test")

	// Generate token with first key
	token, _ := jwtService1.GenerateToken("session-123", "channel-456", "", "", 1*time.Hour)

	// Try to validate with second key (should fail)
	_, err := jwtService2.ValidateToken(token)
	if err == nil {
		t.Error("Expected error when validating with different key")
	}
}
