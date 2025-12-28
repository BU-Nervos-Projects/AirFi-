package tests

import (
	"context"
	"errors"
	"testing"

	"github.com/airfi/airfi-perun-nervous/tests/mocks"
)

func TestMockRouter_AuthorizeMAC(t *testing.T) {
	router := mocks.NewMockRouter()

	err := router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "test")
	if err != nil {
		t.Fatalf("AuthorizeMAC failed: %v", err)
	}

	if !router.IsAuthorized("AA:BB:CC:DD:EE:FF") {
		t.Error("MAC should be authorized")
	}
}

func TestMockRouter_DeauthorizeMAC(t *testing.T) {
	router := mocks.NewMockRouter()

	router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "test")
	router.DeauthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF")

	if router.IsAuthorized("AA:BB:CC:DD:EE:FF") {
		t.Error("MAC should not be authorized after deauth")
	}
}

func TestMockRouter_GetAuthorizedCount(t *testing.T) {
	router := mocks.NewMockRouter()

	if router.GetAuthorizedCount() != 0 {
		t.Error("Initial count should be 0")
	}

	router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "test1")
	router.AuthorizeMAC(context.Background(), "11:22:33:44:55:66", "192.168.1.101", "test2")

	if router.GetAuthorizedCount() != 2 {
		t.Errorf("Count should be 2, got %d", router.GetAuthorizedCount())
	}
}

func TestMockRouter_Reset(t *testing.T) {
	router := mocks.NewMockRouter()

	router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "test")
	router.Reset()

	if router.GetAuthorizedCount() != 0 {
		t.Error("Count should be 0 after reset")
	}
}

func TestMockRouterWithError_AuthorizeError(t *testing.T) {
	expectedErr := errors.New("authorize failed")
	router := mocks.NewMockRouterWithError(expectedErr, nil)

	err := router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "test")
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
}

func TestMockRouterWithError_DeauthorizeError(t *testing.T) {
	expectedErr := errors.New("deauthorize failed")
	router := mocks.NewMockRouterWithError(nil, expectedErr)

	err := router.DeauthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF")
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
}

func TestMockRouterWithTracking_TracksCalls(t *testing.T) {
	router := mocks.NewMockRouterWithTracking()

	router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "comment1")
	router.AuthorizeMAC(context.Background(), "11:22:33:44:55:66", "192.168.1.101", "comment2")
	router.DeauthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF")

	authCalls := router.GetAuthorizeCalls()
	if len(authCalls) != 2 {
		t.Errorf("Expected 2 authorize calls, got %d", len(authCalls))
	}

	if authCalls[0].MAC != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("First call MAC mismatch")
	}
	if authCalls[0].IP != "192.168.1.100" {
		t.Errorf("First call IP mismatch")
	}
	if authCalls[0].Comment != "comment1" {
		t.Errorf("First call comment mismatch")
	}

	deauthCalls := router.GetDeauthorizeCalls()
	if len(deauthCalls) != 1 {
		t.Errorf("Expected 1 deauthorize call, got %d", len(deauthCalls))
	}

	if deauthCalls[0] != "AA:BB:CC:DD:EE:FF" {
		t.Errorf("Deauth call MAC mismatch")
	}
}

func TestValidateMACAddress_Valid(t *testing.T) {
	validMACs := []string{
		"AA:BB:CC:DD:EE:FF",
		"aa:bb:cc:dd:ee:ff",
		"00:11:22:33:44:55",
	}

	for _, mac := range validMACs {
		err := mocks.ValidateMACAddress(mac)
		if err != nil {
			t.Errorf("MAC %s should be valid: %v", mac, err)
		}
	}
}

func TestValidateMACAddress_Invalid(t *testing.T) {
	invalidMACs := []string{
		"AA:BB:CC:DD:EE",      // Too short
		"AA:BB:CC:DD:EE:FF:00", // Too long
		"AABBCCDDEEFF",        // No colons
		"AA-BB-CC-DD-EE-FF",   // Wrong separator
		"",                    // Empty
	}

	for _, mac := range invalidMACs {
		err := mocks.ValidateMACAddress(mac)
		if err == nil {
			t.Errorf("MAC %s should be invalid", mac)
		}
	}
}

func TestMockRouter_CustomAuthorizeFunc(t *testing.T) {
	router := mocks.NewMockRouter()

	customCalled := false
	router.AuthorizeFunc = func(ctx context.Context, mac, ip, comment string) error {
		customCalled = true
		return nil
	}

	router.AuthorizeMAC(context.Background(), "AA:BB:CC:DD:EE:FF", "192.168.1.100", "test")

	if !customCalled {
		t.Error("Custom authorize function should be called")
	}
}

func TestMockRouter_ConcurrentAccess(t *testing.T) {
	router := mocks.NewMockRouter()

	done := make(chan bool, 20)

	// 10 authorize goroutines
	for i := 0; i < 10; i++ {
		go func(idx int) {
			mac := "00:00:00:00:00:" + string(rune('A'+idx))
			router.AuthorizeMAC(context.Background(), mac, "192.168.1.1", "test")
			done <- true
		}(i)
	}

	// 10 deauthorize goroutines
	for i := 0; i < 10; i++ {
		go func(idx int) {
			mac := "00:00:00:00:00:" + string(rune('A'+idx))
			router.DeauthorizeMAC(context.Background(), mac)
			done <- true
		}(i)
	}

	// Wait for all to complete
	for i := 0; i < 20; i++ {
		<-done
	}
}
