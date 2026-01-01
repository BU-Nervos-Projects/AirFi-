// Package perun provides withdrawal functionality for guest wallets.
package perun

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/nervosnetwork/ckb-sdk-go/v2/address"
	"github.com/nervosnetwork/ckb-sdk-go/v2/crypto/blake2b"
	"github.com/nervosnetwork/ckb-sdk-go/v2/indexer"
	"github.com/nervosnetwork/ckb-sdk-go/v2/rpc"
	"github.com/nervosnetwork/ckb-sdk-go/v2/types"
	"go.uber.org/zap"
)

const (
	// WithdrawFee is the transaction fee for withdrawal (0.001 CKB)
	WithdrawFee uint64 = 100000
)

// Withdrawer handles withdrawing remaining CKB from guest wallets.
type Withdrawer struct {
	rpcClient rpc.Client
	logger    *zap.Logger
}

// NewWithdrawer creates a new withdrawer.
func NewWithdrawer(rpcClient rpc.Client, logger *zap.Logger) *Withdrawer {
	return &Withdrawer{
		rpcClient: rpcClient,
		logger:    logger,
	}
}

// GetSenderAddress finds the sender address from the funding transaction.
// It looks at the first input of transactions that sent CKB to the wallet.
func (w *Withdrawer) GetSenderAddress(ctx context.Context, walletAddress string, network types.Network) (string, error) {
	// Decode wallet address to get lock script
	lockScript, err := decodeAddressToScript(walletAddress)
	if err != nil {
		return "", fmt.Errorf("failed to decode wallet address: %w", err)
	}

	// Search for transactions to this address
	searchKey := &indexer.SearchKey{
		Script:           lockScript,
		ScriptType:       types.ScriptTypeLock,
		ScriptSearchMode: types.ScriptSearchModeExact,
		WithData:         true,
	}

	// Get transactions
	txs, err := w.rpcClient.GetTransactions(ctx, searchKey, indexer.SearchOrderDesc, 10, "")
	if err != nil {
		return "", fmt.Errorf("failed to get transactions: %w", err)
	}

	if len(txs.Objects) == 0 {
		return "", fmt.Errorf("no transactions found for wallet")
	}

	// Find the first transaction that is not from ourselves (the funding tx)
	for _, txObj := range txs.Objects {
		// Get full transaction
		tx, err := w.rpcClient.GetTransaction(ctx, txObj.TxHash)
		if err != nil {
			continue
		}

		if tx.Transaction == nil || len(tx.Transaction.Inputs) == 0 {
			continue
		}

		// Get the first input to find sender
		firstInput := tx.Transaction.Inputs[0]
		inputTx, err := w.rpcClient.GetTransaction(ctx, firstInput.PreviousOutput.TxHash)
		if err != nil {
			continue
		}

		if inputTx.Transaction == nil {
			continue
		}

		// Get the output that was spent
		outputIdx := firstInput.PreviousOutput.Index
		if int(outputIdx) >= len(inputTx.Transaction.Outputs) {
			continue
		}

		senderLockScript := inputTx.Transaction.Outputs[outputIdx].Lock

		// Skip if sender is the same as recipient (our own tx)
		if senderLockScript.Hash() == lockScript.Hash() {
			continue
		}

		// Convert lock script to address
		senderAddr, err := scriptToAddress(senderLockScript, network)
		if err != nil {
			continue
		}

		w.logger.Info("found sender address",
			zap.String("sender", senderAddr),
			zap.String("tx_hash", txObj.TxHash.Hex()),
		)

		return senderAddr, nil
	}

	return "", fmt.Errorf("could not determine sender address")
}

// WithdrawAll sends all remaining CKB from wallet to the destination address.
func (w *Withdrawer) WithdrawAll(ctx context.Context, privateKey *secp256k1.PrivateKey, fromLockScript *types.Script, toAddress string) (types.Hash, error) {
	w.logger.Info("withdrawing all CKB to sender",
		zap.String("to_address", toAddress),
	)

	// Decode destination address
	toLockScript, err := decodeAddressToScript(toAddress)
	if err != nil {
		return types.Hash{}, fmt.Errorf("failed to decode destination address: %w", err)
	}

	// Get all cells from the wallet
	searchKey := &indexer.SearchKey{
		Script:           fromLockScript,
		ScriptType:       types.ScriptTypeLock,
		ScriptSearchMode: types.ScriptSearchModeExact,
		WithData:         true,
	}

	cells, err := w.rpcClient.GetCells(ctx, searchKey, indexer.SearchOrderAsc, 100, "")
	if err != nil {
		return types.Hash{}, fmt.Errorf("failed to get cells: %w", err)
	}

	if len(cells.Objects) == 0 {
		return types.Hash{}, fmt.Errorf("no cells found in wallet")
	}

	w.logger.Info("found cells in wallet",
		zap.Int("count", len(cells.Objects)),
		zap.String("wallet_lock_hash", fromLockScript.Hash().Hex()),
	)

	// Calculate total capacity and build inputs
	var totalCapacity uint64
	inputs := make([]*types.CellInput, 0)

	// Calculate expected lock script hash for verification
	expectedLockHash := fromLockScript.Hash()

	for _, cell := range cells.Objects {
		// Only use cells without type scripts (pure CKB cells)
		if cell.Output.Type != nil {
			w.logger.Debug("skipping cell with type script",
				zap.String("outpoint", fmt.Sprintf("%s:%d", cell.OutPoint.TxHash.Hex(), cell.OutPoint.Index)),
			)
			continue
		}

		// Verify cell actually belongs to this wallet (lock script matches)
		if cell.Output.Lock.Hash() != expectedLockHash {
			w.logger.Warn("skipping cell with different lock script",
				zap.String("outpoint", fmt.Sprintf("%s:%d", cell.OutPoint.TxHash.Hex(), cell.OutPoint.Index)),
				zap.String("expected_lock_hash", expectedLockHash.Hex()),
				zap.String("actual_lock_hash", cell.Output.Lock.Hash().Hex()),
			)
			continue
		}

		totalCapacity += cell.Output.Capacity
		inputs = append(inputs, &types.CellInput{
			Since:          0,
			PreviousOutput: cell.OutPoint,
		})

		w.logger.Debug("adding cell to withdrawal",
			zap.String("outpoint", fmt.Sprintf("%s:%d", cell.OutPoint.TxHash.Hex(), cell.OutPoint.Index)),
			zap.Uint64("capacity", cell.Output.Capacity),
		)
	}

	if len(inputs) == 0 {
		// Log what cells were found for debugging
		w.logger.Warn("no withdrawable cells found - cells may have been consumed by Perun channel",
			zap.Int("total_cells_found", len(cells.Objects)),
			zap.String("expected_lock_hash", expectedLockHash.Hex()),
		)
		return types.Hash{}, fmt.Errorf("no withdrawable cells found (cells may have been consumed by Perun channel - use manual refund API)")
	}

	if totalCapacity <= WithdrawFee+MinCellCapacity {
		return types.Hash{}, fmt.Errorf("insufficient balance for withdrawal: %d shannons", totalCapacity)
	}

	// Calculate output capacity (total - fee)
	outputCapacity := totalCapacity - WithdrawFee

	w.logger.Info("withdrawal details",
		zap.Uint64("total_capacity", totalCapacity),
		zap.Uint64("output_capacity", outputCapacity),
		zap.Uint64("fee", WithdrawFee),
		zap.Int("input_cells", len(inputs)),
	)

	// Build transaction
	secp256k1CellDep := getSecp256k1CellDep()

	tx := &types.Transaction{
		Version: 0,
		CellDeps: []*types.CellDep{
			secp256k1CellDep,
		},
		Inputs: inputs,
		Outputs: []*types.CellOutput{
			{
				Capacity: outputCapacity,
				Lock:     toLockScript,
				Type:     nil,
			},
		},
		OutputsData: [][]byte{{}},
		Witnesses:   make([][]byte, len(inputs)),
	}

	// First witness is the signature, rest are empty
	tx.Witnesses[0] = make([]byte, 85)
	for i := 1; i < len(inputs); i++ {
		tx.Witnesses[i] = []byte{}
	}

	// Sign the transaction
	signedTx, err := w.signTransaction(tx, privateKey)
	if err != nil {
		return types.Hash{}, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Submit transaction
	txHash, err := w.rpcClient.SendTransaction(ctx, signedTx)
	if err != nil {
		return types.Hash{}, fmt.Errorf("failed to send transaction: %w", err)
	}

	w.logger.Info("withdrawal transaction submitted",
		zap.String("tx_hash", txHash.Hex()),
		zap.Uint64("amount_ckb", outputCapacity/100000000),
	)

	return *txHash, nil
}

// signTransaction signs a transaction with the given private key.
// For multiple inputs in the same lock group, the signature message must include ALL witnesses.
func (w *Withdrawer) signTransaction(tx *types.Transaction, privateKey *secp256k1.PrivateKey) (*types.Transaction, error) {
	// Create empty witness for placeholder
	witnessArgs := &types.WitnessArgs{
		Lock: make([]byte, 65), // 65 bytes for signature
	}
	witnessBytes := witnessArgs.Serialize()

	// Set witness placeholder before computing hash
	tx.Witnesses[0] = witnessBytes

	// Calculate transaction hash
	txHash := tx.ComputeHash()

	// Calculate message to sign: tx_hash + len(witness0) + witness0 + len(witness1) + witness1 + ...
	// For multiple inputs in the same lock group, ALL witnesses must be included
	message := make([]byte, 32)
	copy(message[:32], txHash[:])

	// Add all witnesses to the message
	for _, witness := range tx.Witnesses {
		lenBytes := make([]byte, 8)
		binary.LittleEndian.PutUint64(lenBytes, uint64(len(witness)))
		message = append(message, lenBytes...)
		message = append(message, witness...)
	}

	// Hash the message using blake2b
	messageHash := blake2b.Blake256(message)

	// Sign with secp256k1
	sig := signWithKey(messageHash, privateKey)

	// Update witness with signature
	witnessArgs.Lock = sig
	tx.Witnesses[0] = witnessArgs.Serialize()

	return tx, nil
}

// decodeAddressToScript converts a CKB address string to a lock script.
func decodeAddressToScript(addr string) (*types.Script, error) {
	parsedAddr, err := address.Decode(addr)
	if err != nil {
		return nil, err
	}
	return parsedAddr.Script, nil
}

// scriptToAddress converts a lock script to a CKB address string.
func scriptToAddress(script *types.Script, network types.Network) (string, error) {
	addr := &address.Address{
		Script:  script,
		Network: network,
	}
	return addr.Encode()
}
