// Package main provides the entry point for the AirFi backend server.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/gin-gonic/gin"
	"github.com/nervosnetwork/ckb-sdk-go/v2/indexer"
	"github.com/nervosnetwork/ckb-sdk-go/v2/rpc"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"

	gpchannel "perun.network/go-perun/channel"
	gpclient "perun.network/go-perun/client"
	gpwire "perun.network/go-perun/wire"

	"github.com/airfi/airfi-perun-nervous/internal/auth"
	"github.com/airfi/airfi-perun-nervous/internal/db"
	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/airfi/airfi-perun-nervous/internal/router"
	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

// GuestSession represents an active guest session with their channel client.
type GuestSession struct {
	ID            string
	Client        *perun.ChannelClient
	Channel       *gpclient.Channel
	GuestAddress  string
	FundingAmount *big.Int
	TotalPaid     *big.Int
	CreatedAt     time.Time
	ExpiresAt     time.Time
}

// Server represents the AirFi backend server.
type Server struct {
	hostClient        *perun.ChannelClient
	hostPrivKey       *secp256k1.PrivateKey
	wireBus           *gpwire.LocalBus
	ckbClient         rpc.Client
	jwtService        *auth.JWTService
	db                *db.DB
	walletManager     *guest.WalletManager
	sessions          map[string]*GuestSession // Perun channel sessions (in-memory)
	sessionsMu        sync.RWMutex
	logger            *zap.Logger
	ratePerMin        *big.Int // CKBytes per minute
	dashboardPassword string   // Simple dashboard auth
	router            router.Router
}

func main() {
	// Initialize logger
	logger, err := zap.NewDevelopment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  AirFi WiFi Access Backend")
	fmt.Println("  Real Perun State Channels on CKB Testnet")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Create shared wire bus for all channel communication
	wireBus := gpwire.NewLocalBus()

	// Host wallet (WiFi provider)
	hostPrivKeyHex := os.Getenv("HOST_PRIVATE_KEY")
	if hostPrivKeyHex == "" {
		hostPrivKeyHex = "5ba43817d0634ca9f1620b4f17874f366794f181cd0eb854ea7ff711093b26f3"
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

	// Connect to CKB RPC for broadcasting transactions
	ckbClient, err := rpc.Dial(perun.TestnetRPCURL)
	if err != nil {
		logger.Fatal("failed to connect to CKB RPC", zap.Error(err))
	}

	// Check host balance
	ctx := context.Background()
	balance, _ := hostClient.GetBalance(ctx)
	hostBalanceCKB := float64(balance.Int64()) / 100000000
	fmt.Printf("  Host Balance: %.2f CKB\n", hostBalanceCKB)

	// Warn if host balance is too low for channel operations
	// Host needs: 100 CKB funding + 61 CKB minimum cell + fees ≈ 200 CKB minimum
	if hostBalanceCKB < 200 {
		fmt.Printf("  WARNING: Host balance (%.2f CKB) may be too low for channel operations!\n", hostBalanceCKB)
		fmt.Println("           Recommended minimum: 200 CKB")
		fmt.Println("           Please fund the host wallet from: https://faucet.nervos.org")
	}

	// Ensure host wallet has multiple cells for Perun channel operations
	// This is critical: Perun needs multiple cells for funding transactions
	// Host needs at least 3 cells: 1-2 for funding, 1 for change output
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

	// Initialize JWT service
	keysDir := os.Getenv("KEYS_DIR")
	if keysDir == "" {
		keysDir = "./keys"
	}
	keyPair, err := auth.LoadOrGenerateKeyPair(keysDir+"/private.pem", keysDir+"/public.pem")
	if err != nil {
		logger.Fatal("failed to initialize JWT keys", zap.Error(err))
	}
	jwtService := auth.NewJWTService(keyPair, "airfi-wifi")
	fmt.Println("  JWT Service: Initialized")

	// Dashboard password from env or default
	dashboardPassword := os.Getenv("DASHBOARD_PASSWORD")
	if dashboardPassword == "" {
		dashboardPassword = "airfi2025"
	}

	// Initialize SQLite database
	dbPath := os.Getenv("DB_PATH")

	// Initialize router (OpenWrt/OpenNDS)
	var wifiRouter router.Router
	openwrtAddr := os.Getenv("OPENWRT_ADDRESS")

	if openwrtAddr != "" {
		openwrtPort := 22
		if portStr := os.Getenv("OPENWRT_PORT"); portStr != "" {
			if p, err := strconv.Atoi(portStr); err == nil {
				openwrtPort = p
			}
		}

		authTimeout := 0 // Use OpenNDS default
		if timeoutStr := os.Getenv("OPENWRT_AUTH_TIMEOUT"); timeoutStr != "" {
			if t, err := strconv.Atoi(timeoutStr); err == nil {
				authTimeout = t
			}
		}

		openwrtConfig := router.OpenWrtConfig{
			Address:     openwrtAddr,
			Port:        openwrtPort,
			Username:    os.Getenv("OPENWRT_USERNAME"),
			Password:    os.Getenv("OPENWRT_PASSWORD"),
			PrivateKey:  os.Getenv("OPENWRT_PRIVATE_KEY"),
			AuthTimeout: authTimeout,
		}

		if openwrtConfig.Username == "" {
			openwrtConfig.Username = "root"
		}

		var err error
		wifiRouter, err = router.NewOpenWrtClient(openwrtConfig, logger.Named("openwrt"))
		if err != nil {
			logger.Fatal("failed to create OpenWrt client", zap.Error(err))
		}
		fmt.Printf("  Router: OpenWrt/OpenNDS @ %s:%d\n", openwrtAddr, openwrtPort)

		// Test connection
		if err := wifiRouter.TestConnection(context.Background()); err != nil {
			logger.Warn("OpenWrt connection test failed", zap.Error(err))
			fmt.Printf("  OpenNDS Status: Connection failed - %s\n", err.Error())
		} else {
			fmt.Println("  OpenNDS Status: Connected")
		}
	} else {
		wifiRouter = &router.NoopRouter{}
		fmt.Println("  Router: Not configured (set OPENWRT_ADDRESS to enable)")
	}
	if dbPath == "" {
		dbPath = "./airfi.db"
	}
	database, err := db.Open(dbPath)
	if err != nil {
		logger.Fatal("failed to open database", zap.Error(err))
	}
	defer database.Close()
	fmt.Printf("  Database: SQLite initialized (%s)\n", dbPath)

	// Create wallet manager for guest wallets
	walletMgr := guest.NewWalletManager(types.NetworkTest)
	fmt.Println("  Wallet Manager: Initialized")

	// Create server
	server := &Server{
		hostClient:        hostClient,
		hostPrivKey:       hostPrivKey,
		wireBus:           wireBus,
		ckbClient:         ckbClient,
		jwtService:        jwtService,
		db:                database,
		walletManager:     walletMgr,
		sessions:          make(map[string]*GuestSession),
		logger:            logger,
		ratePerMin:        big.NewInt(833333333), // ~8.33 CKB per minute (500 CKB = 1 hour)
		dashboardPassword: dashboardPassword,
		router:            wifiRouter,
	}

	// Setup proposal handler
	hostClient.HandleProposals(&HostProposalHandler{
		server: server,
		logger: logger.Named("host-handler"),
	})

	// Create Gin router
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(corsMiddleware())

	// Static files and templates
	router.Static("/static", "./web/guest/static")
	router.LoadHTMLGlob("./web/guest/templates/*")

	// Page routes
	router.GET("/", server.handleIndex)
	router.GET("/connect", server.handleConnect)
	router.GET("/session/:sessionId", server.handleSession)
	router.GET("/dashboard", server.handleDashboard)
	router.GET("/dashboard/login", server.handleDashboardLogin)
	router.POST("/dashboard/login", server.handleDashboardLoginPost)
	router.GET("/dashboard/logout", server.handleDashboardLogout)

	// API routes
	api := router.Group("/api/v1")
	{
		api.GET("/wallet", server.handleWalletStatus)
		api.POST("/wallet/guest", server.handleCreateGuestWallet)  // Generate guest wallet
		api.GET("/wallet/guest/:id", server.handleGetGuestWallet)  // Get guest wallet status
		api.POST("/channels/open", server.handleOpenChannel)
		api.GET("/sessions", server.handleListSessions)
		api.GET("/sessions/:sessionId", server.handleGetSession)
		api.GET("/sessions/:sessionId/token", server.handleGetSessionToken)
		api.POST("/sessions/:sessionId/end", server.handleEndSession)
		api.POST("/sessions/:sessionId/extend", server.handleExtendSession)
		api.POST("/sessions/:sessionId/refund", server.handleManualRefund) // Manual refund to specified address
		api.POST("/auth/validate", server.handleValidateToken)
	}

	// Health check
	router.GET("/health", func(c *gin.Context) {
		// Check if CKB connection is working
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		_, err := server.hostClient.GetBalance(ctx)
		connected := err == nil

		c.JSON(http.StatusOK, gin.H{
			"status":    "healthy",
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"connected": connected,
		})
	})

	// Start funding detector (background)
	fundingCtx, fundingCancel := context.WithCancel(context.Background())
	go server.startFundingDetector(fundingCtx)
	fmt.Println("  Funding Detector: Started")

	// Start micropayment processor (background)
	go server.startMicropaymentProcessor(fundingCtx)
	fmt.Println("  Micropayment Processor: Started")

	// Start server
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	fmt.Printf("\n  Server starting on http://localhost%s\n", addr)
	fmt.Println("═══════════════════════════════════════════════════════════════")

	httpServer := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\n  Shutting down...")
	fundingCancel() // Stop background goroutines
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpServer.Shutdown(ctx)
}

// Page handlers
func (s *Server) handleIndex(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{
		"title": "AirFi - WiFi Access",
	})
}

func (s *Server) handleConnect(c *gin.Context) {
	// Capture MAC and IP from OpenNDS captive portal redirect
	// OpenNDS sends: http://portal/?mac=aa:bb:cc:dd:ee:ff&ip=192.168.1.100
	mac := c.Query("mac")
	ip := c.Query("ip")

	c.HTML(http.StatusOK, "connect.html", gin.H{
		"title":      "Connect - AirFi",
		"macAddress": mac,
		"ipAddress":  ip,
	})
}

func (s *Server) handleDashboard(c *gin.Context) {
	// Simple auth check via cookie
	authCookie, err := c.Cookie("airfi_host_auth")
	if err != nil || authCookie != s.dashboardPassword {
		c.Redirect(http.StatusFound, "/dashboard/login")
		return
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"title": "Host Dashboard - AirFi",
	})
}

func (s *Server) handleDashboardLogin(c *gin.Context) {
	c.HTML(http.StatusOK, "dashboard_login.html", gin.H{
		"title": "Login - Host Dashboard",
	})
}

func (s *Server) handleDashboardLoginPost(c *gin.Context) {
	password := c.PostForm("password")

	if password == s.dashboardPassword {
		// Set auth cookie (24 hours)
		c.SetCookie("airfi_host_auth", password, 86400, "/", "", false, true)
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}

	c.HTML(http.StatusOK, "dashboard_login.html", gin.H{
		"title": "Login - Host Dashboard",
		"error": "Invalid password",
	})
}

func (s *Server) handleDashboardLogout(c *gin.Context) {
	c.SetCookie("airfi_host_auth", "", -1, "/", "", false, true)
	c.Redirect(http.StatusFound, "/dashboard/login")
}


func (s *Server) handleSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Check database for session
	dbSession, err := s.db.GetSession(sessionID)
	if err == nil {
		remaining := time.Until(dbSession.ExpiresAt)
		if remaining < 0 {
			remaining = 0
		}
		status := dbSession.Status
		if remaining <= 0 && status == "active" {
			status = "expired"
		}

		// Truncate session ID for display
		displayID := dbSession.ID
		if len(displayID) > 20 {
			displayID = displayID[:20] + "..."
		}

		channelDisplay := "Pending"
		if dbSession.ChannelID != "" {
			if len(dbSession.ChannelID) > 16 {
				channelDisplay = dbSession.ChannelID[:16] + "..."
			} else {
				channelDisplay = dbSession.ChannelID
			}
		}

		c.HTML(http.StatusOK, "session.html", gin.H{
			"title":         "Session - AirFi",
			"remainingTime": formatDuration(remaining),
			"session": gin.H{
				"ID":         displayID,
				"ChannelID":  channelDisplay,
				"BalanceCKB": fmt.Sprintf("%d", dbSession.BalanceCKB),
				"SpentCKB":   fmt.Sprintf("%d", dbSession.SpentCKB),
				"FundingCKB": fmt.Sprintf("%d", dbSession.FundingCKB),
				"Status":     status,
			},
		})
		return
	}

	// Check Perun channel session (in-memory)
	s.sessionsMu.RLock()
	session, exists := s.sessions[sessionID]
	s.sessionsMu.RUnlock()

	if !exists {
		c.Redirect(http.StatusFound, "/")
		return
	}

	remaining := time.Until(session.ExpiresAt)
	if remaining < 0 {
		remaining = 0
	}

	c.HTML(http.StatusOK, "session.html", gin.H{
		"title":         "Session - AirFi",
		"remainingTime": formatDuration(remaining),
		"session": gin.H{
			"ID":         session.ID,
			"ChannelID":  fmt.Sprintf("%x", session.Channel.ID())[:16] + "...",
			"BalanceCKB": fmt.Sprintf("%.0f", float64(session.FundingAmount.Int64()-session.TotalPaid.Int64())/100000000),
			"SpentCKB":   fmt.Sprintf("%.0f", float64(session.TotalPaid.Int64())/100000000),
			"FundingCKB": fmt.Sprintf("%.0f", float64(session.FundingAmount.Int64())/100000000),
			"Status":     "active",
		},
	})
}

// API handlers
func (s *Server) handleWalletStatus(c *gin.Context) {
	balance, err := s.hostClient.GetBalance(c.Request.Context())
	balanceCKB := float64(balance.Int64()) / 100000000

	c.JSON(http.StatusOK, gin.H{
		"address":     s.hostClient.GetAddress(),
		"balance_ckb": balanceCKB,
		"network":     "testnet",
		"connected":   err == nil,
	})
}



// handleListSessions returns all sessions for the Host CLI/Dashboard.
func (s *Server) handleListSessions(c *gin.Context) {
	type sessionInfo struct {
		SessionID     string `json:"session_id"`
		GuestAddress  string `json:"guest_address"`
		BalanceCKB    int64  `json:"balance_ckb"`
		FundingCKB    int64  `json:"funding_ckb"`
		SpentCKB      int64  `json:"spent_ckb"`
		RemainingTime string `json:"remaining_time"`
		Status        string `json:"status"`
		ChannelID     string `json:"channel_id"`
		CreatedAt     string `json:"created_at"`
	}

	sessions := make([]sessionInfo, 0)

	// Get sessions from database
	dbSessions, err := s.db.ListSessions("")
	if err == nil {
		for _, session := range dbSessions {
			status := session.Status
			var remainingTimeStr string

			// For channel_opening status, timer hasn't started yet
			if status == "channel_opening" {
				remainingTimeStr = "-"
			} else {
				remaining := time.Until(session.ExpiresAt)
				if remaining < 0 {
					remaining = 0
				}
				if remaining <= 0 && status == "active" {
					status = "expired"
				}
				remainingTimeStr = formatDuration(remaining)
			}

			sessions = append(sessions, sessionInfo{
				SessionID:     session.ID,
				GuestAddress:  session.GuestAddress,
				BalanceCKB:    session.BalanceCKB,
				FundingCKB:    session.FundingCKB,
				SpentCKB:      session.SpentCKB,
				RemainingTime: remainingTimeStr,
				Status:        status,
				ChannelID:     session.ChannelID,
				CreatedAt:     session.CreatedAt.Format(time.RFC3339),
			})
		}
	}

	// Build a set of session IDs already in the list (from database)
	sessionIDSet := make(map[string]bool)
	for _, s := range sessions {
		sessionIDSet[s.SessionID] = true
	}

	// Add Perun channel sessions (in-memory) only if not already in database list
	s.sessionsMu.RLock()
	for _, session := range s.sessions {
		// Skip if already added from database
		if sessionIDSet[session.ID] {
			continue
		}

		remaining := time.Until(session.ExpiresAt)
		if remaining < 0 {
			remaining = 0
		}
		status := "active"
		if remaining <= 0 {
			status = "expired"
		}

		fundingCKB := session.FundingAmount.Int64() / 100000000
		spentCKB := session.TotalPaid.Int64() / 100000000
		balanceCKB := fundingCKB - spentCKB

		sessions = append(sessions, sessionInfo{
			SessionID:     session.ID,
			GuestAddress:  session.GuestAddress,
			BalanceCKB:    balanceCKB,
			FundingCKB:    fundingCKB,
			SpentCKB:      spentCKB,
			RemainingTime: formatDuration(remaining),
			Status:        status,
			ChannelID:     fmt.Sprintf("%x", session.Channel.ID())[:16],
			CreatedAt:     session.CreatedAt.Format(time.RFC3339),
		})
	}
	s.sessionsMu.RUnlock()

	c.JSON(http.StatusOK, gin.H{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

func (s *Server) handleOpenChannel(c *gin.Context) {
	var req struct {
		GuestAddress  string `json:"guest_address" binding:"required"`
		FundingAmount string `json:"funding_amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Parse funding amount (in CKB)
	fundingCKB, _ := new(big.Int).SetString(req.FundingAmount, 10)
	if fundingCKB == nil || fundingCKB.Sign() <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid funding amount"})
		return
	}
	fundingShannons := new(big.Int).Mul(fundingCKB, big.NewInt(100000000))

	s.logger.Info("opening channel",
		zap.String("guest_address", req.GuestAddress),
		zap.String("funding", fundingCKB.String()),
	)

	// For demo: use the pre-funded guest wallet
	// In production, guest would sign with their own wallet
	guestPrivKeyHex := "afa8e30da03b2dc13a8eccc2546d1d7a36c4a9bbdcdc3e94d18e44cb4eb73b41"
	guestKeyBytes, _ := hex.DecodeString(guestPrivKeyHex)
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	// Create Guest channel client
	guestClient, err := perun.NewChannelClient(&perun.ChannelClientConfig{
		RPCURL:     perun.TestnetRPCURL,
		PrivateKey: guestPrivKey,
		Deployment: perun.GetTestnetDeployment(),
		Logger:     s.logger.Named("guest"),
		WireBus:    s.wireBus,
	})
	if err != nil {
		s.logger.Error("failed to create guest client", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create channel"})
		return
	}

	// Guest proposes channel to Host
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	// Host funding (fixed 100 CKB for demo)
	hostFunding := big.NewInt(10000000000) // 100 CKB

	channel, err := guestClient.ProposeChannel(
		ctx,
		s.hostClient.GetWireAddress(),
		s.hostClient.GetAccount().Address(),
		fundingShannons,
		hostFunding,
	)
	if err != nil {
		guestClient.Close()
		s.logger.Error("failed to open channel", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Calculate session duration from funding
	minutes := new(big.Int).Div(fundingShannons, s.ratePerMin).Int64()
	duration := time.Duration(minutes) * time.Minute

	// Create session
	sessionID := fmt.Sprintf("%x", channel.ID())[:16]
	session := &GuestSession{
		ID:            sessionID,
		Client:        guestClient,
		Channel:       channel,
		GuestAddress:  req.GuestAddress,
		FundingAmount: fundingShannons,
		TotalPaid:     big.NewInt(0),
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(duration),
	}

	s.sessionsMu.Lock()
	s.sessions[sessionID] = session
	s.sessionsMu.Unlock()

	s.logger.Info("channel opened",
		zap.String("session_id", sessionID),
		zap.String("channel_id", fmt.Sprintf("%x", channel.ID())),
	)

	c.JSON(http.StatusOK, gin.H{
		"session_id":     sessionID,
		"channel_id":     fmt.Sprintf("%x", channel.ID()),
		"funding_amount": fundingCKB.String(),
		"duration_mins":  minutes,
	})
}

func (s *Server) handleGetSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Check database for session
	dbSession, err := s.db.GetSession(sessionID)
	if err == nil {
		status := dbSession.Status
		var remainingTimeStr string

		// For channel_opening status, timer hasn't started yet
		if status == "channel_opening" {
			remainingTimeStr = "-"
		} else {
			remaining := time.Until(dbSession.ExpiresAt)
			if remaining < 0 {
				remaining = 0
			}
			if remaining <= 0 && status == "active" {
				status = "expired"
			}
			remainingTimeStr = formatDuration(remaining)
		}

		c.JSON(http.StatusOK, gin.H{
			"session_id":     dbSession.ID,
			"wallet_id":      dbSession.WalletID,
			"channel_id":     dbSession.ChannelID,
			"guest_address":  dbSession.GuestAddress,
			"host_address":   dbSession.HostAddress,
			"funding_ckb":    dbSession.FundingCKB,
			"balance_ckb":    dbSession.BalanceCKB,
			"spent_ckb":      dbSession.SpentCKB,
			"remaining_time": remainingTimeStr,
			"expires_at":     dbSession.ExpiresAt.Format(time.RFC3339),
			"status":         status,
		})
		return
	}

	// Check Perun channel session (in-memory)
	s.sessionsMu.RLock()
	session, exists := s.sessions[sessionID]
	s.sessionsMu.RUnlock()

	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	remaining := time.Until(session.ExpiresAt)
	if remaining < 0 {
		remaining = 0
	}

	status := "active"
	if remaining <= 0 {
		status = "expired"
	}

	fundingCKB := session.FundingAmount.Int64() / 100000000
	spentCKB := session.TotalPaid.Int64() / 100000000
	balanceCKB := fundingCKB - spentCKB

	c.JSON(http.StatusOK, gin.H{
		"session_id":     session.ID,
		"channel_id":     fmt.Sprintf("%x", session.Channel.ID())[:16],
		"guest_address":  session.GuestAddress,
		"funding_ckb":    fundingCKB,
		"balance_ckb":    balanceCKB,
		"spent_ckb":      spentCKB,
		"remaining_time": formatDuration(remaining),
		"status":         status,
	})
}

func (s *Server) handleExtendSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	var req struct {
		Amount string `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Parse amount (in CKB)
	amountCKB, _ := new(big.Int).SetString(req.Amount, 10)
	if amountCKB == nil || amountCKB.Sign() <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount"})
		return
	}

	s.sessionsMu.Lock()
	session, exists := s.sessions[sessionID]
	if !exists {
		s.sessionsMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found or channel not active"})
		return
	}

	amountShannons := new(big.Int).Mul(amountCKB, big.NewInt(100000000))

	// Send payment (off-chain) - this deducts from guest balance and adds to host
	err := session.Client.SendPayment(session.Channel, amountShannons)
	if err != nil {
		s.sessionsMu.Unlock()
		s.logger.Error("extend payment failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Update in-memory session
	session.TotalPaid.Add(session.TotalPaid, amountShannons)
	// Calculate additional minutes: amount in shannons / rate per minute
	additionalMins := new(big.Int).Div(amountShannons, s.ratePerMin).Int64()
	session.ExpiresAt = session.ExpiresAt.Add(time.Duration(additionalMins) * time.Minute)
	s.sessionsMu.Unlock()

	// Update database session
	if err := s.db.ExtendSession(sessionID, additionalMins, amountCKB.Int64()); err != nil {
		s.logger.Error("failed to update session in database", zap.Error(err))
	}

	remaining := time.Until(session.ExpiresAt)

	s.logger.Info("session extended",
		zap.String("session_id", sessionID),
		zap.Int64("amount_ckb", amountCKB.Int64()),
		zap.Int64("additional_minutes", additionalMins),
		zap.Duration("remaining", remaining),
	)

	c.JSON(http.StatusOK, gin.H{
		"session_id":         sessionID,
		"amount_paid_ckb":    amountCKB.Int64(),
		"additional_minutes": additionalMins,
		"remaining_time":     formatDuration(remaining),
		"status":             "active",
	})
}

// handleManualRefund refunds remaining CKB to a specified address.
// Use this when automatic sender detection failed.
func (s *Server) handleManualRefund(c *gin.Context) {
	sessionID := c.Param("sessionId")

	var req struct {
		ToAddress string `json:"to_address" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get wallet by session ID
	wallet, err := s.db.GetWalletBySessionID(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found for session"})
		return
	}

	// Check if already withdrawn
	if wallet.Status == "withdrawn" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "funds already withdrawn"})
		return
	}

	s.logger.Info("manual refund requested",
		zap.String("session_id", sessionID),
		zap.String("wallet_id", wallet.ID),
		zap.String("to_address", req.ToAddress),
	)

	// Load private key
	guestKeyBytes, err := hex.DecodeString(wallet.PrivateKeyHex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load wallet key"})
		return
	}
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	// Get lock script for wallet
	guestLockScript, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode wallet address"})
		return
	}

	// Withdraw to specified address
	withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))
	txHash, err := withdrawer.WithdrawAll(c.Request.Context(), guestPrivKey, guestLockScript, req.ToAddress)
	if err != nil {
		s.logger.Error("manual refund failed",
			zap.Error(err),
			zap.String("session_id", sessionID),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "refund failed",
			"details": err.Error(),
		})
		return
	}

	// Update wallet status
	s.db.UpdateWalletStatus(wallet.ID, "withdrawn")

	s.logger.Info("manual refund successful",
		zap.String("session_id", sessionID),
		zap.String("tx_hash", txHash.Hex()),
		zap.String("to_address", req.ToAddress),
	)

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"tx_hash":    txHash.Hex(),
		"to_address": req.ToAddress,
		"status":     "refunded",
	})
}

// handleGetSessionToken returns the JWT token for a session.
// JWT is only generated after channel is successfully opened (status = "active").
func (s *Server) handleGetSessionToken(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Check database for session
	dbSession, err := s.db.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	// Check if session is expired
	if time.Now().After(dbSession.ExpiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
		return
	}

	// Only generate JWT after channel is open (status = "active")
	if dbSession.Status != "active" {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"error":   "channel not ready",
			"status":  dbSession.Status,
			"message": "Please wait for channel to open before accessing WiFi",
		})
		return
	}

	// Generate fresh JWT token with MAC and IP for device binding
	remaining := time.Until(dbSession.ExpiresAt)
	token, err := s.jwtService.GenerateToken(dbSession.ID, dbSession.ChannelID, dbSession.MACAddress, dbSession.IPAddress, remaining)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id":   dbSession.ID,
		"access_token": token,
		"expires_at":   dbSession.ExpiresAt.Format(time.RFC3339),
		"channel_id":   dbSession.ChannelID,
		"mac_address":  dbSession.MACAddress,
		"ip_address":   dbSession.IPAddress,
	})
}

// handleValidateToken validates a JWT access token.
func (s *Server) handleValidateToken(c *gin.Context) {
	var req struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claims, err := s.jwtService.ValidateToken(req.Token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"valid": false,
			"error": err.Error(),
		})
		return
	}

	// Check if token is expired
	if time.Now().After(claims.ExpiresAt.Time) {
		c.JSON(http.StatusUnauthorized, gin.H{
			"valid":  false,
			"error":  "token expired",
			"claims": claims,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"valid":          true,
		"session_id":     claims.SessionID,
		"channel_id":     claims.ChannelID,
		"mac_address":    claims.MACAddress,
		"ip_address":     claims.IPAddress,
		"expires_at":     claims.ExpiresAt.Time.Format(time.RFC3339),
		"remaining_secs": int(time.Until(claims.ExpiresAt.Time).Seconds()),
	})
}


func (s *Server) handleEndSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	s.sessionsMu.Lock()
	session, exists := s.sessions[sessionID]
	if !exists {
		s.sessionsMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	delete(s.sessions, sessionID)
	s.sessionsMu.Unlock()

	s.logger.Info("settling channel",
		zap.String("session_id", sessionID),
	)

	// Settle channel (on-chain)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	err := session.Client.SettleChannel(ctx, session.Channel)
	if err != nil {
		s.logger.Error("settlement failed", zap.Error(err))
		// Still mark as settled in DB
	} else {
		s.logger.Info("channel settled successfully", zap.String("session_id", sessionID))
	}

	// Update database
	s.db.SettleSession(sessionID)

	// Deauthorize MAC address from router
	dbSession, err := s.db.GetSession(sessionID)
	if err == nil && dbSession.MACAddress != "" {
		if err := s.router.DeauthorizeMAC(ctx, dbSession.MACAddress); err != nil {
			s.logger.Error("failed to deauthorize MAC from router",
				zap.Error(err),
				zap.String("mac", dbSession.MACAddress),
				zap.String("session_id", sessionID),
			)
		} else {
			s.logger.Info("MAC deauthorized from router",
				zap.String("mac", dbSession.MACAddress),
				zap.String("session_id", sessionID),
			)
		}
	}

	session.Client.Close()

	// Channel funds are already returned by Perun settlement
	// Try to withdraw any remaining reserved CKB (best effort)
	go func() {
		withdrawHash, err := s.withdrawToSender(context.Background(), sessionID)
		if err != nil {
			// This is expected in many cases - Perun settlement already handled the funds
			s.logger.Info("auto-withdraw skipped or failed (Perun settlement already returned funds)",
				zap.String("session_id", sessionID),
				zap.String("note", err.Error()),
			)
		} else {
			s.logger.Info("auto-withdraw of reserved CKB successful",
				zap.String("session_id", sessionID),
				zap.String("tx_hash", withdrawHash),
			)
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"session_id": session.ID,
		"status":     "settled",
		"message":    "Channel settled successfully! Your CKB has been returned via Perun settlement.",
	})
}

// HostProposalHandler handles incoming channel proposals on the host side.
type HostProposalHandler struct {
	server *Server
	logger *zap.Logger
}

func (h *HostProposalHandler) HandleProposal(proposal gpclient.ChannelProposal, responder *gpclient.ProposalResponder) {
	h.logger.Info("received channel proposal")

	// Debug: Check host cells before accepting
	ctx := context.Background()
	hostBalance, err := h.server.hostClient.GetBalance(ctx)
	if err != nil {
		h.logger.Warn("failed to check host balance", zap.Error(err))
	} else {
		h.logger.Info("host balance before funding",
			zap.String("balance_shannons", hostBalance.String()),
			zap.Float64("balance_ckb", float64(hostBalance.Int64())/100000000),
		)
	}

	// Debug: Count host cells
	hostLockScript, _ := guest.DecodeAddress(h.server.hostClient.GetAddress())
	cellSplitter := perun.NewCellSplitter(h.server.ckbClient, h.logger)
	cellCount, _ := cellSplitter.CountCells(ctx, hostLockScript)
	h.logger.Info("host cell count before funding", zap.Int("count", cellCount))

	ledgerProposal, ok := proposal.(*gpclient.LedgerChannelProposalMsg)
	if !ok {
		h.logger.Error("expected LedgerChannelProposalMsg")
		return
	}

	accept := ledgerProposal.Accept(h.server.hostClient.GetAccount().Address(), gpclient.WithRandomNonce())

	_, err = responder.Accept(context.Background(), accept)
	if err != nil {
		h.logger.Error("failed to accept proposal", zap.Error(err))
		return
	}

	h.logger.Info("accepted channel proposal")
}

func (h *HostProposalHandler) HandleUpdate(cur *gpchannel.State, next gpclient.ChannelUpdate, responder *gpclient.UpdateResponder) {
	h.logger.Info("received update proposal", zap.Uint64("version", next.State.Version))

	// Accept all updates (in production, verify the update)
	err := responder.Accept(context.Background())
	if err != nil {
		h.logger.Error("failed to accept update", zap.Error(err))
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0:00"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// handleCreateGuestWallet generates a new guest wallet for funding.
func (s *Server) handleCreateGuestWallet(c *gin.Context) {
	// Parse optional MAC and IP from request (sent by frontend from captive portal redirect)
	var req struct {
		MACAddress string `json:"mac_address"`
		IPAddress  string `json:"ip_address"`
	}
	c.ShouldBindJSON(&req) // Ignore error - fields are optional

	// Generate new wallet
	wallet, err := s.walletManager.GenerateWallet()
	if err != nil {
		s.logger.Error("failed to generate wallet", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate wallet"})
		return
	}

	// Store in database
	// Note: Minimum is 150 CKB to cover channel funding + tx fees + change output
	dbWallet := &db.GuestWallet{
		ID:            wallet.ID,
		Address:       wallet.Address,
		PrivateKeyHex: wallet.GetPrivateKeyHex(),
		FundingCKB:    150, // Minimum CKB for Perun channel
		BalanceCKB:    0,
		CreatedAt:     time.Now(),
		Status:        "created",
		MACAddress:    req.MACAddress,
		IPAddress:     req.IPAddress,
	}

	if err := s.db.CreateGuestWallet(dbWallet); err != nil {
		s.logger.Error("failed to save wallet", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save wallet"})
		return
	}

	s.logger.Info("guest wallet created",
		zap.String("wallet_id", wallet.ID),
		zap.String("address", wallet.Address),
		zap.String("mac_address", req.MACAddress),
	)

	c.JSON(http.StatusOK, gin.H{
		"wallet_id":    wallet.ID,
		"address":      wallet.Address,
		"funding_ckb":  61,
		"status":       "created",
		"host_address": s.hostClient.GetAddress(),
	})
}

// handleGetGuestWallet returns the status of a guest wallet.
func (s *Server) handleGetGuestWallet(c *gin.Context) {
	walletID := c.Param("id")

	// Get from database
	wallet, err := s.db.GetGuestWallet(walletID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
		return
	}

	// Check on-chain balance if still waiting for funding
	if wallet.Status == "created" {
		balance, err := s.checkWalletBalance(c.Request.Context(), wallet.Address)
		if err == nil && balance >= 150*100000000 { // 150 CKB minimum in shannons
			balanceCKB := balance / 100000000

			// Create session
			sessionID := s.createSessionFromWallet(wallet, balanceCKB)

			// Update wallet status
			s.db.UpdateWalletFunded(walletID, balanceCKB, sessionID)
			wallet.Status = "funded"
			wallet.BalanceCKB = balanceCKB
			wallet.SessionID = sessionID

			// Open Perun channel in background
			go s.openChannelForSession(context.Background(), wallet, sessionID, balanceCKB)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"wallet_id":   wallet.ID,
		"address":     wallet.Address,
		"balance_ckb": wallet.BalanceCKB,
		"status":      wallet.Status,
		"session_id":  wallet.SessionID,
		"created_at":  wallet.CreatedAt.Format(time.RFC3339),
	})
}

// checkWalletBalance queries the on-chain balance for an address.
func (s *Server) checkWalletBalance(ctx context.Context, address string) (int64, error) {
	// Parse address to get lock script
	lockScript, err := guest.DecodeAddress(address)
	if err != nil {
		s.logger.Error("failed to decode address", zap.Error(err), zap.String("address", address))
		return 0, fmt.Errorf("failed to decode address: %w", err)
	}

	s.logger.Debug("checking wallet balance",
		zap.String("address", address),
		zap.String("code_hash", lockScript.CodeHash.Hex()),
	)

	// Query indexer for cells capacity
	searchKey := &indexer.SearchKey{
		Script:     lockScript,
		ScriptType: types.ScriptTypeLock,
	}

	capacity, err := s.ckbClient.GetCellsCapacity(ctx, searchKey)
	if err != nil {
		s.logger.Error("failed to get cells capacity", zap.Error(err))
		return 0, fmt.Errorf("failed to query indexer: %w", err)
	}

	s.logger.Info("wallet balance checked",
		zap.String("address", address),
		zap.Uint64("capacity", capacity.Capacity),
	)

	return int64(capacity.Capacity), nil
}

// createSessionFromWallet creates a new session when a wallet is funded.
func (s *Server) createSessionFromWallet(wallet *db.GuestWallet, balanceCKB int64) string {
	// Generate session ID
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	sessionID := hex.EncodeToString(idBytes)

	// Calculate usable funding (total - reserved for channel operations)
	// ~1500 CKB is reserved for Perun channel setup (channel cell + token cell + fees)
	const reservedCKB int64 = 1500
	usableCKB := balanceCKB - reservedCKB
	if usableCKB < 0 {
		usableCKB = 0
	}

	// Create session in database
	// OPTIMISTIC OPENING: Timer starts NOW, WiFi access granted immediately
	// Duration based on usable CKB at rate of ~8.33 CKB per minute (500 CKB = 1 hour)
	now := time.Now()
	sessionDuration := time.Duration(usableCKB*100000000/833333333) * time.Minute
	session := &db.Session{
		ID:           sessionID,
		WalletID:     wallet.ID,
		GuestAddress: wallet.Address,
		HostAddress:  s.hostClient.GetAddress(),
		FundingCKB:   balanceCKB,
		BalanceCKB:   usableCKB,
		SpentCKB:     0,
		CreatedAt:    now,
		ExpiresAt:    now.Add(sessionDuration), // Timer starts NOW (optimistic)
		Status:       "active",                  // Active immediately (optimistic)
		MACAddress:   wallet.MACAddress,
		IPAddress:    wallet.IPAddress,
	}

	if err := s.db.CreateSession(session); err != nil {
		s.logger.Error("failed to create session", zap.Error(err))
		return ""
	}

	s.logger.Info("session created from wallet",
		zap.String("session_id", sessionID),
		zap.String("wallet_id", wallet.ID),
		zap.Int64("funded_ckb", balanceCKB),
		zap.Int64("usable_ckb", usableCKB),
		zap.Int64("reserved_ckb", reservedCKB),
		zap.String("mac_address", wallet.MACAddress),
	)

	return sessionID
}

// startFundingDetector runs a background loop to detect wallet funding.
func (s *Server) startFundingDetector(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.checkPendingWallets(ctx)
		}
	}
}

// checkPendingWallets checks all pending wallets for funding.
func (s *Server) checkPendingWallets(ctx context.Context) {
	wallets, err := s.db.ListPendingWallets()
	if err != nil {
		s.logger.Error("failed to list pending wallets", zap.Error(err))
		return
	}

	for _, wallet := range wallets {
		balance, err := s.checkWalletBalance(ctx, wallet.Address)
		if err != nil {
			continue
		}

		if balance >= 150*100000000 { // 150 CKB minimum for Perun channel
			balanceCKB := balance / 100000000

			// Detect sender address for later refund (with retry)
			go s.detectAndSaveSenderAddress(wallet.ID, wallet.Address)

			sessionID := s.createSessionFromWallet(wallet, balanceCKB)
			if sessionID != "" {
				s.db.UpdateWalletFunded(wallet.ID, balanceCKB, sessionID)
				s.logger.Info("wallet funded, session created",
					zap.String("wallet_id", wallet.ID),
					zap.Int64("balance", balanceCKB),
					zap.String("session_id", sessionID),
				)

				// OPTIMISTIC OPENING: Authorize MAC immediately for instant WiFi access
				// Channel will open in background - if it fails, we'll deauthorize
				if wallet.MACAddress != "" {
					comment := fmt.Sprintf("AirFi session (optimistic): %s", sessionID)
					if err := s.router.AuthorizeMAC(ctx, wallet.MACAddress, wallet.IPAddress, comment); err != nil {
						s.logger.Error("failed to authorize MAC (optimistic)",
							zap.Error(err),
							zap.String("mac", wallet.MACAddress),
							zap.String("session_id", sessionID),
						)
					} else {
						s.logger.Info("MAC authorized (optimistic) - WiFi access granted immediately",
							zap.String("mac", wallet.MACAddress),
							zap.String("ip", wallet.IPAddress),
							zap.String("session_id", sessionID),
						)
					}
				}

				// Open Perun channel in background
				go s.openChannelForSession(ctx, wallet, sessionID, balanceCKB)
			}
		}
	}
}

// openChannelForSession opens a Perun payment channel for a funded session.
func (s *Server) openChannelForSession(ctx context.Context, wallet *db.GuestWallet, sessionID string, balanceCKB int64) {
	s.logger.Info("opening Perun channel for session",
		zap.String("session_id", sessionID),
		zap.Int64("funding_ckb", balanceCKB),
	)

	// Load guest private key from wallet
	guestKeyBytes, err := hex.DecodeString(wallet.PrivateKeyHex)
	if err != nil {
		s.logger.Error("failed to decode guest private key", zap.Error(err))
		return
	}
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	// Verify the key produces the expected address
	pubKeyBytes := guestPrivKey.PubKey().SerializeCompressed()
	s.logger.Info("reconstructed private key",
		zap.String("key_hex", wallet.PrivateKeyHex[:16]+"..."),
		zap.String("pubkey_prefix", fmt.Sprintf("0x%x...", pubKeyBytes[:8])),
	)

	// Get lock script for cell operations
	guestLockScript, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		s.logger.Error("failed to decode guest address", zap.Error(err))
		s.db.UpdateSessionStatus(sessionID, "channel_failed")
		return
	}

	// CRITICAL: Ensure wallet has at least 4 cells for Perun operations
	// The Perun SDK needs:
	// - 1 cell for channel token
	// - 1-2 cells for funding inputs
	// - 1 cell for change (transaction balancing requires additional capacity)
	// - 1 extra cell as buffer for final balancing
	s.logger.Info("ensuring wallet has minimum cells for Perun operation")
	cellSplitter := perun.NewCellSplitter(s.ckbClient, s.logger.Named("cell-splitter"))
	if err := cellSplitter.EnsureMinimumCells(ctx, guestPrivKey, guestLockScript, 4); err != nil {
		s.logger.Error("failed to prepare wallet cells", zap.Error(err))
		s.db.UpdateSessionStatus(sessionID, "cell_preparation_failed")
		return
	}
	guestCellCount, _ := cellSplitter.CountCells(ctx, guestLockScript)
	s.logger.Info("wallet cell preparation complete", zap.Int("cell_count", guestCellCount))

	// Create Guest channel client
	guestClient, err := perun.NewChannelClient(&perun.ChannelClientConfig{
		RPCURL:     perun.TestnetRPCURL,
		PrivateKey: guestPrivKey,
		Deployment: perun.GetTestnetDeployment(),
		Logger:     s.logger.Named("guest-" + sessionID[:8]),
		WireBus:    s.wireBus,
	})
	if err != nil {
		s.logger.Error("failed to create guest client", zap.Error(err))
		s.db.UpdateSessionStatus(sessionID, "channel_failed")
		return
	}

	// Debug: compare wallet address with Perun client address
	s.logger.Info("address comparison",
		zap.String("wallet_address", wallet.Address),
		zap.String("perun_address", guestClient.GetAddress()),
		zap.Bool("match", wallet.Address == guestClient.GetAddress()),
	)

	s.logger.Info("wallet lock script",
		zap.String("code_hash", guestLockScript.CodeHash.Hex()),
		zap.String("hash_type", string(guestLockScript.HashType)),
		zap.String("args", fmt.Sprintf("0x%x", guestLockScript.Args)),
	)

	// Check Perun client balance after cell split
	s.logger.Info("querying perun balance after cell preparation...")
	perunBalance, err := guestClient.GetBalance(ctx)
	if err != nil {
		s.logger.Warn("failed to get perun balance", zap.Error(err))
	} else {
		s.logger.Info("perun client balance",
			zap.String("balance_shannons", perunBalance.String()),
			zap.Float64("balance_ckb", float64(perunBalance.Int64())/100000000),
		)
	}

	// Guest funding in shannons
	// The Perun library requires:
	// - 1 cell for channel token (61 CKB consumed but not counted in iterator balance)
	// - Iterator must have enough cells for: funding + change cell capacity
	// - Minimum change cell = 61 CKB
	// - PFLS minimum capacity = 80 CKB
	// Channel cell needs significant CKB for lock + type + state data storage
	// Based on testing: need ~1500 CKB reserved for channel operations
	// Minimum 2000 CKB total to have meaningful WiFi time after channel setup
	const minBalanceForChannel = int64(2000)
	if balanceCKB < minBalanceForChannel {
		s.logger.Error("insufficient balance for channel - minimum 2000 CKB required",
			zap.Int64("balance", balanceCKB),
			zap.Int64("minimum_required", minBalanceForChannel),
		)
		s.db.UpdateSessionStatus(sessionID, "insufficient_funds")
		guestClient.Close()
		return
	}

	// Reserve for channel cell + token cell + change cell + buffer
	// Testing showed 1500 CKB reserved works reliably
	reservedCKB := int64(1500)
	fundingCKB := balanceCKB - reservedCKB
	if fundingCKB < 42 { // PFLS minimum capacity is ~41 CKB (4100000032 shannons)
		s.logger.Error("insufficient balance for channel after reserving for operations",
			zap.Int64("balance", balanceCKB),
			zap.Int64("reserved", reservedCKB),
			zap.Int64("available_for_funding", fundingCKB),
		)
		s.db.UpdateSessionStatus(sessionID, "insufficient_funds")
		guestClient.Close()
		return
	}
	guestFunding := big.NewInt(fundingCKB * 100000000)
	s.logger.Info("calculated funding amount",
		zap.Int64("balance_ckb", balanceCKB),
		zap.Int64("reserved_ckb", reservedCKB),
		zap.Int64("funding_ckb", fundingCKB),
	)

	// Host funding (matching for demo)
	hostFunding := big.NewInt(10000000000) // 100 CKB

	// Update session status
	s.db.UpdateSessionStatus(sessionID, "channel_opening")

	// Propose channel to Host
	channelCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	channel, err := guestClient.ProposeChannel(
		channelCtx,
		s.hostClient.GetWireAddress(),
		s.hostClient.GetAccount().Address(),
		guestFunding,
		hostFunding,
	)
	if err != nil {
		guestClient.Close()
		s.logger.Error("failed to open channel", zap.Error(err))
		s.db.UpdateSessionStatus(sessionID, "channel_failed")

		// OPTIMISTIC OPENING: Deauthorize MAC since channel failed
		// User was given WiFi access optimistically, now revoke it
		if wallet.MACAddress != "" {
			s.logger.Warn("revoking optimistic WiFi access due to channel failure",
				zap.String("session_id", sessionID),
				zap.String("mac", wallet.MACAddress),
			)
			if err := s.router.DeauthorizeMAC(ctx, wallet.MACAddress); err != nil {
				s.logger.Error("failed to deauthorize MAC after channel failure",
					zap.Error(err),
					zap.String("mac", wallet.MACAddress),
				)
			}
		}
		return
	}

	// Get channel ID
	channelID := fmt.Sprintf("%x", channel.ID())

	// Update session with channel ID (timer already started with optimistic opening)
	if err := s.db.UpdateSessionChannel(sessionID, channelID, "active"); err != nil {
		s.logger.Error("failed to update session channel", zap.Error(err), zap.String("session_id", sessionID))
	} else {
		s.logger.Info("channel opened successfully",
			zap.String("session_id", sessionID),
			zap.String("channel_id", channelID),
		)
	}
	if err := s.db.UpdateWalletStatus(wallet.ID, "channel_open"); err != nil {
		s.logger.Error("failed to update wallet status", zap.Error(err), zap.String("wallet_id", wallet.ID))
	}

	// MAC already authorized earlier (optimistic opening)
	// No need to authorize again

	// Store in-memory for micropayment processing
	sessionDuration := time.Duration(fundingCKB*100000000/833333333) * time.Minute
	guestSession := &GuestSession{
		ID:            sessionID,
		Client:        guestClient,
		Channel:       channel,
		GuestAddress:  wallet.Address,
		FundingAmount: guestFunding,
		TotalPaid:     big.NewInt(0),
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(sessionDuration), // Use the same calculated duration
	}

	s.sessionsMu.Lock()
	s.sessions[sessionID] = guestSession
	s.sessionsMu.Unlock()

	s.logger.Info("Perun channel opened",
		zap.String("session_id", sessionID),
		zap.String("channel_id", channelID),
		zap.Int64("guest_funding", balanceCKB),
	)
}

// startMicropaymentProcessor runs a background loop to process micropayments.
func (s *Server) startMicropaymentProcessor(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second) // Every minute
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processMicropayments(ctx)
		}
	}
}

// processMicropayments deducts 1 CKB per minute from all active sessions.
func (s *Server) processMicropayments(ctx context.Context) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	for sessionID, session := range s.sessions {
		// Check if session is still valid
		if time.Now().After(session.ExpiresAt) {
			s.logger.Info("session expired, settling channel",
				zap.String("session_id", sessionID),
			)

			// Settle channel
			go s.settleExpiredSession(ctx, session)
			delete(s.sessions, sessionID)
			continue
		}

		// Calculate how much balance remains
		remaining := new(big.Int).Sub(session.FundingAmount, session.TotalPaid)
		if remaining.Cmp(s.ratePerMin) < 0 {
			s.logger.Info("insufficient balance, settling channel",
				zap.String("session_id", sessionID),
			)

			go s.settleExpiredSession(ctx, session)
			delete(s.sessions, sessionID)
			continue
		}

		// Send micropayment (1 CKB = 1 minute)
		err := session.Client.SendPayment(session.Channel, s.ratePerMin)
		if err != nil {
			s.logger.Error("micropayment failed",
				zap.String("session_id", sessionID),
				zap.Error(err),
			)
			continue
		}

		// Update session
		session.TotalPaid.Add(session.TotalPaid, s.ratePerMin)
		spentCKB := session.TotalPaid.Int64() / 100000000
		balanceCKB := (session.FundingAmount.Int64() - session.TotalPaid.Int64()) / 100000000

		// Update database
		s.db.UpdateSessionBalance(sessionID, balanceCKB, spentCKB)

		s.logger.Debug("micropayment processed",
			zap.String("session_id", sessionID),
			zap.Int64("spent_ckb", spentCKB),
			zap.Int64("balance_ckb", balanceCKB),
		)
	}
}

// settleExpiredSession settles a channel when session expires.
func (s *Server) settleExpiredSession(ctx context.Context, session *GuestSession) {
	settleCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err := session.Client.SettleChannel(settleCtx, session.Channel)
	if err != nil {
		s.logger.Error("failed to settle channel",
			zap.String("session_id", session.ID),
			zap.Error(err),
		)
	} else {
		s.logger.Info("channel settled",
			zap.String("session_id", session.ID),
		)
	}

	// Update database
	s.db.SettleSession(session.ID)

	// Deauthorize MAC address from router for expired session
	dbSession, err := s.db.GetSession(session.ID)
	if err == nil && dbSession.MACAddress != "" {
		if err := s.router.DeauthorizeMAC(ctx, dbSession.MACAddress); err != nil {
			s.logger.Error("failed to deauthorize MAC from router",
				zap.Error(err),
				zap.String("mac", dbSession.MACAddress),
				zap.String("session_id", session.ID),
			)
		} else {
			s.logger.Info("MAC deauthorized from router",
				zap.String("mac", dbSession.MACAddress),
				zap.String("session_id", session.ID),
			)
		}
	}

	session.Client.Close()

	// Channel funds are already returned by Perun settlement
	// Try to withdraw any remaining reserved CKB (best effort)
	go func() {
		withdrawHash, err := s.withdrawToSender(context.Background(), session.ID)
		if err != nil {
			s.logger.Info("auto-withdraw skipped for expired session (Perun settlement already returned funds)",
				zap.String("session_id", session.ID),
				zap.String("note", err.Error()),
			)
		} else {
			s.logger.Info("auto-withdraw of reserved CKB successful for expired session",
				zap.String("session_id", session.ID),
				zap.String("tx_hash", withdrawHash),
			)
		}
	}()
}

// detectAndSaveSenderAddress detects the sender address with retries.
// CKB indexer might not have indexed the transaction immediately.
func (s *Server) detectAndSaveSenderAddress(walletID, walletAddress string) {
	withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))

	// Retry with increasing delays
	delays := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}

	for i, delay := range delays {
		time.Sleep(delay)

		senderAddr, err := withdrawer.GetSenderAddress(context.Background(), walletAddress, types.NetworkTest)
		if err != nil {
			s.logger.Warn("sender detection attempt failed",
				zap.String("wallet_id", walletID),
				zap.Int("attempt", i+1),
				zap.Error(err),
			)
			continue
		}

		// Success - save the sender address
		s.db.UpdateWalletSenderAddress(walletID, senderAddr)
		s.logger.Info("sender address detected and saved",
			zap.String("wallet_id", walletID),
			zap.String("sender_address", senderAddr),
			zap.Int("attempt", i+1),
		)
		return
	}

	s.logger.Error("failed to detect sender address after all retries",
		zap.String("wallet_id", walletID),
	)
}

// withdrawToSender withdraws remaining CKB from guest wallet to the original sender.
func (s *Server) withdrawToSender(ctx context.Context, sessionID string) (string, error) {
	// Get wallet by session ID
	wallet, err := s.db.GetWalletBySessionID(sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet: %w", err)
	}

	s.logger.Info("starting refund process",
		zap.String("session_id", sessionID),
		zap.String("wallet_id", wallet.ID),
		zap.String("wallet_address", wallet.Address),
		zap.String("sender_address", wallet.SenderAddress),
	)

	// If sender address not found, try to detect it now
	if wallet.SenderAddress == "" {
		s.logger.Info("sender address not found, attempting detection...",
			zap.String("wallet_id", wallet.ID),
		)

		withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))
		senderAddr, err := withdrawer.GetSenderAddress(ctx, wallet.Address, types.NetworkTest)
		if err != nil {
			return "", fmt.Errorf("no sender address found and detection failed: %w", err)
		}

		// Save for future use
		s.db.UpdateWalletSenderAddress(wallet.ID, senderAddr)
		wallet.SenderAddress = senderAddr

		s.logger.Info("sender address detected during refund",
			zap.String("wallet_id", wallet.ID),
			zap.String("sender_address", senderAddr),
		)
	}

	// Load private key
	guestKeyBytes, err := hex.DecodeString(wallet.PrivateKeyHex)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	// Get lock script for wallet
	guestLockScript, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		return "", fmt.Errorf("failed to decode wallet address: %w", err)
	}

	withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))

	// Retry withdrawal with increasing wait times
	// Settlement might take a while to confirm on CKB testnet
	waitTimes := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}
	var lastErr error

	for i, waitTime := range waitTimes {
		s.logger.Info("waiting for settlement to confirm before withdrawal...",
			zap.String("session_id", sessionID),
			zap.Int("attempt", i+1),
			zap.Duration("wait_time", waitTime),
		)
		time.Sleep(waitTime)

		// Try withdrawal
		txHash, err := withdrawer.WithdrawAll(ctx, guestPrivKey, guestLockScript, wallet.SenderAddress)
		if err != nil {
			lastErr = err
			s.logger.Warn("withdrawal attempt failed, will retry",
				zap.String("session_id", sessionID),
				zap.Int("attempt", i+1),
				zap.Error(err),
			)
			continue
		}

		// Success!
		s.db.UpdateWalletStatus(wallet.ID, "withdrawn")
		s.logger.Info("refund successful",
			zap.String("session_id", sessionID),
			zap.String("tx_hash", txHash.Hex()),
			zap.String("to_address", wallet.SenderAddress),
		)
		return txHash.Hex(), nil
	}

	return "", fmt.Errorf("failed to withdraw after %d attempts: %w", len(waitTimes), lastErr)
}

