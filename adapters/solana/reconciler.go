package solana

import (
	"context"

	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
)

// ChainRecord is the adapter-owned payload a Solana transaction record
// carries in ChainData: the intent the flow builds from, the blockhash
// window the build pinned with the commitment it read it at, the
// required signer order, and the message and transaction encodings each
// later phase needs. The recovery worker reads the pin back to judge a
// signature the node no longer holds; a resumed flow signs and
// broadcasts from what is stored instead of re-asking the chain.
type ChainRecord struct {
	Intent     *BuildQuery           `json:"intent,omitempty"`
	Pin        BlockhashPin          `json:"pin,omitempty"`
	Commitment Commitment            `json:"commitment,omitempty"`
	Signers    []identifiers.Address `json:"signers,omitempty"`
	Unsigned   []byte                `json:"unsigned,omitempty"`
	Signed     []byte                `json:"signed,omitempty"`
}

// StatusOf reports the strongest state the chain currently shows for one
// recorded transaction (spec §7.8). The node's answer is decisive when
// it holds one — including its refusal, which is terminal. An
// unobserved signature is judged by the pinned window instead: still
// open means the transaction may still land, closed means it may or may
// not have run and stays unknown. A record without a chain reference has
// nothing to ask about and reports its own status — the submission flow
// that lost the reference is a bookkeeping bug, not a chain fact.
//
// The ledger-wide signature search stays off on purpose: with the window
// closed and nothing observed, searched and unsearched both map to
// unknown here, so the extra scan would buy the reconciler nothing. The
// rebuild decision belongs to the flow, which can ask Expire directly.
func (n *Network) StatusOf(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error) {
	if tx.ChainHash == "" {
		return tx.Status, nil
	}
	observed, err := n.TransactionStatus(ctx, tx.ChainHash)
	if err != nil {
		return "", err
	}
	if observed == nil {
		return n.unobservedStatus(ctx, tx)
	}
	// An error detail means the transaction executed and did not
	// succeed: terminal, whatever confirmation depth it reached.
	if observed.ErrDetail != "" {
		return transaction.TxReverted, nil
	}
	switch observed.Status {
	case string(rpc.ConfirmationStatusFinalized):
		return transaction.TxFinalized, nil
	case string(rpc.ConfirmationStatusConfirmed):
		return transaction.TxConfirmed, nil
	default:
		// processed: seen in a block, still shallow enough to be
		// reorganised away — included, not yet confirmed.
		return transaction.TxIncluded, nil
	}
}

// unobservedStatus classifies a signature the node holds no status for.
// Without the adapter's own chain record there is no window to judge, so
// the outcome stays unknown.
func (n *Network) unobservedStatus(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error) {
	rec, ok := tx.ChainData.(*ChainRecord)
	if !ok || rec == nil {
		return transaction.TxUnknown, nil
	}
	// The expiry check reads the height at finalized: a window that has
	// closed even by the strictest commitment is closed for good.
	height, err := n.BlockHeight(ctx, CommitmentFinalized)
	if err != nil {
		return "", err
	}
	if rec.Pin.Valid(height) == ExpiryValid {
		return transaction.TxPending, nil
	}
	return transaction.TxUnknown, nil
}
