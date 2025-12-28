package tests

import (
	"testing"
	"time"

	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

func TestDefaultTestnetConfig(t *testing.T) {
	config := perun.DefaultTestnetConfig()

	if config.Network != perun.NetworkTestnet {
		t.Errorf("Network: expected %s, got %s", perun.NetworkTestnet, config.Network)
	}
	if config.RPCURL != "https://testnet.ckb.dev/rpc" {
		t.Errorf("RPCURL: expected testnet URL, got %s", config.RPCURL)
	}
	if config.IndexerURL != "https://testnet.ckb.dev/indexer" {
		t.Errorf("IndexerURL: expected testnet URL, got %s", config.IndexerURL)
	}
	if config.ChannelTimeout != 1*time.Hour {
		t.Errorf("ChannelTimeout: expected 1h, got %v", config.ChannelTimeout)
	}
	if config.FundingTimeout != 10*time.Minute {
		t.Errorf("FundingTimeout: expected 10m, got %v", config.FundingTimeout)
	}
	if config.AssetID != "CKBytes" {
		t.Errorf("AssetID: expected CKBytes, got %s", config.AssetID)
	}
}

func TestDefaultDevnetConfig(t *testing.T) {
	config := perun.DefaultDevnetConfig()

	if config.Network != perun.NetworkDevnet {
		t.Errorf("Network: expected %s, got %s", perun.NetworkDevnet, config.Network)
	}
	if config.RPCURL != "http://localhost:8114" {
		t.Errorf("RPCURL: expected localhost, got %s", config.RPCURL)
	}
	if config.IndexerURL != "http://localhost:8116" {
		t.Errorf("IndexerURL: expected localhost, got %s", config.IndexerURL)
	}
}

func TestConfigValidate_Valid(t *testing.T) {
	config := &perun.Config{
		RPCURL:         "https://testnet.ckb.dev/rpc",
		ChannelTimeout: 1 * time.Hour,
	}

	err := config.Validate()
	if err != nil {
		t.Errorf("Expected no error for valid config, got: %v", err)
	}
}

func TestConfigValidate_MissingRPCURL(t *testing.T) {
	config := &perun.Config{
		RPCURL:         "",
		ChannelTimeout: 1 * time.Hour,
	}

	err := config.Validate()
	if err == nil {
		t.Error("Expected error for missing RPC URL")
	}
}

func TestConfigValidate_InvalidChannelTimeout(t *testing.T) {
	config := &perun.Config{
		RPCURL:         "https://testnet.ckb.dev/rpc",
		ChannelTimeout: 0,
	}

	err := config.Validate()
	if err == nil {
		t.Error("Expected error for zero channel timeout")
	}
}

func TestConfigValidate_NegativeChannelTimeout(t *testing.T) {
	config := &perun.Config{
		RPCURL:         "https://testnet.ckb.dev/rpc",
		ChannelTimeout: -1 * time.Hour,
	}

	err := config.Validate()
	if err == nil {
		t.Error("Expected error for negative channel timeout")
	}
}

func TestConfigError_Message(t *testing.T) {
	err := perun.ErrInvalidConfig("test error message")

	expected := "perun config error: test error message"
	if err.Error() != expected {
		t.Errorf("Error message: expected '%s', got '%s'", expected, err.Error())
	}
}

func TestNetworkType_Constants(t *testing.T) {
	tests := []struct {
		network  perun.NetworkType
		expected string
	}{
		{perun.NetworkTestnet, "testnet"},
		{perun.NetworkMainnet, "mainnet"},
		{perun.NetworkDevnet, "devnet"},
	}

	for _, tt := range tests {
		if string(tt.network) != tt.expected {
			t.Errorf("NetworkType: expected %s, got %s", tt.expected, string(tt.network))
		}
	}
}
