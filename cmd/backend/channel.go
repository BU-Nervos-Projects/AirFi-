package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"go.uber.org/zap"

	gpchannel "perun.network/go-perun/channel"
	gpclient "perun.network/go-perun/client"

	"github.com/airfi/airfi-perun-nervous/internal/db"
	"github.com/airfi/airfi-perun-nervous/internal/guest"
	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

// HostProposalHandler handles incoming channel proposals on the host side.
type HostProposalHandler struct {
	server *Server
	logger *zap.Logger
}

// HandleProposal handles a channel proposal from a guest.
func (h *HostProposalHandler) HandleProposal(proposal gpclient.ChannelProposal, responder *gpclient.ProposalResponder) {
	h.logger.Info("received channel proposal")

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

// HandleUpdate handles a channel update.
func (h *HostProposalHandler) HandleUpdate(cur *gpchannel.State, next gpclient.ChannelUpdate, responder *gpclient.UpdateResponder) {
	h.logger.Info("received update proposal", zap.Uint64("version", next.State.Version))

	err := responder.Accept(context.Background())
	if err != nil {
		h.logger.Error("failed to accept update", zap.Error(err))
	}
}

// openChannelForSession opens a Perun payment channel for a funded session.
func (s *Server) openChannelForSession(ctx context.Context, wallet *db.GuestWallet, sessionID string, balanceCKB int64) {
	s.logger.Info("opening Perun channel for session",
		zap.String("session_id", sessionID),
		zap.Int64("funding_ckb", balanceCKB),
	)

	guestKeyBytes, err := hex.DecodeString(wallet.PrivateKeyHex)
	if err != nil {
		s.logger.Error("failed to decode guest private key", zap.Error(err))
		return
	}
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	pubKeyBytes := guestPrivKey.PubKey().SerializeCompressed()
	s.logger.Info("reconstructed private key",
		zap.String("key_hex", wallet.PrivateKeyHex[:16]+"..."),
		zap.String("pubkey_prefix", fmt.Sprintf("0x%x...", pubKeyBytes[:8])),
	)

	guestLockScript, err := guest.DecodeAddress(wallet.Address)
	if err != nil {
		s.logger.Error("failed to decode guest address", zap.Error(err))
		s.db.UpdateSessionStatus(sessionID, "channel_failed")
		return
	}

	// Guest cell preparation
	s.logger.Info("preparing guest wallet cells for Perun operation")
	cellSplitter := perun.NewCellSplitter(s.ckbClient, s.logger.Named("cell-splitter"))
	if err := cellSplitter.EnsureMinimumCells(ctx, guestPrivKey, guestLockScript, 4); err != nil {
		s.logger.Error("failed to prepare wallet cells", zap.Error(err))
		s.db.UpdateSessionStatus(sessionID, "cell_preparation_failed")
		return
	}
	guestCellCount, _ := cellSplitter.CountCells(ctx, guestLockScript)
	s.logger.Info("guest wallet cell preparation complete", zap.Int("cell_count", guestCellCount))

	// Create guest channel client
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

	minBalanceForChannel := s.channelSetupCKB
	if balanceCKB < minBalanceForChannel {
		s.logger.Error("insufficient balance for channel",
			zap.Int64("balance", balanceCKB),
			zap.Int64("minimum_required", minBalanceForChannel),
		)
		s.db.UpdateSessionStatus(sessionID, "insufficient_funds")
		guestClient.Close()
		return
	}

	reservedCKB := s.channelSetupCKB
	fundingCKB := balanceCKB - reservedCKB
	guestFunding := big.NewInt(fundingCKB * 100000000)
	s.logger.Info("calculated funding amount",
		zap.Int64("balance_ckb", balanceCKB),
		zap.Int64("reserved_ckb", reservedCKB),
		zap.Int64("funding_ckb", fundingCKB),
	)

	hostFunding := big.NewInt(10000000000) // 100 CKB

	s.db.UpdateSessionStatus(sessionID, "channel_opening")

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

		// Revoke optimistic WiFi access
		if wallet.MACAddress != "" {
			s.logger.Warn("revoking optimistic WiFi access due to channel failure",
				zap.String("session_id", sessionID),
				zap.String("mac", wallet.MACAddress),
			)
			if err := s.router.DeauthorizeMAC(ctx, wallet.MACAddress); err != nil {
				s.logger.Error("failed to deauthorize MAC after channel failure", zap.Error(err))
			}
		}
		return
	}

	channelID := fmt.Sprintf("%x", channel.ID())

	if err := s.db.UpdateSessionChannel(sessionID, channelID, "active"); err != nil {
		s.logger.Error("failed to update session channel", zap.Error(err))
	} else {
		s.logger.Info("channel opened successfully",
			zap.String("session_id", sessionID),
			zap.String("channel_id", channelID),
		)
	}
	if err := s.db.UpdateWalletStatus(wallet.ID, "channel_open"); err != nil {
		s.logger.Error("failed to update wallet status", zap.Error(err))
	}

	// Calculate catch-up payment for elapsed time
	dbSession, err := s.db.GetSession(sessionID)
	if err != nil {
		s.logger.Error("failed to get session for catch-up calculation", zap.Error(err))
		return
	}

	elapsedTime := time.Since(dbSession.CreatedAt)
	elapsedMinutes := int64(elapsedTime.Minutes())
	if elapsedMinutes < 1 {
		elapsedMinutes = 1
	}

	catchUpShannons := new(big.Int).Mul(big.NewInt(elapsedMinutes), s.ratePerMin)
	catchUpCKB := catchUpShannons.Int64() / 100000000

	s.logger.Info("calculating catch-up payment for channel opening delay",
		zap.Duration("elapsed_time", elapsedTime),
		zap.Int64("elapsed_minutes", elapsedMinutes),
		zap.Int64("catch_up_ckb", catchUpCKB),
	)

	if catchUpShannons.Cmp(big.NewInt(0)) > 0 {
		err := guestClient.SendPayment(channel, catchUpShannons)
		if err != nil {
			s.logger.Error("failed to send catch-up payment", zap.Error(err))
		} else {
			s.logger.Info("catch-up payment sent", zap.Int64("amount_ckb", catchUpCKB))
		}
	}

	// Store in-memory for micropayment processing
	guestSession := &GuestSession{
		ID:            sessionID,
		Client:        guestClient,
		Channel:       channel,
		GuestAddress:  wallet.Address,
		FundingAmount: guestFunding,
		TotalPaid:     catchUpShannons,
		CreatedAt:     dbSession.CreatedAt,
		ExpiresAt:     dbSession.ExpiresAt,
	}

	s.sessionsMu.Lock()
	s.sessions[sessionID] = guestSession
	s.sessionsMu.Unlock()

	// Update database with initial spent amount
	newBalance := fundingCKB - catchUpCKB
	s.db.UpdateSessionBalance(sessionID, newBalance, catchUpCKB)

	s.logger.Info("Perun channel opened",
		zap.String("session_id", sessionID),
		zap.String("channel_id", channelID),
		zap.Int64("guest_funding", balanceCKB),
		zap.Int64("catch_up_spent", catchUpCKB),
	)
}
