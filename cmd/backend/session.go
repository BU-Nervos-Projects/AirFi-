package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"

	gpclient "perun.network/go-perun/client"

	"github.com/airfi/airfi-perun-nervous/internal/db"
	"github.com/airfi/airfi-perun-nervous/internal/guest"
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

// createSessionFromWallet creates a new session when a wallet is funded.
func (s *Server) createSessionFromWallet(wallet *db.GuestWallet, balanceCKB int64) string {
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	sessionID := hex.EncodeToString(idBytes)

	reservedCKB := s.channelSetupCKB
	usableCKB := balanceCKB - reservedCKB
	if usableCKB < 0 {
		usableCKB = 0
	}

	// Calculate session duration based on rate (using shannons for precision)
	ratePerHour, err := s.db.GetRatePerHour()
	if err != nil || ratePerHour <= 0 {
		ratePerHour = 500 // default
	}
	// Use same formula as micropayment processor for consistency
	usableShannons := usableCKB * 100000000
	ratePerMinShannons := (ratePerHour * 100000000) / 60
	sessionMinutes := usableShannons / ratePerMinShannons

	now := time.Now()
	sessionDuration := time.Duration(sessionMinutes) * time.Minute
	session := &db.Session{
		ID:           sessionID,
		WalletID:     wallet.ID,
		GuestAddress: wallet.Address,
		HostAddress:  s.hostClient.GetAddress(),
		FundingCKB:   balanceCKB,
		BalanceCKB:   usableCKB,
		SpentCKB:     0,
		CreatedAt:    now,
		ExpiresAt:    now.Add(sessionDuration),
		Status:       "active",
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
	)

	return sessionID
}

// startMicropaymentProcessor runs a background loop to process micropayments.
func (s *Server) startMicropaymentProcessor(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
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

// processMicropayments deducts CKB per minute from all active sessions.
func (s *Server) processMicropayments(ctx context.Context) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()

	for sessionID, session := range s.sessions {
		// Check expiration
		if time.Now().After(session.ExpiresAt) {
			s.logger.Info("session expired, settling channel", zap.String("session_id", sessionID))
			go s.settleExpiredSession(ctx, session)
			delete(s.sessions, sessionID)
			continue
		}

		// Check balance
		remaining := new(big.Int).Sub(session.FundingAmount, session.TotalPaid)
		if remaining.Cmp(s.ratePerMin) < 0 {
			s.logger.Info("insufficient balance, settling channel", zap.String("session_id", sessionID))
			go s.settleExpiredSession(ctx, session)
			delete(s.sessions, sessionID)
			continue
		}

		// Send micropayment
		err := session.Client.SendPayment(session.Channel, s.ratePerMin)
		if err != nil {
			s.logger.Error("micropayment failed", zap.String("session_id", sessionID), zap.Error(err))
			continue
		}

		session.TotalPaid.Add(session.TotalPaid, s.ratePerMin)
		spentCKB := session.TotalPaid.Int64() / 100000000
		balanceCKB := (session.FundingAmount.Int64() - session.TotalPaid.Int64()) / 100000000

		s.db.UpdateSessionBalance(sessionID, balanceCKB, spentCKB)

		s.logger.Debug("micropayment processed",
			zap.String("session_id", sessionID),
			zap.Int64("spent_ckb", spentCKB),
			zap.Int64("balance_ckb", balanceCKB),
		)
	}
}

// settleSessionInBackground handles channel settlement without blocking.
func (s *Server) settleSessionInBackground(session *GuestSession) {
	s.logger.Info("starting background settlement", zap.String("session_id", session.ID))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	err := session.Client.SettleChannel(ctx, session.Channel)
	if err != nil {
		s.logger.Error("background settlement failed", zap.Error(err))
	} else {
		s.logger.Info("background settlement completed", zap.String("session_id", session.ID))
	}

	s.db.SettleSession(session.ID)
	session.Client.Close()

	// Try to withdraw remaining CKB
	withdrawHash, err := s.withdrawToSender(context.Background(), session.ID)
	if err != nil {
		s.logger.Info("auto-withdraw skipped (Perun settlement already returned funds)",
			zap.String("session_id", session.ID),
			zap.String("note", err.Error()),
		)
	} else {
		s.logger.Info("auto-withdraw successful",
			zap.String("session_id", session.ID),
			zap.String("tx_hash", withdrawHash),
		)
	}

	s.logger.Info("background settlement process completed", zap.String("session_id", session.ID))
}

// settleExpiredSession settles a channel when session expires.
func (s *Server) settleExpiredSession(ctx context.Context, session *GuestSession) {
	settleCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	err := session.Client.SettleChannel(settleCtx, session.Channel)
	if err != nil {
		s.logger.Error("failed to settle channel", zap.String("session_id", session.ID), zap.Error(err))
	} else {
		s.logger.Info("channel settled", zap.String("session_id", session.ID))
	}

	s.db.SettleSession(session.ID)

	// Deauthorize MAC
	dbSession, err := s.db.GetSession(session.ID)
	if err == nil && dbSession.MACAddress != "" {
		if err := s.router.DeauthorizeMAC(ctx, dbSession.MACAddress); err != nil {
			s.logger.Error("failed to deauthorize MAC", zap.Error(err), zap.String("mac", dbSession.MACAddress))
		} else {
			s.logger.Info("MAC deauthorized", zap.String("mac", dbSession.MACAddress))
		}
	}

	session.Client.Close()

	// Try to withdraw remaining CKB
	go func() {
		withdrawHash, err := s.withdrawToSender(context.Background(), session.ID)
		if err != nil {
			s.logger.Info("auto-withdraw skipped for expired session",
				zap.String("session_id", session.ID),
				zap.String("note", err.Error()),
			)
		} else {
			s.logger.Info("auto-withdraw successful for expired session",
				zap.String("session_id", session.ID),
				zap.String("tx_hash", withdrawHash),
			)
		}
	}()
}

// withdrawToSender withdraws remaining CKB from guest wallet to sender.
func (s *Server) withdrawToSender(ctx context.Context, sessionID string) (string, error) {
	wallet, err := s.db.GetWalletBySessionID(sessionID)
	if err != nil {
		return "", fmt.Errorf("failed to get wallet: %w", err)
	}

	s.logger.Info("starting refund process",
		zap.String("session_id", sessionID),
		zap.String("wallet_id", wallet.ID),
		zap.String("sender_address", wallet.SenderAddress),
	)

	// Detect sender if not found
	if wallet.SenderAddress == "" {
		s.logger.Info("sender address not found, attempting detection...")
		withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))
		senderAddr, err := withdrawer.GetSenderAddress(ctx, wallet.Address, types.NetworkTest)
		if err != nil {
			return "", fmt.Errorf("no sender address: %w", err)
		}
		s.db.UpdateWalletSenderAddress(wallet.ID, senderAddr)
		wallet.SenderAddress = senderAddr
	}

	guestKeyBytes, err := hex.DecodeString(wallet.PrivateKeyHex)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	guestLockScript, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		return "", fmt.Errorf("failed to decode wallet address: %w", err)
	}

	withdrawer := perun.NewWithdrawer(s.ckbClient, s.logger.Named("withdrawer"))

	waitTimes := []time.Duration{30 * time.Second, 60 * time.Second, 120 * time.Second}
	var lastErr error

	for i, waitTime := range waitTimes {
		s.logger.Info("waiting for settlement to confirm...",
			zap.String("session_id", sessionID),
			zap.Int("attempt", i+1),
			zap.Duration("wait_time", waitTime),
		)
		time.Sleep(waitTime)

		txHash, err := withdrawer.WithdrawAll(ctx, guestPrivKey, guestLockScript, wallet.SenderAddress)
		if err != nil {
			lastErr = err
			s.logger.Warn("withdrawal attempt failed",
				zap.String("session_id", sessionID),
				zap.Int("attempt", i+1),
				zap.Error(err),
			)
			continue
		}

		s.db.UpdateWalletStatus(wallet.ID, "withdrawn")
		s.logger.Info("refund successful",
			zap.String("session_id", sessionID),
			zap.String("tx_hash", txHash.Hex()),
		)
		return txHash.Hex(), nil
	}

	return "", fmt.Errorf("failed to withdraw after %d attempts: %w", len(waitTimes), lastErr)
}
