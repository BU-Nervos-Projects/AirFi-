package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nervosnetwork/ckb-sdk-go/v2/indexer"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"

	"github.com/airfi/airfi-perun-nervous/internal/db"
	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

// handleCreateGuestWallet generates a new guest wallet for funding.
func (s *Server) handleCreateGuestWallet(c *gin.Context) {
	var req struct {
		MACAddress string `json:"mac_address"`
		IPAddress  string `json:"ip_address"`
	}
	c.ShouldBindJSON(&req)

	wallet, err := s.walletManager.GenerateWallet()
	if err != nil {
		s.logger.Error("failed to generate wallet", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate wallet"})
		return
	}

	dbWallet := &db.GuestWallet{
		ID:            wallet.ID,
		Address:       wallet.Address,
		PrivateKeyHex: wallet.GetPrivateKeyHex(),
		FundingCKB:    500,
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

// getMinimumFunding returns the minimum CKB required (channel_setup + rate_per_hour).
func (s *Server) getMinimumFunding() int64 {
	ratePerHour, err := s.db.GetRatePerHour()
	if err != nil {
		ratePerHour = 500 // default
	}

	return s.channelSetupCKB + ratePerHour
}

// handleGetGuestWallet returns the status of a guest wallet.
func (s *Server) handleGetGuestWallet(c *gin.Context) {
	walletID := c.Param("id")

	wallet, err := s.db.GetGuestWallet(walletID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "wallet not found"})
		return
	}

	minimumCKB := s.getMinimumFunding()

	// Check on-chain balance if still waiting for funding
	if wallet.Status == "created" {
		balance, err := s.checkWalletBalance(c.Request.Context(), wallet.Address)
		if err == nil {
			balanceCKB := balance / 100000000

			if balanceCKB >= minimumCKB {
				// Sufficient funding - create session
				sessionID := s.createSessionFromWallet(wallet, balanceCKB)

				s.db.UpdateWalletFunded(walletID, balanceCKB, sessionID)
				wallet.Status = "funded"
				wallet.BalanceCKB = balanceCKB
				wallet.SessionID = sessionID

				go s.openChannelForSession(context.Background(), wallet, sessionID, balanceCKB)
			} else if balanceCKB > 0 {
				// Partial funding - update balance but don't create session
				wallet.BalanceCKB = balanceCKB
				s.db.UpdateWalletBalance(walletID, balanceCKB)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"wallet_id":    wallet.ID,
		"address":      wallet.Address,
		"balance_ckb":  wallet.BalanceCKB,
		"minimum_ckb":  minimumCKB,
		"status":       wallet.Status,
		"session_id":   wallet.SessionID,
		"created_at":   wallet.CreatedAt.Format(time.RFC3339),
	})
}

// checkWalletBalance queries the on-chain balance for an address.
func (s *Server) checkWalletBalance(ctx context.Context, address string) (int64, error) {
	lockScript, err := guest.DecodeAddress(address)
	if err != nil {
		s.logger.Error("failed to decode address", zap.Error(err), zap.String("address", address))
		return 0, fmt.Errorf("failed to decode address: %w", err)
	}

	s.logger.Debug("checking wallet balance", zap.String("address", address))

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

	minimumCKB := s.getMinimumFunding()

	for _, wallet := range wallets {
		balance, err := s.checkWalletBalance(ctx, wallet.Address)
		if err != nil {
			continue
		}

		balanceCKB := balance / 100000000

		if balanceCKB >= minimumCKB {
			// Sufficient funding - create session
			go s.detectAndSaveSenderAddress(wallet.ID, wallet.Address)

			sessionID := s.createSessionFromWallet(wallet, balanceCKB)
			if sessionID != "" {
				s.db.UpdateWalletFunded(wallet.ID, balanceCKB, sessionID)
				s.logger.Info("wallet funded, session created",
					zap.String("wallet_id", wallet.ID),
					zap.Int64("balance", balanceCKB),
					zap.Int64("minimum", minimumCKB),
					zap.String("session_id", sessionID),
				)

				// Authorize MAC immediately (optimistic)
				if wallet.MACAddress != "" {
					comment := fmt.Sprintf("AirFi session (optimistic): %s", sessionID)
					if err := s.router.AuthorizeMAC(ctx, wallet.MACAddress, wallet.IPAddress, comment); err != nil {
						s.logger.Error("failed to authorize MAC", zap.Error(err), zap.String("mac", wallet.MACAddress))
					} else {
						s.logger.Info("MAC authorized (optimistic)",
							zap.String("mac", wallet.MACAddress),
							zap.String("ip", wallet.IPAddress),
						)
					}
				}

				go s.openChannelForSession(ctx, wallet, sessionID, balanceCKB)
			}
		} else if balanceCKB > 0 {
			// Partial funding - update balance for display
			s.db.UpdateWalletBalance(wallet.ID, balanceCKB)
			s.logger.Debug("partial funding detected",
				zap.String("wallet_id", wallet.ID),
				zap.Int64("balance", balanceCKB),
				zap.Int64("minimum", minimumCKB),
			)
		}
	}
}

// detectAndSaveSenderAddress detects the sender address with retries.
func (s *Server) detectAndSaveSenderAddress(walletID, walletAddress string) {
	withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))

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
