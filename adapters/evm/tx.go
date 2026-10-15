package evm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// ReceiptSummary is the chain-agnostic receipt the adapter reports
// (spec §7.8): the chain's own outcome, mapped to the shared transaction
// status. It carries no SDK types across the boundary.
type ReceiptSummary struct {
	Hash        string
	BlockNumber uint64
	BlockHash   string
	GasUsed     uint64
	Status      transaction.TransactionStatus
}

// Submit broadcasts one signed transaction and returns its chain hash.
// The outcome stays open: a timeout or a lost response does not mean the
// transaction was not accepted (spec §7.4, §14.3). Reconcile with
// Receipt before deciding anything, and never assume it was not sent.
func (n *Network) Submit(ctx context.Context, signed []byte) (string, error) {
	var tx types.Transaction
	if err := tx.UnmarshalBinary(signed); err != nil {
		return "", of(cherrs.InvalidArgument, n.opBase+".submit", n.id.String(),
			fmt.Errorf("payload is not an EVM transaction: %v", err))
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	err := n.pool.call(ctx, n.id.String(), n.opBase+".send_transaction", func(c rpcClient) error {
		return c.SendTransaction(ctx, &tx)
	})
	if err != nil {
		n.stats.countError()
		return "", err
	}
	return tx.Hash().Hex(), nil
}

// Receipt reports the transaction's inclusion outcome (spec §7.8):
// TxIncluded or TxReverted once the chain has it, TxPending while the
// node still holds it in its mempool, TxUnknown when the node no longer
// knows it — the outcome is open and only reconciliation closes it.
func (n *Network) Receipt(ctx context.Context, txHash string) (transaction.TransactionStatus, *ReceiptSummary, error) {
	hash, err := parseHash(txHash)
	if err != nil {
		return "", nil, of(cherrs.InvalidArgument, n.opBase+".transaction_receipt", n.id.String(), err)
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var receipt *types.Receipt
	var pooled *types.Transaction
	err = n.pool.call(ctx, n.id.String(), n.opBase+".transaction_receipt", func(c rpcClient) error {
		var e error
		receipt, e = c.TransactionReceipt(ctx, hash)
		if e != nil {
			return e
		}
		if receipt == nil {
			// Not mined: does the node still hold it? That decides
			// pending from unknown.
			pooled, _, e = c.TransactionByHash(ctx, hash)
		}
		return e
	})
	if err != nil {
		n.stats.countError()
		return "", nil, err
	}
	if receipt == nil {
		if pooled != nil {
			return transaction.TxPending, nil, nil
		}
		return transaction.TxUnknown, nil, nil
	}
	switch receipt.Status {
	case types.ReceiptStatusSuccessful:
		// Executed, not yet at the finality policy's depth: inclusion
		// is the fact, confirmation is the policy's call.
		return transaction.TxIncluded, &ReceiptSummary{
			Hash:        hash.Hex(),
			BlockNumber: receipt.BlockNumber.Uint64(),
			BlockHash:   receipt.BlockHash.Hex(),
			GasUsed:     receipt.GasUsed,
			Status:      transaction.TxIncluded,
		}, nil
	case types.ReceiptStatusFailed:
		// Executed with a failing result, not rejected before
		// execution. Terminal, and not a retry: the business must
		// compensate, not resend.
		return transaction.TxReverted, &ReceiptSummary{
			Hash:        hash.Hex(),
			BlockNumber: receipt.BlockNumber.Uint64(),
			BlockHash:   receipt.BlockHash.Hex(),
			GasUsed:     receipt.GasUsed,
			Status:      transaction.TxReverted,
		}, nil
	default:
		return "", nil, of(cherrs.InternalError, n.opBase+".transaction_receipt", n.id.String(),
			fmt.Errorf("node returned receipt status %d", receipt.Status))
	}
}

// parseHash decodes a 32-byte hash in hex, with or without the 0x prefix.
func parseHash(raw string) (common.Hash, error) {
	s := strings.TrimSpace(raw)
	if len(s) < 2 || s[0] != '0' || (s[1] != 'x' && s[1] != 'X') {
		return common.Hash{}, fmt.Errorf("hash must be hex with a 0x prefix, got %q", raw)
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	if len(s) != 64 {
		return common.Hash{}, fmt.Errorf("hash must be 32 bytes, got %d hex characters", len(s))
	}
	h := common.HexToHash(raw)
	if h == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("hash must not be zero")
	}
	return h, nil
}
