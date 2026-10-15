package transaction

import (
	"context"
	"fmt"

	txmodel "github.com/cuihairu/chame/capability/transaction"
)

// StatusQuery is the chain-agnostic observation seam the status
// reconciler drives (§10.4). Adapters implement it on their own types —
// the framework sees only the method set, so no adapter imports the
// application and no application imports an adapter. The record goes in
// whole because its chain reference and its adapter-owned chain data are
// both adapter business: the adapter reads back what it wrote.
type StatusQuery interface {
	// StatusOf reports the strongest state the chain currently shows for
	// this record. A record the chain cannot describe reports
	// txmodel.TxUnknown, never a guess. Returning the record's own
	// status is a legitimate "nothing new" observation.
	StatusOf(ctx context.Context, tx txmodel.Transaction) (txmodel.TransactionStatus, error)
}

// StatusReconciler is the shared Reconciler implementation over any
// adapter that answers StatusOf. It owns the parts of recovery that are
// not chain-specific: refusing to query records without a chain
// reference, and shaping the observation so the shared state machine
// accepts it without ever writing something untrue.
type StatusReconciler struct {
	query StatusQuery
}

// NewStatusReconciler wires a reconciler over one observation seam.
func NewStatusReconciler(query StatusQuery) (*StatusReconciler, error) {
	if query == nil {
		return nil, fmt.Errorf("transaction reconciler: status query is required")
	}
	return &StatusReconciler{query: query}, nil
}

// Reconcile observes the chain and reports the status the record should
// advance to. The worker applies it through the repository's
// conditional update, so this method never writes state (§10.4).
func (r *StatusReconciler) Reconcile(ctx context.Context, tx txmodel.Transaction) (txmodel.TransactionStatus, error) {
	// A record without a chain reference has nothing to ask about. The
	// flow that submitted it must record the reference; a query would
	// only turn that bookkeeping gap into an error loop.
	if tx.ChainHash == "" {
		return tx.Status, nil
	}
	observed, err := r.query.StatusOf(ctx, tx)
	if err != nil {
		return "", err
	}
	if observed == "" || observed == tx.Status {
		return tx.Status, nil
	}
	// The chain answers with its strongest truth; the matrix may only
	// accept a weaker step from where the record stands. Each weaker
	// step is still true — finalized implies confirmed implies included
	// implies pending — so the record converges by walking down what it
	// observed, not by rewriting it.
	for _, next := range weaken(observed) {
		if next == tx.Status {
			break
		}
		if txmodel.CanTransition(tx.Status, next) {
			return next, nil
		}
	}
	// Nothing lawful matched: reopen through unknown when the matrix
	// allows it — that is the status that says "resolve me again on the
	// next scan" — and otherwise leave the record as it stands.
	if txmodel.CanTransition(tx.Status, txmodel.TxUnknown) {
		return txmodel.TxUnknown, nil
	}
	return tx.Status, nil
}

// weaken lists s and every strictly weaker status it implies, strongest
// first. Terminal outcomes (reverted, failed) imply nothing: a
// transaction that executed with a failing result is not "a bit
// pending".
func weaken(s txmodel.TransactionStatus) []txmodel.TransactionStatus {
	switch s {
	case txmodel.TxFinalized:
		return []txmodel.TransactionStatus{txmodel.TxFinalized, txmodel.TxConfirmed, txmodel.TxIncluded, txmodel.TxPending}
	case txmodel.TxConfirmed:
		return []txmodel.TransactionStatus{txmodel.TxConfirmed, txmodel.TxIncluded, txmodel.TxPending}
	case txmodel.TxIncluded:
		return []txmodel.TransactionStatus{txmodel.TxIncluded, txmodel.TxPending}
	case txmodel.TxPending:
		return []txmodel.TransactionStatus{txmodel.TxPending}
	default:
		return []txmodel.TransactionStatus{s}
	}
}
