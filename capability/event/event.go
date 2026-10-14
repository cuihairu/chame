// Package event defines the standard on-chain event model and delivery
// contract (spec §11): unique event identities, deduplication, at-least-
// once delivery with idempotent consumers, cursor-based resumable
// consumption, and reorganization handling. Event models differ between
// chains and are never assumed identical (§11.1); the model below carries
// only what every source can express, with the chain-specific parts kept
// raw and positioned.
package event

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/cuihairu/chame/core/identifiers"
)

// SourceType names the origin of an event (§11.2).
type SourceType string

const (
	// SourceEVMLog is an EVM log event (§11.1).
	SourceEVMLog SourceType = "evm-log"
	// SourceSolanaLog is a Solana transaction-log event (§11.1).
	SourceSolanaLog SourceType = "solana-log"
)

// Valid reports whether s is a known source type.
func (s SourceType) Valid() bool {
	return s == SourceEVMLog || s == SourceSolanaLog
}

// ObservationState is the event's current on-chain observation (§11.2).
type ObservationState string

const (
	// ObservationObserved is the event's block still belongs to the
	// current chain.
	ObservationObserved ObservationState = "observed"
	// ObservationReorged marks an event whose block was removed by a
	// reorganization (§11.6).
	ObservationReorged ObservationState = "reorged"
)

// FinalityState is the event's finality state (§11.2, §11.4). Delivery is
// gated on it: an event is not delivered until its state reaches the
// configured requirement.
type FinalityState string

const (
	FinalityPending   FinalityState = "pending"
	FinalityConfirmed FinalityState = "confirmed"
	FinalityFinal     FinalityState = "final"
)

// Rank orders the finality states so a requirement can be expressed as
// "at least this state".
func (f FinalityState) Rank() int {
	switch f {
	case FinalityPending:
		return 0
	case FinalityConfirmed:
		return 1
	case FinalityFinal:
		return 2
	default:
		return -1
	}
}

// Valid reports whether f is a known finality state.
func (f FinalityState) Valid() bool { return f.Rank() >= 0 }

// ProcessingState is the delivery state (§11.4).
type ProcessingState string

const (
	// ProcessingQueued is an event waiting for delivery; attempts above
	// zero mean the next attempt is deferred by the retry backoff.
	ProcessingQueued ProcessingState = "queued"
	// ProcessingDelivered means the handler succeeded. Terminal:
	// redelivery is impossible, replay is the consumer's job.
	ProcessingDelivered ProcessingState = "delivered"
	// ProcessingReorged marks an event invalidated by a reorganization
	// before it completed (§11.6 step 2). It is never delivered.
	ProcessingReorged ProcessingState = "reorged"
	// ProcessingPoisoned means the event exhausted its retry budget. It
	// is terminal and visible; compensating it is the application's job
	// (§11.6: business compensation cannot be automatic).
	ProcessingPoisoned ProcessingState = "poisoned"
)

// Valid reports whether s is a known processing state.
func (s ProcessingState) Valid() bool {
	switch s {
	case ProcessingQueued, ProcessingDelivered, ProcessingReorged, ProcessingPoisoned:
		return true
	default:
		return false
	}
}

// EventID is the unique identity of one event (§11.3). A transaction
// hash alone is never enough: one transaction may emit several events, so
// the identity includes the chain-specific position. Build one with
// PositionKey.
type EventID string

// PositionKey builds the unique event identity from the chain-specific
// position (§11.3): network, source type, transaction, block location,
// in-transaction index, and an extra locator when the source cannot give
// a stable index.
func PositionKey(network identifiers.NetworkID, src SourceType, transactionID, blockHash string, blockHeight, index uint64, locator string) EventID {
	return EventID(fmt.Sprintf("%s|%s|%s|%s|%d|%d|%s",
		network.String(), src, transactionID, blockHash, blockHeight, index, locator))
}

// Event is the chain-agnostic event record (§11.2). Data is the raw
// event payload, owned by the source adapter and decoded by the consumer;
// the shared model never interprets it.
type Event struct {
	// ID is the unique event identity (§11.3).
	ID EventID
	// Network pins the event to one logical network.
	Network identifiers.NetworkID
	// SourceType is the event origin (§11.1).
	SourceType SourceType
	// TransactionID is the transaction hash or signature.
	TransactionID string
	// BlockHash and BlockHeight are the chain-specific location: block
	// hash and height for EVM, slot reference and slot for Solana
	// (§11.2, §11.5). They carry enough to detect a reorganization.
	BlockHash   string
	BlockHeight uint64
	// Index is the in-transaction event position when the source gives
	// one (§11.3).
	Index uint64
	// Locator is the extra position information used when a stable index
	// is unavailable (§11.3).
	Locator string
	// Type is the normalized event type used to select a handler.
	Type string
	// Data is the raw event payload.
	Data json.RawMessage
	// DecodeVersion is the decoder version that produced Type.
	DecodeVersion int
	// At is when the event was first observed.
	At time.Time
	// Observation is the current on-chain observation state (§11.6).
	Observation ObservationState
	// Finality gates delivery (§11.4).
	Finality FinalityState
	// Processing is the delivery state (§11.4).
	Processing ProcessingState
	// Attempts counts delivery attempts (§11.2).
	Attempts int
	// NextAttemptAt defers the next attempt after a failure.
	NextAttemptAt time.Time
	// CreatedAt and UpdatedAt are framework timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks the record shape every store persists.
func (e Event) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("event: id must not be empty")
	}
	if err := e.Network.Validate(); err != nil {
		return fmt.Errorf("event %s: %w", e.ID, err)
	}
	if !e.SourceType.Valid() {
		return fmt.Errorf("event %s: unknown source type %q", e.ID, e.SourceType)
	}
	if !e.Observation.Valid() {
		return fmt.Errorf("event %s: unknown observation state %q", e.ID, e.Observation)
	}
	if !e.Finality.Valid() {
		return fmt.Errorf("event %s: unknown finality state %q", e.ID, e.Finality)
	}
	if !e.Processing.Valid() {
		return fmt.Errorf("event %s: unknown processing state %q", e.ID, e.Processing)
	}
	if e.Type == "" {
		return fmt.Errorf("event %s: normalized type must not be empty", e.ID)
	}
	if e.At.IsZero() || e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		return fmt.Errorf("event %s: observation and timestamps must be set", e.ID)
	}
	return nil
}

// Valid reports whether s is a known observation state.
func (s ObservationState) Valid() bool {
	return s == ObservationObserved || s == ObservationReorged
}

// Cursor is the resume position of one (network, source) pair (§11.5). It
// never holds a bare integer: the block hash lets a rollback or a chain
// swap at the same height be detected.
type Cursor struct {
	// Network pins the cursor to one logical network.
	Network identifiers.NetworkID
	// SourceType names the source whose position this tracks.
	SourceType SourceType
	// BlockHeight is the last committed position consumed.
	BlockHeight uint64
	// BlockHash is the chain location at BlockHeight.
	BlockHash string
	// UpdatedAt is when the cursor last moved.
	UpdatedAt time.Time
	// RollbackCount and LastRollback record reorganization rollbacks,
	// the audit trail required by §11.6.
	RollbackCount int
	LastRollback  time.Time
}

// CursorKey names one cursor.
type CursorKey struct {
	Network    identifiers.NetworkID
	SourceType SourceType
}

// Validate checks the network and source type.
func (k CursorKey) Validate() error {
	if err := k.Network.Validate(); err != nil {
		return fmt.Errorf("cursor key: %w", err)
	}
	if !k.SourceType.Valid() {
		return fmt.Errorf("cursor key: unknown source type %q", k.SourceType)
	}
	return nil
}

// Key returns the cursor addressable by (network, source type).
func (c Cursor) Key() CursorKey {
	return CursorKey{Network: c.Network, SourceType: c.SourceType}
}
