// Package transaction defines the shared transaction model and state
// machine (spec §9). The model carries identity, lifecycle status, and
// idempotency scope; chain-specific payloads stay adapter-owned and are
// never flattened into one universal format (spec §21.2).
//
// The status set expresses uncertainty explicitly: an operation whose
// outcome cannot be determined is Unknown, never a silent success or
// failure (spec §7.8, §22).
package transaction

import (
	"errors"
	"fmt"
	"time"

	"github.com/cuihairu/chame/core/identifiers"
)

// OperationID identifies one business operation. An operation may drive
// zero or more transactions (retries, replacements) and is the anchor
// for idempotency (spec §10.1).
type OperationID string

// TransactionID identifies one chain transaction across its whole
// lifecycle, independent of the chain-level hash.
type TransactionID string

// TransactionStatus is the chain-agnostic lifecycle status (spec §9.1,
// §7.8). The full legal transition matrix lives in statemachine.go.
type TransactionStatus string

const (
	// TxCreated marks a recorded operation before any chain material
	// exists.
	TxCreated TransactionStatus = "created"
	// TxPrepared marks built parameters, unsigned.
	TxPrepared TransactionStatus = "prepared"
	// TxSigned marks a signed transaction not yet offered to the chain.
	TxSigned TransactionStatus = "signed"
	// TxSubmitted marks a transaction the RPC layer accepted for
	// broadcast; the outcome is not yet observed.
	TxSubmitted TransactionStatus = "submitted"
	// TxPending marks a transaction seen in the mempool.
	TxPending TransactionStatus = "pending"
	// TxIncluded marks a transaction included in a block.
	TxIncluded TransactionStatus = "included"
	// TxConfirmed marks a transaction that reached the application's
	// confirmation policy (depth or commitment).
	TxConfirmed TransactionStatus = "confirmed"
	// TxFinalized marks a transaction that reached the network's
	// finality requirement. Terminal.
	TxFinalized TransactionStatus = "finalized"
	// TxReverted marks a transaction executed by the chain with a
	// failing result. Terminal.
	TxReverted TransactionStatus = "reverted"
	// TxFailed marks a transaction rejected before execution (invalid,
	// dropped, never accepted). Terminal.
	TxFailed TransactionStatus = "failed"
	// TxReorged marks a transaction whose observed block was removed by
	// a reorganization. The outcome is open again.
	TxReorged TransactionStatus = "reorged"
	// TxUnknown marks a transaction whose current outcome cannot be
	// reliably determined. It resolves only through reconciliation.
	TxUnknown TransactionStatus = "unknown"
)

// ErrIllegalTransition is returned when a status change is not part of
// the legal matrix. Use errors.Is to detect it.
var ErrIllegalTransition = errors.New("transaction: illegal status transition")

// String returns the status name.
func (s TransactionStatus) String() string { return string(s) }

// Valid reports whether s is a known status.
func (s TransactionStatus) Valid() bool {
	_, ok := legalTransitions[s]
	return ok
}

// Terminal reports whether s ends the lifecycle. Terminal statuses accept
// no further transitions.
func (s TransactionStatus) Terminal() bool {
	switch s {
	case TxFinalized, TxReverted, TxFailed:
		return true
	default:
		return false
	}
}

// Uncertain reports whether the status expresses an unresolved outcome
// (spec §22: 交易状态能够表达不确定性).
func (s TransactionStatus) Uncertain() bool {
	return s == TxUnknown || s == TxReorged
}

// CanTransition reports whether moving from one status to another is
// legal. The matrix is the single source of truth; repositories use it
// for optimistic conditional updates (spec §10.2).
func CanTransition(from, to TransactionStatus) bool {
	if !from.Valid() || !to.Valid() {
		return false
	}
	if from == to {
		return true // idempotent observation of the same status
	}
	for _, next := range legalTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// TransitionsFrom lists the statuses reachable from s, in declaration
// order. Used by docs, tooling, and tests.
func TransitionsFrom(s TransactionStatus) []TransactionStatus {
	if !s.Valid() {
		return nil
	}
	out := make([]TransactionStatus, len(legalTransitions[s]))
	copy(out, legalTransitions[s])
	return out
}

// Transaction is the chain-agnostic transaction record (spec §9.1).
// Chain-specific detail travels in ChainData, owned by the adapter that
// produced it; the shared model never interprets it.
type Transaction struct {
	// ID is the framework-level identity.
	ID TransactionID `json:"id"`
	// OperationID links the transaction to its business operation.
	OperationID OperationID `json:"operation_id"`
	// Network pins the transaction to one logical network. Records of
	// different networks never mix (spec §19 Phase 3).
	Network identifiers.NetworkID `json:"network"`
	// Status is the current lifecycle status.
	Status TransactionStatus `json:"status"`
	// IdempotencyKey is the application-level key within its scope, if
	// any (spec §10.1).
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// RequestHash fingerprints the business request that produced this
	// transaction; equal keys with different hashes conflict (§10.1).
	RequestHash string `json:"request_hash,omitempty"`
	// Sender and Recipient are family-validated addresses.
	Sender    identifiers.Address `json:"sender,omitempty"`
	Recipient identifiers.Address `json:"recipient,omitempty"`
	// Amount is the integer-only value moved, when applicable.
	Amount identifiers.Amount `json:"amount,omitempty"`
	// ChainHash is the chain-level reference (EVM hash, Solana
	// signature) once one exists.
	ChainHash string `json:"chain_hash,omitempty"`
	// ChainData is the adapter-owned payload (encoded EVM transaction,
	// Solana message, ...). Opaque to the shared model.
	ChainData any `json:"-"`
	// CreatedAt and UpdatedAt are framework timestamps.
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TransitionTo returns a copy of t advanced to next. It never mutates the
// receiver; persistence layers enforce the same matrix atomically
// (spec §10.2).
func (t Transaction) TransitionTo(next TransactionStatus, at time.Time) (Transaction, error) {
	if !CanTransition(t.Status, next) {
		return Transaction{}, fmt.Errorf("%w: %s -> %s on transaction %s",
			ErrIllegalTransition, t.Status, next, t.ID)
	}
	out := t
	out.Status = next
	out.UpdatedAt = at
	return out, nil
}
