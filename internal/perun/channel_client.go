package perun

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/nervosnetwork/ckb-sdk-go/v2/indexer"
	"github.com/nervosnetwork/ckb-sdk-go/v2/rpc"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"

	gpclient "perun.network/go-perun/client"
	gpchannel "perun.network/go-perun/channel"
	gpwallet "perun.network/go-perun/wallet"
	gpwire "perun.network/go-perun/wire"
	gpwiretest "perun.network/go-perun/backend/sim/wire"
	"perun.network/go-perun/watcher/local"

	"perun.network/perun-ckb-backend/backend"
	"perun.network/perun-ckb-backend/channel/adjudicator"
	"perun.network/perun-ckb-backend/channel/asset"
	"perun.network/perun-ckb-backend/channel/funder"
	ckbclient "perun.network/perun-ckb-backend/client"
	ckbwallet "perun.network/perun-ckb-backend/wallet"
	"perun.network/perun-ckb-backend/wallet/address"
	ckbwallettest "perun.network/perun-ckb-backend/wallet/test"
)

// ChannelClient wraps go-perun client for proper channel management.
type ChannelClient struct {
	perunClient  *gpclient.Client
	account      *ckbwallet.Account
	wallet       *ckbwallettest.TestEphemeralWallet
	funder       gpchannel.Funder
	adjudicator  gpchannel.Adjudicator
	ckbClient    *ckbclient.Client
	wireAddress  gpwire.Address
	deployment   backend.Deployment
	rpcClient    rpc.Client
	logger       *zap.Logger

	// Active channels
	channels   map[gpchannel.ID]*ActiveChannel
	channelsMu sync.RWMutex
}

// ActiveChannel represents an active Perun channel with proper state management.
type ActiveChannel struct {
	Channel     *gpclient.Channel
	PeerAddress string
	CreatedAt   time.Time
}

// ChannelClientConfig contains configuration for the channel client.
type ChannelClientConfig struct {
	RPCURL     string
	PrivateKey *secp256k1.PrivateKey
	Deployment backend.Deployment
	Logger     *zap.Logger
	WireBus    *gpwire.LocalBus // Shared bus for communication
}

// NewChannelClient creates a new go-perun based channel client.
func NewChannelClient(cfg *ChannelClientConfig) (*ChannelClient, error) {
	if cfg.Logger == nil {
		cfg.Logger = zap.NewNop()
	}

	// Connect to CKB RPC
	rpcClient, err := rpc.Dial(cfg.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("failed to dial RPC: %w", err)
	}

	// Create wallet account from private key
	account := ckbwallet.NewAccountFromPrivateKey(cfg.PrivateKey)

	// Create ephemeral wallet
	wallet := ckbwallettest.NewTestEphemeralWallet(account)
	if err := wallet.AddAccount(account); err != nil {
		return nil, fmt.Errorf("failed to add account to wallet: %w", err)
	}

	// Create signer
	participant := address.AsParticipant(account.Address())
	ckbAddress := participant.ToCKBAddress(types.NetworkTest)
	signer := backend.NewSignerInstance(ckbAddress, *cfg.PrivateKey, types.NetworkTest)

	// Create CKB client
	ckbClient, err := ckbclient.NewClient(rpcClient, *signer, cfg.Deployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create CKB client: %w", err)
	}

	// Create funder and adjudicator
	channelFunder := funder.NewDefaultFunder(ckbClient, cfg.Deployment)
	channelAdjudicator := adjudicator.NewAdjudicator(ckbClient)

	// Create watcher
	watcher, err := local.NewWatcher(channelAdjudicator)
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	// Create wire identity (for channel communication)
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	wireIdentity := gpwiretest.NewRandomAccount(rng)

	// Create go-perun client
	perunClient, err := gpclient.New(
		wireIdentity.Address(),
		cfg.WireBus,
		channelFunder,
		channelAdjudicator,
		wallet,
		watcher,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Perun client: %w", err)
	}

	addressStr, _ := ckbAddress.Encode()
	cfg.Logger.Info("ChannelClient initialized",
		zap.String("address", addressStr),
		zap.String("signer_code_hash", ckbAddress.Script.CodeHash.String()),
		zap.String("signer_args", fmt.Sprintf("0x%x", ckbAddress.Script.Args)),
		zap.String("signer_hash_type", string(ckbAddress.Script.HashType)),
	)

	return &ChannelClient{
		perunClient:  perunClient,
		account:      account,
		wallet:       wallet,
		funder:       channelFunder,
		adjudicator:  channelAdjudicator,
		ckbClient:    ckbClient,
		wireAddress:  wireIdentity.Address(),
		deployment:   cfg.Deployment,
		rpcClient:    rpcClient,
		logger:       cfg.Logger,
		channels:     make(map[gpchannel.ID]*ActiveChannel),
	}, nil
}

// GetAddress returns the CKB address of this client.
func (cc *ChannelClient) GetAddress() string {
	participant := address.AsParticipant(cc.account.Address())
	ckbAddr := participant.ToCKBAddress(types.NetworkTest)
	addrStr, _ := ckbAddr.Encode()
	return addrStr
}

// GetWireAddress returns the wire address for channel proposals.
func (cc *ChannelClient) GetWireAddress() gpwire.Address {
	return cc.wireAddress
}

// GetAccount returns the Perun account for channel operations.
func (cc *ChannelClient) GetAccount() gpwallet.Account {
	return cc.account
}

// GetBalance returns the on-chain CKB balance.
func (cc *ChannelClient) GetBalance(ctx context.Context) (*big.Int, error) {
	participant := address.AsParticipant(cc.account.Address())
	ckbAddress := participant.ToCKBAddress(types.NetworkTest)

	addressStr, err := ckbAddress.Encode()
	if err != nil {
		return nil, fmt.Errorf("failed to encode address: %w", err)
	}

	cc.logger.Info("querying balance",
		zap.String("address", addressStr),
		zap.String("code_hash", ckbAddress.Script.CodeHash.String()),
		zap.String("args", fmt.Sprintf("0x%x", ckbAddress.Script.Args)),
	)

	capacity, err := cc.rpcClient.GetCellsCapacity(ctx, &indexer.SearchKey{
		Script:     ckbAddress.Script,
		ScriptType: types.ScriptTypeLock,
	})
	if err != nil {
		cc.logger.Warn("failed to get balance", zap.String("address", addressStr), zap.Error(err))
		return big.NewInt(0), nil
	}

	cc.logger.Info("balance query result",
		zap.Uint64("capacity", capacity.Capacity),
		zap.Float64("capacity_ckb", float64(capacity.Capacity)/100000000),
	)

	return big.NewInt(int64(capacity.Capacity)), nil
}

// ProposeChannel proposes a new channel to a peer.
func (cc *ChannelClient) ProposeChannel(
	ctx context.Context,
	peerWireAddr gpwire.Address,
	peerPerunAddr gpwallet.Address,
	myFunding *big.Int,
	peerFunding *big.Int,
) (*gpclient.Channel, error) {
	// Get our address details for debugging
	participant := address.AsParticipant(cc.account.Address())
	ckbAddress := participant.ToCKBAddress(types.NetworkTest)
	addressStr, _ := ckbAddress.Encode()

	cc.logger.Info("proposing channel",
		zap.String("my_address", addressStr),
		zap.String("my_funding", myFunding.String()),
		zap.String("peer_funding", peerFunding.String()),
		zap.String("lock_code_hash", ckbAddress.Script.CodeHash.String()),
		zap.String("lock_args", fmt.Sprintf("0x%x", ckbAddress.Script.Args)),
	)

	// Check our balance before proposing
	balance, err := cc.GetBalance(ctx)
	if err != nil {
		cc.logger.Warn("failed to check balance before proposal", zap.Error(err))
	} else {
		cc.logger.Info("balance before proposal",
			zap.String("balance_shannons", balance.String()),
			zap.Float64("balance_ckb", float64(balance.Int64())/100000000),
			zap.String("required_shannons", myFunding.String()),
		)
	}

	// Create allocation
	ckbAsset := asset.NewCKBytesAsset()
	initAlloc := gpchannel.NewAllocation(2, ckbAsset)
	initAlloc.SetAssetBalances(ckbAsset, []gpchannel.Bal{myFunding, peerFunding})

	// Create proposal - peers array must include ALL participants (including ourselves)
	proposal, err := gpclient.NewLedgerChannelProposal(
		ChallengeBlocks,
		cc.account.Address(),
		initAlloc,
		[]gpwire.Address{cc.wireAddress, peerWireAddr},
		gpclient.WithoutApp(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create proposal: %w", err)
	}

	// Propose channel
	ch, err := cc.perunClient.ProposeChannel(ctx, proposal)
	if err != nil {
		return nil, fmt.Errorf("failed to propose channel: %w", err)
	}

	// Store active channel
	cc.channelsMu.Lock()
	cc.channels[ch.ID()] = &ActiveChannel{
		Channel:   ch,
		CreatedAt: time.Now(),
	}
	cc.channelsMu.Unlock()

	cc.logger.Info("channel proposed successfully",
		zap.String("channel_id", fmt.Sprintf("%x", ch.ID())),
	)

	return ch, nil
}

// ChannelHandler combines ProposalHandler and UpdateHandler interfaces.
type ChannelHandler interface {
	gpclient.ProposalHandler
	gpclient.UpdateHandler
}

// HandleProposals starts handling incoming channel proposals and updates.
func (cc *ChannelClient) HandleProposals(handler ChannelHandler) {
	go cc.perunClient.Handle(handler, handler)
}

// SendPayment sends an off-chain payment in the channel.
// This properly signs the new state with both parties.
func (cc *ChannelClient) SendPayment(ch *gpclient.Channel, amount *big.Int) error {
	cc.logger.Info("sending payment",
		zap.String("channel_id", fmt.Sprintf("%x", ch.ID())),
		zap.String("amount", amount.String()),
	)

	// Get current state
	state := ch.State().Clone()

	// Update balances (send from us to peer)
	ckbAsset := asset.NewCKBytesAsset()
	myIdx := ch.Idx()
	peerIdx := 1 - myIdx

	myBal := state.Allocation.Balance(myIdx, ckbAsset)
	peerBal := state.Allocation.Balance(peerIdx, ckbAsset)

	if myBal.Cmp(amount) < 0 {
		return fmt.Errorf("insufficient balance: have %s, want %s", myBal.String(), amount.String())
	}

	// Create new balances
	newMyBal := new(big.Int).Sub(myBal, amount)
	newPeerBal := new(big.Int).Add(peerBal, amount)

	// Set new balances in the right order
	newBals := make([]gpchannel.Bal, 2)
	newBals[myIdx] = newMyBal
	newBals[peerIdx] = newPeerBal
	state.Allocation.SetAssetBalances(ckbAsset, newBals)

	// Update the channel state (this handles signing automatically)
	err := ch.Update(context.Background(), func(s *gpchannel.State) {
		s.Allocation = state.Allocation
	})
	if err != nil {
		return fmt.Errorf("failed to update channel: %w", err)
	}

	cc.logger.Info("payment sent",
		zap.String("channel_id", fmt.Sprintf("%x", ch.ID())),
		zap.String("new_balance", newMyBal.String()),
	)

	return nil
}

// SettleChannel settles the channel on-chain.
// This uses the properly signed state from channel updates.
func (cc *ChannelClient) SettleChannel(ctx context.Context, ch *gpclient.Channel) error {
	cc.logger.Info("settling channel",
		zap.String("channel_id", fmt.Sprintf("%x", ch.ID())),
	)

	// First, finalize the state
	err := ch.Update(ctx, func(s *gpchannel.State) {
		s.IsFinal = true
	})
	if err != nil {
		return fmt.Errorf("failed to finalize state: %w", err)
	}

	// Then settle (this calls Withdraw on the adjudicator with proper signatures)
	err = ch.Settle(ctx, false)
	if err != nil {
		return fmt.Errorf("failed to settle channel: %w", err)
	}

	// Remove from active channels
	cc.channelsMu.Lock()
	delete(cc.channels, ch.ID())
	cc.channelsMu.Unlock()

	cc.logger.Info("channel settled successfully",
		zap.String("channel_id", fmt.Sprintf("%x", ch.ID())),
	)

	return nil
}

// Close closes the channel client.
func (cc *ChannelClient) Close() error {
	return cc.perunClient.Close()
}

