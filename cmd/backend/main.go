// Package main provides the entry point for the AirFi backend server.
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/nervosnetwork/ckb-sdk-go/v2/rpc"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"

	gpwire "perun.network/go-perun/wire"

	"github.com/airfi/airfi-perun-nervous/internal/auth"
	"github.com/airfi/airfi-perun-nervous/internal/config"
	"github.com/airfi/airfi-perun-nervous/internal/db"
	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/airfi/airfi-perun-nervous/internal/perun"
	"github.com/airfi/airfi-perun-nervous/internal/router"
)

func main() {
	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	// Load configuration
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./config/config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  AirFi WiFi Access Backend")
	fmt.Println("  Real Perun State Channels on CKB Testnet")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Create shared wire bus for all channel communication
	wireBus := gpwire.NewLocalBus()

	// Host wallet (WiFi provider) - from config
	hostPrivKeyHex := cfg.CKB.PrivateKey
	if hostPrivKeyHex == "" {
		hostPrivKeyHex = "0x5ba43817d0634ca9f1620b4f17874f366794f181cd0eb854ea7ff711093b26f3"
	}
	// Remove 0x prefix if present
	if len(hostPrivKeyHex) > 2 && hostPrivKeyHex[:2] == "0x" {
		hostPrivKeyHex = hostPrivKeyHex[2:]
	}
	hostKeyBytes, _ := hex.DecodeString(hostPrivKeyHex)
	hostPrivKey := secp256k1.PrivKeyFromBytes(hostKeyBytes)

	// Create Host channel client
	fmt.Println("\n  Initializing Host channel client...")
	hostClient, err := perun.NewChannelClient(&perun.ChannelClientConfig{
		RPCURL:     perun.TestnetRPCURL,
		PrivateKey: hostPrivKey,
		Deployment: perun.GetTestnetDeployment(),
		Logger:     logger.Named("host"),
		WireBus:    wireBus,
	})
	if err != nil {
		logger.Fatal("failed to create Host client", zap.Error(err))
	}
	defer hostClient.Close()

	fmt.Printf("  Host Address: %s\n", hostClient.GetAddress())

	// Connect to CKB RPC
	ckbClient, err := rpc.Dial(perun.TestnetRPCURL)
	if err != nil {
		logger.Fatal("failed to connect to CKB RPC", zap.Error(err))
	}

	// Check host balance
	ctx := context.Background()
	balance, _ := hostClient.GetBalance(ctx)
	hostBalanceCKB := float64(balance.Int64()) / 100000000
	fmt.Printf("  Host Balance: %.2f CKB\n", hostBalanceCKB)

	if hostBalanceCKB < 200 {
		fmt.Printf("  WARNING: Host balance (%.2f CKB) may be too low for channel operations!\n", hostBalanceCKB)
		fmt.Println("           Recommended minimum: 200 CKB")
		fmt.Println("           Please fund from: https://faucet.nervos.org")
	}

	// Prepare host wallet cells
	fmt.Println("  Preparing Host wallet cells for Perun...")
	hostLockScript, err := guest.DecodeAddress(hostClient.GetAddress())
	if err != nil {
		logger.Fatal("failed to decode host address", zap.Error(err))
	}
	hostCellSplitter := perun.NewCellSplitter(ckbClient, logger.Named("host-cell-splitter"))
	if err := hostCellSplitter.EnsureMinimumCells(ctx, hostPrivKey, hostLockScript, 3); err != nil {
		logger.Fatal("failed to prepare host wallet cells", zap.Error(err))
	}
	hostCellCount, _ := hostCellSplitter.CountCells(ctx, hostLockScript)
	fmt.Printf("  Host wallet cells ready (count: %d)\n", hostCellCount)

	// Initialize JWT service - from config
	keyPair, err := auth.LoadOrGenerateKeyPair(cfg.Auth.PrivateKeyPath, cfg.Auth.PublicKeyPath)
	if err != nil {
		logger.Fatal("failed to initialize JWT keys", zap.Error(err))
	}
	jwtService := auth.NewJWTService(keyPair, "airfi-wifi")
	fmt.Println("  JWT Service: Initialized")

	// Dashboard password - from config
	dashboardPassword := cfg.Server.DashboardPassword
	if dashboardPassword == "" {
		dashboardPassword = "airfi2025"
	}

	// Initialize database - from config
	database, err := db.Open(cfg.Database.Path)
	if err != nil {
		logger.Fatal("failed to open database", zap.Error(err))
	}
	defer database.Close()
	fmt.Printf("  Database: SQLite initialized (%s)\n", cfg.Database.Path)

	// Initialize router (OpenWrt/OpenNDS) - from config
	wifiRouter := initializeRouter(cfg, logger)

	// Create wallet manager
	walletMgr := guest.NewWalletManager(types.NetworkTest)
	fmt.Println("  Wallet Manager: Initialized")

	// Load rate from database (or use config default)
	ratePerHour, err := database.GetRatePerHour()
	if err != nil {
		ratePerHour = cfg.WiFi.RatePerHour
	}
	fmt.Printf("  Rate: %d CKB/hour (%.2f CKB/min)\n", ratePerHour, float64(ratePerHour)/60)
	fmt.Printf("  Channel Setup: %d CKB (reserved)\n", cfg.Perun.ChannelSetupCKB)

	// Create server
	server := NewServer(&ServerConfig{
		HostClient:        hostClient,
		HostPrivKey:       hostPrivKey,
		HostLockScript:    hostLockScript,
		WireBus:           wireBus,
		CKBClient:         ckbClient,
		JWTService:        jwtService,
		DB:                database,
		WalletManager:     walletMgr,
		Logger:            logger,
		RatePerHour:       ratePerHour,
		ChannelSetupCKB:   cfg.Perun.ChannelSetupCKB,
		DashboardPassword: dashboardPassword,
		Router:            wifiRouter,
	})

	// Get server address - from config
	addr := fmt.Sprintf(":%d", cfg.Server.Port)

	fmt.Println("  Funding Detector: Started")
	fmt.Println("  Micropayment Processor: Started")
	fmt.Printf("\n  Server starting on http://localhost%s\n", addr)
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())

	// Handle graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		fmt.Println("\n  Shutting down...")
		cancel()
	}()

	// Run server
	if err := server.Run(ctx, addr); err != nil {
		logger.Error("server error", zap.Error(err))
	}
}

// initializeRouter creates the WiFi router client based on configuration.
func initializeRouter(cfg *config.Config, logger *zap.Logger) router.Router {
	// Check if OpenWrt is configured
	if cfg.OpenWrt == nil || cfg.OpenWrt.Address == "" {
		fmt.Println("  Router: Not configured (set openwrt.address in config to enable)")
		return &router.NoopRouter{}
	}

	openwrtPort := cfg.OpenWrt.Port
	if openwrtPort == 0 {
		openwrtPort = 22
	}

	openwrtConfig := router.OpenWrtConfig{
		Address:     cfg.OpenWrt.Address,
		Port:        openwrtPort,
		Username:    cfg.OpenWrt.Username,
		Password:    cfg.OpenWrt.Password,
		PrivateKey:  cfg.OpenWrt.PrivateKey,
		AuthTimeout: cfg.OpenWrt.AuthTimeout,
	}

	if openwrtConfig.Username == "" {
		openwrtConfig.Username = "root"
	}

	wifiRouter, err := router.NewOpenWrtClient(openwrtConfig, logger.Named("openwrt"))
	if err != nil {
		logger.Fatal("failed to create OpenWrt client", zap.Error(err))
	}
	fmt.Printf("  Router: OpenWrt/OpenNDS @ %s:%d\n", cfg.OpenWrt.Address, openwrtPort)

	if err := wifiRouter.TestConnection(context.Background()); err != nil {
		logger.Warn("OpenWrt connection test failed", zap.Error(err))
		fmt.Printf("  OpenNDS Status: Connection failed - %s\n", err.Error())
	} else {
		fmt.Println("  OpenNDS Status: Connected")
	}

	return wifiRouter
}
