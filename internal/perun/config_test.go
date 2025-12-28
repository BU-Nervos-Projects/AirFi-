package perun

import (
	"testing"
	"time"
)

func TestDefaultTestnetConfig(t *testing.T) {
	config := DefaultTestnetConfig()

	if config.Network != NetworkTestnet {
		t.Errorf("Network: expected testnet, got %s", config.Network)
	}
	if config.RPCURL != "https://testnet.ckb.dev/rpc" {
		t.Errorf("RPCURL: expected testnet URL, got %s", config.RPCURL)
	}
	if config.ChannelTimeout != 1*time.Hour {
		t.Errorf("ChannelTimeout: expected 1h, got %v", config.ChannelTimeout)
	}
	if config.AssetID != "CKBytes" {
		t.Errorf("AssetID: expected CKBytes, got %s", config.AssetID)
	}
}

func TestDefaultDevnetConfig(t *testing.T) {
	config := DefaultDevnetConfig()

	if config.Network != NetworkDevnet {
		t.Errorf("Network: expected devnet, got %s", config.Network)
	}
	if config.RPCURL != "http://localhost:8114" {
		t.Errorf("RPCURL: expected localhost, got %s", config.RPCURL)
	}
}

func TestConfig_Validate_Valid(t *testing.T) {
	config := &Config{
		RPCURL:         "https://testnet.ckb.dev/rpc",
		ChannelTimeout: 1 * time.Hour,
	}

	if err := config.Validate(); err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
}

func TestConfig_Validate_MissingRPCURL(t *testing.T) {
	config := &Config{
		RPCURL:         "",
		ChannelTimeout: 1 * time.Hour,
	}

	if err := config.Validate(); err == nil {
		t.Error("Expected error for missing RPC URL")
	}
}

func TestConfig_Validate_ZeroTimeout(t *testing.T) {
	config := &Config{
		RPCURL:         "https://testnet.ckb.dev/rpc",
		ChannelTimeout: 0,
	}

	if err := config.Validate(); err == nil {
		t.Error("Expected error for zero timeout")
	}
}

func TestConfig_Validate_NegativeTimeout(t *testing.T) {
	config := &Config{
		RPCURL:         "https://testnet.ckb.dev/rpc",
		ChannelTimeout: -1 * time.Hour,
	}

	if err := config.Validate(); err == nil {
		t.Error("Expected error for negative timeout")
	}
}

func TestConfigError(t *testing.T) {
	err := ErrInvalidConfig("test message")

	expected := "perun config error: test message"
	if err.Error() != expected {
		t.Errorf("Error message: expected '%s', got '%s'", expected, err.Error())
	}
}

func TestNetworkType_Values(t *testing.T) {
	tests := []struct {
		network  NetworkType
		expected string
	}{
		{NetworkTestnet, "testnet"},
		{NetworkMainnet, "mainnet"},
		{NetworkDevnet, "devnet"},
	}

	for _, tt := range tests {
		if string(tt.network) != tt.expected {
			t.Errorf("NetworkType: expected %s, got %s", tt.expected, string(tt.network))
		}
	}
}
