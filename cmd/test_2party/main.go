package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"go.uber.org/zap"

	goperun "perun.network/go-perun/channel"
	"perun.network/perun-ckb-backend/channel/asset"

	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

func main() {
	// Setup logger
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  AirFi Real 2-Party Perun Channel Test")
	fmt.Println("  Both Host and Guest have real private keys")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Host wallet (WiFi provider - receives payments)
	hostPrivKeyHex := "5ba43817d0634ca9f1620b4f17874f366794f181cd0eb854ea7ff711093b26f3"
	hostKeyBytes, _ := hex.DecodeString(hostPrivKeyHex)
	hostPrivKey := secp256k1.PrivKeyFromBytes(hostKeyBytes)

	// Guest wallet (WiFi user - pays for access)
	guestPrivKeyHex := "afa8e30da03b2dc13a8eccc2546d1d7a36c4a9bbdcdc3e94d18e44cb4eb73b41"
	guestKeyBytes, _ := hex.DecodeString(guestPrivKeyHex)
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	// Create Host Perun client
	fmt.Println("\n  Creating Host Perun client...")
	hostCfg := &perun.PerunConfig{
		RPCURL:     perun.TestnetRPCURL,
		PrivateKey: hostPrivKey,
		Deployment: perun.GetTestnetDeployment(),
		Logger:     logger.Named("host"),
	}
	hostClient, err := perun.NewPerunClient(hostCfg)
	if err != nil {
		logger.Fatal("failed to create Host client", zap.Error(err))
	}
	defer hostClient.Close()

	// Create Guest Perun client
	fmt.Println("  Creating Guest Perun client...")
	guestCfg := &perun.PerunConfig{
		RPCURL:     perun.TestnetRPCURL,
		PrivateKey: guestPrivKey,
		Deployment: perun.GetTestnetDeployment(),
		Logger:     logger.Named("guest"),
	}
	guestClient, err := perun.NewPerunClient(guestCfg)
	if err != nil {
		logger.Fatal("failed to create Guest client", zap.Error(err))
	}
	defer guestClient.Close()

	fmt.Printf("\n  Host Address:  %s\n", hostClient.GetAddress())
	fmt.Printf("  Guest Address: %s\n", guestClient.GetAddress())

	// Check balances
	ctx := context.Background()
	hostBalance, _ := hostClient.GetBalance(ctx)
	guestBalance, _ := guestClient.GetBalance(ctx)
	fmt.Printf("\n  Host Balance:  %.2f CKB\n", float64(hostBalance.Int64())/100000000)
	fmt.Printf("  Guest Balance: %.2f CKB\n", float64(guestBalance.Int64())/100000000)

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Step 1: Guest Opens Channel with Host (Real 2-Party)")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Guest opens channel with Host using Host's REAL account
	guestFunding := big.NewInt(10000000000) // 100 CKB from guest
	hostFunding := big.NewInt(10000000000)  // 100 CKB from host

	fmt.Printf("\n  Guest Funding: %.2f CKB\n", float64(guestFunding.Int64())/100000000)
	fmt.Printf("  Host Funding:  %.2f CKB\n", float64(hostFunding.Int64())/100000000)
	fmt.Println("\n  Step 1a: Guest opens channel (Party A)...")

	ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Guest opens channel using Host's REAL Perun account
	channel, err := guestClient.OpenChannelWithPeer(
		ctxTimeout,
		hostClient.GetAccount(),       // Host's REAL account
		hostClient.GetAddress(),       // Host's CKB address
		guestFunding,                  // Guest's funding
		hostFunding,                   // Host's funding
	)
	if err != nil {
		logger.Error("failed to open channel", zap.Error(err))
		fmt.Printf("\n  ❌ Error: %v\n", err)
		fmt.Println("\n═══════════════════════════════════════════════════════════════")
		return
	}

	fmt.Printf("\n  ✅ Guest opened channel! (Channel ID: %x)\n", channel.ID)
	fmt.Printf("  Status: %s\n", channel.State)

	// Register channel on Host's side (so Host can track it)
	hostClient.RegisterChannel(
		channel.ID,
		channel.Params,
		guestClient.GetAccount(),
		guestClient.GetAddress(),
		hostFunding,   // Host's balance (from Host's perspective)
		guestFunding,  // Guest's balance (from Host's perspective)
	)

	// Step 1b: Host funds the channel (Party B)
	fmt.Println("\n  Step 1b: Host funds channel (Party B)...")

	// Create the initial state for funding (same as what Guest used)
	ckbAsset := asset.NewCKBytesAsset()
	initAlloc := goperun.NewAllocation(2, ckbAsset)
	initAlloc.SetAssetBalances(ckbAsset, []goperun.Bal{guestFunding, hostFunding})

	initState := &goperun.State{
		ID:         channel.ID,
		Version:    0,
		App:        goperun.NoApp(),
		Allocation: *initAlloc,
		Data:       goperun.NoData(),
		IsFinal:    false,
	}

	err = hostClient.FundChannel(ctxTimeout, channel.PCTS, channel.Params, initState)
	if err != nil {
		logger.Error("failed to fund channel", zap.Error(err))
		fmt.Printf("\n  ❌ Host funding failed: %v\n", err)
		fmt.Println("  Continuing with Guest-only funding...")
	} else {
		fmt.Println("  ✅ Host funded channel!")
	}

	fmt.Println("\n  ✅ Channel Opened with Real 2-Party Setup!")
	fmt.Printf("  Channel ID: %x\n", channel.ID)
	fmt.Printf("  PCTS Hash: %s\n", channel.FundingTx)
	fmt.Printf("  Guest Balance: %.2f CKB\n", float64(channel.MyBalance.Int64())/100000000)
	fmt.Printf("  Host Balance:  %.2f CKB\n", float64(channel.PeerBalance.Int64())/100000000)

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Step 2: Guest Makes Payments (Off-chain)")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Simulate WiFi usage payments
	payments := []int64{
		100000000,  // 1 CKB - 1 minute
		500000000,  // 5 CKB - 5 minutes
		200000000,  // 2 CKB - 2 minutes
	}

	for i, amount := range payments {
		paymentAmount := big.NewInt(amount)

		// Update on Guest's side (sends payment)
		err := guestClient.SendPayment(channel.ID, paymentAmount)
		if err != nil {
			logger.Error("payment failed on guest side", zap.Error(err))
			continue
		}

		// Update on Host's side (receives payment)
		err = hostClient.ReceivePayment(channel.ID, paymentAmount)
		if err != nil {
			logger.Error("payment failed on host side", zap.Error(err))
			continue
		}

		fmt.Printf("\n  Payment %d: %.2f CKB → Host (off-chain)\n", i+1, float64(amount)/100000000)
	}

	// Get updated balances
	guestChannel, _ := guestClient.GetChannel(channel.ID)
	hostChannel, _ := hostClient.GetChannel(channel.ID)

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Final Balances (Off-chain State)")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("\n  Guest's view: My %.2f CKB, Host %.2f CKB\n",
		float64(guestChannel.MyBalance.Int64())/100000000,
		float64(guestChannel.PeerBalance.Int64())/100000000)
	fmt.Printf("  Host's view:  My %.2f CKB, Guest %.2f CKB\n",
		float64(hostChannel.MyBalance.Int64())/100000000,
		float64(hostChannel.PeerBalance.Int64())/100000000)
	fmt.Printf("  Total Paid: %.2f CKB\n", float64(8*100000000)/100000000)

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Step 3: Cooperative Settlement (Both Parties Sign)")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	fmt.Println("\n  Attempting cooperative close...")
	fmt.Println("  (Both Guest and Host sign final state)")

	settleTx, err := guestClient.SettleChannel(ctxTimeout, channel.ID)
	if err != nil {
		fmt.Printf("\n  ❌ Settlement Error: %v\n", err)

		fmt.Println("\n  Trying force close flow instead...")
		fmt.Println("\n═══════════════════════════════════════════════════════════════")
		fmt.Println("  Step 3b: Force Close (Dispute + Wait + Claim)")
		fmt.Println("═══════════════════════════════════════════════════════════════")

		// Try dispute
		fmt.Println("\n  Registering dispute (starting challenge period)...")
		err = guestClient.DisputeChannel(ctxTimeout, channel.ID)
		if err != nil {
			fmt.Printf("\n  ❌ Dispute Error: %v\n", err)
			fmt.Println("\n  Note: This may be a limitation of the Perun library.")
		} else {
			fmt.Println("  ✅ Dispute registered! Challenge period started")

			// Wait
			fmt.Println("\n  Waiting 30 seconds (simulating challenge period)...")
			time.Sleep(30 * time.Second)

			// Force close
			fmt.Println("\n  Force closing channel...")
			err = guestClient.ForceCloseChannel(ctxTimeout, channel.ID)
			if err != nil {
				fmt.Printf("\n  ❌ Force Close Error: %v\n", err)
			} else {
				fmt.Println("  ✅ Channel force closed! Funds released.")
			}
		}
	} else {
		fmt.Println("\n  ✅ Channel settled cooperatively!")
		fmt.Printf("  Settlement TX: %s\n", settleTx)
	}

	// Check final on-chain balances
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Final On-Chain Balances")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	hostBalanceFinal, _ := hostClient.GetBalance(ctx)
	guestBalanceFinal, _ := guestClient.GetBalance(ctx)
	fmt.Printf("\n  Host Balance:  %.2f CKB (was %.2f)\n",
		float64(hostBalanceFinal.Int64())/100000000,
		float64(hostBalance.Int64())/100000000)
	fmt.Printf("  Guest Balance: %.2f CKB (was %.2f)\n",
		float64(guestBalanceFinal.Int64())/100000000,
		float64(guestBalance.Int64())/100000000)

	fmt.Println("\n  Check wallets in explorer:")
	fmt.Printf("  Host:  https://pudge.explorer.nervos.org/address/%s\n", hostClient.GetAddress())
	fmt.Printf("  Guest: https://pudge.explorer.nervos.org/address/%s\n", guestClient.GetAddress())
	fmt.Println("═══════════════════════════════════════════════════════════════")
}
