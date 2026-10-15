package evm

import (
	"context"

	"github.com/cuihairu/chame/capability/transaction"
)

// StatusOf reports the strongest state the chain currently shows for one
// recorded transaction (spec §7.8). It is the observation seam the
// recovery worker drives (spec §10.4): the record supplies its chain
// hash, the chain answers, and the receipt's business detail stays with
// whoever holds the record. A record without a chain reference has
// nothing to ask about and reports its own status — the submission flow
// that lost the reference is a bookkeeping bug, not a chain fact.
func (n *Network) StatusOf(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error) {
	if tx.ChainHash == "" {
		return tx.Status, nil
	}
	status, _, err := n.Receipt(ctx, tx.ChainHash)
	return status, err
}
