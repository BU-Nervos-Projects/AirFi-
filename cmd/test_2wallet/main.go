package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"go.uber.org/zap"

	"github.com/airfi/airfi-perun-nervous/internal/perun"
)

func main() {
	// Setup logger
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  AirFi 2-Wallet Perun Channel Test")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Guest wallet (WiFi user - pays for access)
	guestPrivKeyHex := "afa8e30da03b2dc13a8eccc2546d1d7a36c4a9bbdcdc3e94d18e44cb4eb73b41"
	guestKeyBytes, _ := hex.DecodeString(guestPrivKeyHex)
	guestPrivKey := secp256k1.PrivKeyFromBytes(guestKeyBytes)

	// Create Guest Perun client
	guestCfg := &perun.PerunConfig{
		RPCURL:     perun.TestnetRPCURL,
		PrivateKey: guestPrivKey,
		Deployment: perun.GetTestnetDeployment(),
		Logger:     logger.Named("guest"),
	}

	logger.Info("Creating Guest Perun client...")
	guestClient, err := perun.NewPerunClient(guestCfg)
	if err != nil {
		logger.Fatal("failed to create Guest client", zap.Error(err))
	}
	defer guestClient.Close()

	fmt.Printf("\n  Guest Wallet: %s\n", guestClient.GetAddress())

	// Check guest balance
	ctx := context.Background()
	guestBalance, err := guestClient.GetBalance(ctx)
	if err != nil {
		logger.Error("failed to get guest balance", zap.Error(err))
	} else {
		ckb := new(big.Float).Quo(new(big.Float).SetInt(guestBalance), big.NewFloat(100000000))
		fmt.Printf("  Guest Balance: %.2f CKB\n", ckb)
	}

	// Host wallet address (WiFi provider - receives payments)
	hostAddress := "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsqtnn575f6scrdtxvzr5u88h2ksw3cyss9gvxcvjg"
	fmt.Printf("  Host Address: %s\n", hostAddress)

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Step 1: Guest Opens Channel")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Guest opens channel and funds it
	// Both parties need minimum funding due to PFLS capacity requirements
	guestFunding := big.NewInt(10000000000)  // 100 CKB from guest
	hostFunding := big.NewInt(10000000000)   // 100 CKB from host (minimum required)

	fmt.Printf("\n  Guest Funding: %.2f CKB\n", float64(guestFunding.Int64())/100000000)
	fmt.Printf("  Host Funding: %.2f CKB\n", float64(hostFunding.Int64())/100000000)
	fmt.Println("\n  Opening channel (this creates on-chain TX)...")

	ctxTimeout, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	channel, err := guestClient.OpenChannel(ctxTimeout, hostAddress, guestFunding, hostFunding)
	if err != nil {
		logger.Error("failed to open channel", zap.Error(err))
		fmt.Printf("\n  ❌ Error: %v\n", err)
		fmt.Println("\n═══════════════════════════════════════════════════════════════")
		return
	}

	fmt.Println("\n  ✅ Channel Opened!")
	fmt.Printf("  Channel ID: %x\n", channel.ID)
	fmt.Printf("  PCTS Hash: %s\n", channel.FundingTx)
	fmt.Printf("  Guest Balance: %.2f CKB\n", float64(channel.MyBalance.Int64())/100000000)
	fmt.Printf("  Host Balance: %.2f CKB\n", float64(channel.PeerBalance.Int64())/100000000)

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
		err := guestClient.SendPayment(channel.ID, paymentAmount)
		if err != nil {
			logger.Error("payment failed", zap.Error(err))
			continue
		}
		fmt.Printf("\n  Payment %d: %.2f CKB → Host\n", i+1, float64(amount)/100000000)
	}

	// Get updated balances
	updatedChannel, _ := guestClient.GetChannel(channel.ID)
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Final Balances (Off-chain State)")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("\n  Guest Balance: %.2f CKB\n", float64(updatedChannel.MyBalance.Int64())/100000000)
	fmt.Printf("  Host Balance: %.2f CKB\n", float64(updatedChannel.PeerBalance.Int64())/100000000)
	fmt.Printf("  Total Paid: %.2f CKB\n", float64(8*100000000)/100000000)

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Step 3: Force Close (Dispute + Wait + Claim)")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Step 3a: Dispute - register current state on-chain
	fmt.Println("\n  Step 3a: Registering dispute (starting challenge period)...")
	err = guestClient.DisputeChannel(ctxTimeout, channel.ID)
	if err != nil {
		fmt.Printf("\n  ❌ Dispute Error: %v\n", err)
		fmt.Println("\n  Note: Dispute also requires valid signatures from both parties.")
		fmt.Println("  This is a limitation of our current demo setup.")
	} else {
		fmt.Println("  ✅ Dispute registered! Challenge period started (9 blocks)")

		// Step 3b: Wait for challenge period
		fmt.Println("\n  Step 3b: Waiting for challenge period...")
		fmt.Println("  (In production: wait ~9 blocks / few minutes)")
		fmt.Println("  (For demo: waiting 30 seconds...)")
		time.Sleep(30 * time.Second)

		// Step 3c: Force close
		fmt.Println("\n  Step 3c: Force closing channel...")
		err = guestClient.ForceCloseChannel(ctxTimeout, channel.ID)
		if err != nil {
			fmt.Printf("\n  ❌ Force Close Error: %v\n", err)
		} else {
			fmt.Println("  ✅ Channel force closed! Funds released.")
		}
	}

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  Summary")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Printf("\n  Channel ID: %x\n", channel.ID)
	fmt.Println("\n  Check wallets in explorer:")
	fmt.Printf("  Guest: https://pudge.explorer.nervos.org/address/%s\n", guestClient.GetAddress())
	fmt.Println("  Host:  https://pudge.explorer.nervos.org/address/ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsqtnn575f6scrdtxvzr5u88h2ksw3cyss9gvxcvjg")
	fmt.Println("═══════════════════════════════════════════════════════════════")
}
