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
	if old, ok := l.entries[e.Key]; ok {
		if old.Player != e.Player || old.Asset != e.Asset || old.Delta != e.Delta {
			return false, errors.New("event key reused with different payload")
		}
		return false, nil
	}
	l.entries[e.Key] = e
	return true, nil
}

// SetState tracks finality/reorg. Reorg compensation is marked once; host
// code must apply the inverse business operation in the same DB transaction.
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
	if state == Reorged && !e.Compensated && (e.State == Confirmed || e.State == Final) {
		e.Compensated = true
		compensate = -e.Delta
	}
	if state == Reorged {
		e.Compensated = true
	}
	e.State = state
	l.entries[key] = e
	return compensate, nil
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
