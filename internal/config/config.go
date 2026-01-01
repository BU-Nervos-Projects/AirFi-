// Package config provides configuration loading for AirFi backend.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration.
type Config struct {
	CKB      CKBConfig      `yaml:"ckb"`
	Guest    GuestConfig    `yaml:"guest"`
	Perun    PerunConfig    `yaml:"perun"`
	Auth     AuthConfig     `yaml:"auth"`
	Server   ServerConfig   `yaml:"server"`
	WiFi     WiFiConfig     `yaml:"wifi"`
	Database DatabaseConfig `yaml:"database"`
	OpenWrt  *OpenWrtConfig `yaml:"openwrt,omitempty"`
}

// CKBConfig holds CKB network settings.
type CKBConfig struct {
	Network    string `yaml:"network"`
	RPCURL     string `yaml:"rpc_url"`
	IndexerURL string `yaml:"indexer_url"`
	PrivateKey string `yaml:"private_key"`
}

// GuestConfig holds guest wallet settings.
type GuestConfig struct {
	PrivateKey string `yaml:"private_key"`
}

// PerunConfig holds Perun channel settings.
type PerunConfig struct {
	ChannelTimeout    time.Duration `yaml:"channel_timeout"`
	FundingTimeout    time.Duration `yaml:"funding_timeout"`
	SettlementTimeout time.Duration `yaml:"settlement_timeout"`
	ChannelSetupCKB   int64         `yaml:"channel_setup_ckb"`
}

// AuthConfig holds authentication settings.
type AuthConfig struct {
	PrivateKeyPath string        `yaml:"private_key_path"`
	PublicKeyPath  string        `yaml:"public_key_path"`
	TokenDuration  time.Duration `yaml:"token_duration"`
}

// ServerConfig holds server settings.
type ServerConfig struct {
	Host              string `yaml:"host"`
	Port              int    `yaml:"port"`
	DashboardPassword string `yaml:"dashboard_password"`
}

// WiFiConfig holds WiFi pricing settings.
type WiFiConfig struct {
	RatePerHour    int64         `yaml:"rate_per_hour"`
	MinSessionTime time.Duration `yaml:"min_session_time"`
	MaxSessionTime time.Duration `yaml:"max_session_time"`
}

// DatabaseConfig holds database settings.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// OpenWrtConfig holds OpenWrt router settings.
type OpenWrtConfig struct {
	Address     string `yaml:"address"`
	Port        int    `yaml:"port"`
	Username    string `yaml:"username"`
	Password    string `yaml:"password"`
	PrivateKey  string `yaml:"private_key"`
	AuthTimeout int    `yaml:"auth_timeout"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		CKB: CKBConfig{
			Network:    "testnet",
			RPCURL:     "https://testnet.ckb.dev/rpc",
			IndexerURL: "https://testnet.ckb.dev/indexer",
		},
		Perun: PerunConfig{
			ChannelTimeout:    1 * time.Hour,
			FundingTimeout:    10 * time.Minute,
			SettlementTimeout: 30 * time.Minute,
			ChannelSetupCKB:   1000,
		},
		Auth: AuthConfig{
			PrivateKeyPath: "./keys/private.pem",
			PublicKeyPath:  "./keys/public.pem",
			TokenDuration:  1 * time.Hour,
		},
		Server: ServerConfig{
			Host:              "0.0.0.0",
			Port:              8080,
			DashboardPassword: "airfi2025",
		},
		WiFi: WiFiConfig{
			RatePerHour:    500,
			MinSessionTime: 5 * time.Minute,
			MaxSessionTime: 24 * time.Hour,
		},
		Database: DatabaseConfig{
			Path: "./airfi.db",
		},
	}
}

// Load reads configuration from a YAML file.
// If the file doesn't exist, it returns the default configuration.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Apply environment variable overrides
	cfg.applyEnvOverrides()

	return cfg, nil
}

// applyEnvOverrides applies environment variable overrides to the config.
func (c *Config) applyEnvOverrides() {
	if v := os.Getenv("HOST_PRIVATE_KEY"); v != "" {
		c.CKB.PrivateKey = v
	}
	if v := os.Getenv("DASHBOARD_PASSWORD"); v != "" {
		c.Server.DashboardPassword = v
	}
	if v := os.Getenv("DB_PATH"); v != "" {
		c.Database.Path = v
	}
	if v := os.Getenv("PORT"); v != "" {
		var port int
		fmt.Sscanf(v, "%d", &port)
		if port > 0 {
			c.Server.Port = port
		}
	}
	if v := os.Getenv("CHANNEL_SETUP_CKB"); v != "" {
		var ckb int64
		fmt.Sscanf(v, "%d", &ckb)
		if ckb > 0 {
			c.Perun.ChannelSetupCKB = ckb
		}
	}
	if v := os.Getenv("OPENWRT_ADDRESS"); v != "" {
		if c.OpenWrt == nil {
			c.OpenWrt = &OpenWrtConfig{}
		}
		c.OpenWrt.Address = v
	}
	if v := os.Getenv("OPENWRT_PORT"); v != "" {
		if c.OpenWrt == nil {
			c.OpenWrt = &OpenWrtConfig{}
		}
		var port int
		fmt.Sscanf(v, "%d", &port)
		if port > 0 {
			c.OpenWrt.Port = port
		}
	}
	if v := os.Getenv("OPENWRT_USERNAME"); v != "" {
		if c.OpenWrt == nil {
			c.OpenWrt = &OpenWrtConfig{}
		}
		c.OpenWrt.Username = v
	}
	if v := os.Getenv("OPENWRT_PASSWORD"); v != "" {
		if c.OpenWrt == nil {
			c.OpenWrt = &OpenWrtConfig{}
		}
		c.OpenWrt.Password = v
	}
	if v := os.Getenv("OPENWRT_PRIVATE_KEY"); v != "" {
		if c.OpenWrt == nil {
			c.OpenWrt = &OpenWrtConfig{}
		}
		c.OpenWrt.PrivateKey = v
	}
	if v := os.Getenv("OPENWRT_AUTH_TIMEOUT"); v != "" {
		if c.OpenWrt == nil {
			c.OpenWrt = &OpenWrtConfig{}
		}
		var timeout int
		fmt.Sscanf(v, "%d", &timeout)
		c.OpenWrt.AuthTimeout = timeout
	}
}

// GetAddress returns the server address string.
func (c *Config) GetAddress() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}
