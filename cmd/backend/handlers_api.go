package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

// handleWalletStatus returns the host wallet status.
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

// handleListSessions returns all sessions.
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

	// Build set of session IDs already in list
	sessionIDSet := make(map[string]bool)
	for _, sess := range sessions {
		sessionIDSet[sess.SessionID] = true
	}

	// Add Perun channel sessions (in-memory) if not in database
	s.sessionsMu.RLock()
	for _, session := range s.sessions {
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

// handleGetSession returns a specific session.
func (s *Server) handleGetSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	// Check database
	dbSession, err := s.db.GetSession(sessionID)
	if err == nil {
		status := dbSession.Status
		var remainingTimeStr string

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

	// Check in-memory sessions
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

// handleExtendSession extends a session with additional payment.
func (s *Server) handleExtendSession(c *gin.Context) {
	sessionID := c.Param("sessionId")

	var req struct {
		Amount string `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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

	err := session.Client.SendPayment(session.Channel, amountShannons)
	if err != nil {
		s.sessionsMu.Unlock()
		s.logger.Error("extend payment failed", zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	session.TotalPaid.Add(session.TotalPaid, amountShannons)
	additionalMins := new(big.Int).Div(amountShannons, s.ratePerMin).Int64()
	session.ExpiresAt = session.ExpiresAt.Add(time.Duration(additionalMins) * time.Minute)
	s.sessionsMu.Unlock()

	if err := s.db.ExtendSession(sessionID, additionalMins, amountCKB.Int64()); err != nil {
		s.logger.Error("failed to update session in database", zap.Error(err))
	}

	remaining := time.Until(session.ExpiresAt)

	s.logger.Info("session extended",
		zap.String("session_id", sessionID),
		zap.Int64("amount_ckb", amountCKB.Int64()),
		zap.Int64("additional_minutes", additionalMins),
	)

	c.JSON(http.StatusOK, gin.H{
		"session_id":         sessionID,
		"amount_paid_ckb":    amountCKB.Int64(),
		"additional_minutes": additionalMins,
		"remaining_time":     formatDuration(remaining),
		"status":             "active",
	})
}

// handleEndSession ends a session and settles the channel.
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

	s.logger.Info("ending session - settlement will run in background",
		zap.String("session_id", sessionID),
	)

	// Update status to settling
	s.db.UpdateSessionStatus(sessionID, "settling")

	// Deauthorize MAC immediately
	dbSession, err := s.db.GetSession(sessionID)
	if err == nil && dbSession.MACAddress != "" {
		if err := s.router.DeauthorizeMAC(context.Background(), dbSession.MACAddress); err != nil {
			s.logger.Error("failed to deauthorize MAC",
				zap.Error(err),
				zap.String("mac", dbSession.MACAddress),
			)
		} else {
			s.logger.Info("MAC deauthorized", zap.String("mac", dbSession.MACAddress))
		}
	}

	// Run settlement in background
	go s.settleSessionInBackground(session)

	c.JSON(http.StatusOK, gin.H{
		"session_id": session.ID,
		"status":     "settling",
		"message":    "Disconnected! Channel settlement is processing in background.",
	})
}

// handleManualRefund refunds remaining CKB to a specified address.
func (s *Server) handleManualRefund(c *gin.Context) {
	sessionID := c.Param("sessionId")

	var req struct {
		ToAddress string `json:"to_address" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	wallet, err := s.db.GetWalletBySessionID(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found for session"})
		return
	}

	if wallet.Status == "withdrawn" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "funds already withdrawn"})
		return
	}

	s.logger.Info("manual refund requested",
		zap.String("session_id", sessionID),
		zap.String("to_address", req.ToAddress),
	)

	guestKeyBytes, err := hex.DecodeString(wallet.PrivateKeyHex)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load wallet key"})
		return
	}
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	guestLockScript, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode wallet address"})
		return
	}

	withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))
	txHash, err := withdrawer.WithdrawAll(c.Request.Context(), guestPrivKey, guestLockScript, req.ToAddress)
	if err != nil {
		s.logger.Error("manual refund failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "refund failed",
			"details": err.Error(),
		})
		return
	}

	s.db.UpdateWalletStatus(wallet.ID, "withdrawn")

	s.logger.Info("manual refund successful", zap.String("tx_hash", txHash.Hex()))

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"tx_hash":    txHash.Hex(),
		"to_address": req.ToAddress,
		"status":     "refunded",
	})
}

// handleGetSessionToken returns the JWT token for a session.
func (s *Server) handleGetSessionToken(c *gin.Context) {
	sessionID := c.Param("sessionId")

	dbSession, err := s.db.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}

	if time.Now().After(dbSession.ExpiresAt) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
		return
	}

	if dbSession.Status != "active" {
		c.JSON(http.StatusPreconditionFailed, gin.H{
			"error":   "channel not ready",
			"status":  dbSession.Status,
			"message": "Please wait for channel to open before accessing WiFi",
		})
		return
	}

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

// handleGetSettings returns settings (public - used by pricing display).
func (s *Server) handleGetSettings(c *gin.Context) {
	ratePerHour, err := s.db.GetRatePerHour()
	if err != nil {
		ratePerHour = 500
	}

	// Minimum = channel setup + rate per hour
	minimumCKB := s.channelSetupCKB + ratePerHour

	c.JSON(http.StatusOK, gin.H{
		"rate_per_hour":     ratePerHour,
		"channel_setup_ckb": s.channelSetupCKB,
		"minimum_ckb":       minimumCKB,
	})
}

// handleUpdateRate updates the rate per hour.
func (s *Server) handleUpdateRate(c *gin.Context) {
	authCookie, err := c.Cookie("airfi_host_auth")
	if err != nil || authCookie != s.dashboardPassword {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}

	var req struct {
		RatePerHour int64 `json:"rate_per_hour"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	if req.RatePerHour < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "rate must be at least 1 CKB per hour"})
		return
	}

	if err := s.db.SetRatePerHour(req.RatePerHour); err != nil {
		s.logger.Error("failed to set rate", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update rate"})
		return
	}

	s.updateRatePerMin(req.RatePerHour)

	s.logger.Info("rate updated", zap.Int64("rate", req.RatePerHour))
	c.JSON(http.StatusOK, gin.H{
		"rate_per_hour": req.RatePerHour,
		"message":       "Rate updated successfully",
	})
}

// handleOpenChannel opens a new payment channel (demo endpoint).
func (s *Server) handleOpenChannel(c *gin.Context) {
	var req struct {
		GuestAddress  string `json:"guest_address" binding:"required"`
		FundingAmount string `json:"funding_amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

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

	// Demo: use pre-funded guest wallet
	guestPrivKeyHex := "afa8e30da03b2dc13a8eccc2546d1d7a36c4a9bbdcdc3e94d18e44cb4eb73b41"
	guestKeyBytes, _ := hex.DecodeString(guestPrivKeyHex)
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

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

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

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

	minutes := new(big.Int).Div(fundingShannons, s.ratePerMin).Int64()
	duration := time.Duration(minutes) * time.Minute

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
