// Package main provides the entry point for the AirFi backend server.
package main

import (
	"context"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/gin-gonic/gin"
	"github.com/nervosnetwork/ckb-sdk-go/v2/rpc"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"

	gpwire "perun.network/go-perun/wire"

	"github.com/airfi/airfi-perun-nervous/internal/auth"
	"github.com/airfi/airfi-perun-nervous/internal/db"
	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/airfi/airfi-perun-nervous/internal/perun"
	"github.com/airfi/airfi-perun-nervous/internal/router"
)

// Server represents the AirFi backend server.
type Server struct {
	hostClient        *perun.ChannelClient
	hostPrivKey       *secp256k1.PrivateKey
	hostLockScript    *types.Script
	wireBus           *gpwire.LocalBus
	ckbClient         rpc.Client
	jwtService        *auth.JWTService
	db                *db.DB
	walletManager     *guest.WalletManager
	sessions          map[string]*GuestSession
	sessionsMu        sync.RWMutex
	logger            *zap.Logger
	ratePerMin        *big.Int
	channelSetupCKB   int64
	dashboardPassword string
	router            router.Router
}

// ServerConfig holds configuration for creating a new server.
type ServerConfig struct {
	HostClient        *perun.ChannelClient
	HostPrivKey       *secp256k1.PrivateKey
	HostLockScript    *types.Script
	WireBus           *gpwire.LocalBus
	CKBClient         rpc.Client
	JWTService        *auth.JWTService
	DB                *db.DB
	WalletManager     *guest.WalletManager
	Logger            *zap.Logger
	RatePerHour       int64
	ChannelSetupCKB   int64
	DashboardPassword string
	Router            router.Router
}

// NewServer creates a new AirFi server instance.
func NewServer(cfg *ServerConfig) *Server {
	// Convert CKB per hour to shannons per minute
	ratePerMinShannons := (cfg.RatePerHour * 100000000) / 60

	// Default channel setup CKB if not specified
	channelSetupCKB := cfg.ChannelSetupCKB
	if channelSetupCKB <= 0 {
		channelSetupCKB = 1000
	}

	return &Server{
		hostClient:        cfg.HostClient,
		hostPrivKey:       cfg.HostPrivKey,
		hostLockScript:    cfg.HostLockScript,
		wireBus:           cfg.WireBus,
		ckbClient:         cfg.CKBClient,
		jwtService:        cfg.JWTService,
		db:                cfg.DB,
		walletManager:     cfg.WalletManager,
		sessions:          make(map[string]*GuestSession),
		logger:            cfg.Logger,
		ratePerMin:        big.NewInt(ratePerMinShannons),
		channelSetupCKB:   channelSetupCKB,
		dashboardPassword: cfg.DashboardPassword,
		router:            cfg.Router,
	}
}

// Run starts the HTTP server and background workers.
func (s *Server) Run(ctx context.Context, addr string) error {
	// Setup proposal handler
	s.hostClient.HandleProposals(&HostProposalHandler{
		server: s,
		logger: s.logger.Named("host-handler"),
	})

	// Create Gin router
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(corsMiddleware())

	// Static files and templates
	r.Static("/static", "./web/guest/static")
	r.LoadHTMLGlob("./web/guest/templates/*")

	// Setup routes
	s.setupRoutes(r)

	// Start background workers
	go s.startFundingDetector(ctx)
	go s.startMicropaymentProcessor(ctx)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Start server in goroutine
	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// setupRoutes configures all HTTP routes.
func (s *Server) setupRoutes(r *gin.Engine) {
	// Page routes
	r.GET("/", s.handleIndex)
	r.GET("/connect", s.handleConnect)
	r.GET("/session/:sessionId", s.handleSession)
	r.GET("/dashboard", s.handleDashboard)
	r.GET("/dashboard/login", s.handleDashboardLogin)
	r.POST("/dashboard/login", s.handleDashboardLoginPost)
	r.GET("/dashboard/logout", s.handleDashboardLogout)

	// API routes
	api := r.Group("/api/v1")
	{
		api.GET("/wallet", s.handleWalletStatus)
		api.POST("/wallet/guest", s.handleCreateGuestWallet)
		api.GET("/wallet/guest/:id", s.handleGetGuestWallet)
		api.POST("/channels/open", s.handleOpenChannel)
		api.GET("/sessions", s.handleListSessions)
		api.GET("/sessions/:sessionId", s.handleGetSession)
		api.GET("/sessions/:sessionId/token", s.handleGetSessionToken)
		api.POST("/sessions/:sessionId/end", s.handleEndSession)
		api.POST("/sessions/:sessionId/extend", s.handleExtendSession)
		api.POST("/sessions/:sessionId/refund", s.handleManualRefund)
		api.POST("/auth/validate", s.handleValidateToken)
		api.GET("/settings", s.handleGetSettings)
		api.PUT("/settings/rate", s.handleUpdateRate)
	}

	// Health check
	r.GET("/health", s.handleHealth)
}

// updateRatePerMin updates the in-memory rate per minute from the hourly rate.
func (s *Server) updateRatePerMin(ratePerHour int64) {
	ratePerMinShannons := (ratePerHour * 100000000) / 60
	s.ratePerMin = big.NewInt(ratePerMinShannons)
}
