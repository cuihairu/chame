// Package gameledger defines small, host-facing contracts for projecting
// chain observations into a game-owned ledger. It is not a production ledger.
//
// The chain-qualified asset identity both the adapters and this ledger use
// lives in capability/asset (design: game-asset-ownership-and-ledger-boundary);
// this package carries the projection reference contract, not the identity.
package gameledger

import (
	"errors"

	"github.com/cuihairu/chame/capability/asset"
)

// ObservationState is the projection's treatment of a chain event.
type ObservationState string

const (
	Pending   ObservationState = "pending"
	Confirmed ObservationState = "confirmed"
	Final     ObservationState = "final"
	Reorged   ObservationState = "reorged"
)

// EventKey must identify one canonical chain event, e.g. network + tx hash +
// log index (EVM) or network + signature + instruction/event index (Solana).
// It is deliberately independent of a player's request idempotency key.
type EventKey string

// Entry is a minimal example projection record. Business schemas and storage
// remain owned by the host game.
type Entry struct {
	Key         EventKey
	Player      string
	Asset       asset.AssetIdentity
	Delta       int64
	State       ObservationState
	Compensated bool

	// delivered remembers whether the observation ever counted toward a
	// balance: a reorged entry keeps answering its compensation amount
	// until the host confirms the inverse operation applied.
	delivered bool
}

// Ledger is an executable reference contract for exactly-once projection.
// Production hosts should enforce equivalent uniqueness transactionally.
type Ledger struct{ entries map[EventKey]Entry }

func NewLedger() *Ledger { return &Ledger{entries: make(map[EventKey]Entry)} }

// Apply inserts an observation once. A duplicate key is a no-op, matching
// repository CreateInserted/CreateSameKeyExisting semantics.
func (l *Ledger) Apply(e Entry) (inserted bool, err error) {
	if e.Key == "" || e.Player == "" {
		return false, errors.New("event key and player are required")
	}
	a, err := e.Asset.Normalize()
	if err != nil {
		return false, err
	}
	e.Asset = a
	if e.State != Pending && e.State != Confirmed && e.State != Final {
		return false, errors.New("new entry state must be pending, confirmed, or final")
	}
	if e.State == Confirmed || e.State == Final {
		e.delivered = true
	}
	if old, ok := l.entries[e.Key]; ok {
		if old.Player != e.Player || old.Asset != e.Asset || old.Delta != e.Delta {
			return false, errors.New("event key reused with different payload")
		}
		return false, nil
	}
	l.entries[e.Key] = e
	return true, nil
}

// SetState tracks finality/reorg. Moving an entry to reorged answers the
// compensation amount the host must apply as the inverse business
// operation, in the same DB transaction as its bookkeeping. The amount
// stays answerable on every reorged read until the host records the
// operation applied (MarkCompensated): a host that stops between the
// mark and the inverse operation must be able to retry without losing
// the amount, and a compensated entry answers nothing.
func (l *Ledger) SetState(key EventKey, state ObservationState) (compensate int64, err error) {
	e, ok := l.entries[key]
	if !ok {
		return 0, errors.New("entry not found")
	}
	if state != Pending && state != Confirmed && state != Final && state != Reorged {
		return 0, errors.New("invalid observation state")
	}
	if e.State == Reorged && state != Reorged {
		return 0, errors.New("reorged event cannot be resurrected")
	}
	if state != Reorged && observationRank(state) < observationRank(e.State) {
		return 0, errors.New("observation state cannot move backwards")
	}
	if state == Reorged && !e.Compensated && e.delivered {
		// Only an observation the balance already counted earns a
		// reversal; a pending entry never became spendable.
		compensate = -e.Delta
	}
	e.State = state
	if e.State == Confirmed || e.State == Final {
		e.delivered = true
	}
	l.entries[key] = e
	return compensate, nil
}

// MarkCompensated records that the host applied the inverse business
// operation for a reorged entry: the entry stops answering its
// compensation amount, and its delta stays excluded from spendable
// balances. Only a reorged entry can be compensated; marking twice is a
// no-op.
func (l *Ledger) MarkCompensated(key EventKey) error {
	e, ok := l.entries[key]
	if !ok {
		return errors.New("entry not found")
	}
	if e.State != Reorged {
		return errors.New("only a reorged entry can be compensated")
	}
	if e.Compensated {
		return nil
	}
	e.Compensated = true
	l.entries[key] = e
	return nil
}

func observationRank(s ObservationState) int {
	switch s {
	case Pending:
		return 0
	case Confirmed:
		return 1
	case Final:
		return 2
	default:
		return -1
	}
}

// SpendableDelta excludes pending and reorged observations.
func (l *Ledger) SpendableDelta(key EventKey) (int64, bool) {
	e, ok := l.entries[key]
	if !ok || (e.State != Confirmed && e.State != Final) || e.Compensated {
		return 0, false
	}
	return e.Delta, true
}

func (l *Ledger) Entry(key EventKey) (Entry, bool) { e, ok := l.entries[key]; return e, ok }
