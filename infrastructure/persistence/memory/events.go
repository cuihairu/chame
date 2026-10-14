package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/identifiers"
)

// EventStore is an in-memory event.Repository (spec §11). It exists for
// unit tests and examples only: records live in one process's memory,
// survive nothing, and coordinate nothing across instances. Production
// deployments use a durable implementation (ADR-0002). The zero value is
// not usable; construct with NewEventStore. Safe for concurrent use.
type EventStore struct {
	mu      sync.Mutex
	events  map[event.EventID]event.Event
	cursors map[string]event.Cursor
}

// NewEventStore constructs an empty store.
func NewEventStore() *EventStore {
	return &EventStore{
		events:  map[event.EventID]event.Event{},
		cursors: map[string]event.Cursor{},
	}
}

// cursorKey builds the (network, source) index entry.
func cursorKey(k event.CursorKey) string {
	return k.Network.String() + "\x00" + string(k.SourceType)
}

// Create implements event.Repository: identity uniqueness (§11.3). A
// record with the same identity fails with ErrDuplicateEvent, whatever its
// content — a re-poll never stores the same event twice.
func (s *EventStore) Create(_ context.Context, ev event.Event) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.events[ev.ID]; ok {
		return fmt.Errorf("%w: %s", event.ErrDuplicateEvent, ev.ID)
	}
	s.events[ev.ID] = ev
	return nil
}

// Get implements event.Repository.
func (s *EventStore) Get(_ context.Context, id event.EventID) (event.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, ok := s.events[id]
	if !ok {
		return event.Event{}, fmt.Errorf("%w: %s", event.ErrEventNotFound, id)
	}
	return ev, nil
}

// FindPending implements event.Repository: ready events in chain order
// (block height, then in-transaction index), deterministically tie-broken
// by observation time and identity.
func (s *EventStore) FindPending(_ context.Context, q event.PendingQuery) ([]event.Event, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	minRank := q.MinFinality.Rank()
	zeroNetwork := identifiers.NetworkID{}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []event.Event
	for _, ev := range s.events {
		if ev.Processing != event.ProcessingQueued || ev.Observation != event.ObservationObserved {
			continue
		}
		if ev.Finality.Rank() < minRank {
			continue
		}
		if !ev.NextAttemptAt.IsZero() && ev.NextAttemptAt.After(q.NotBefore) {
			continue
		}
		if q.Network != zeroNetwork && !ev.Network.Equal(q.Network) {
			continue
		}
		if q.SourceType != "" && ev.SourceType != q.SourceType {
			continue
		}
		out = append(out, ev)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BlockHeight != out[j].BlockHeight {
			return out[i].BlockHeight < out[j].BlockHeight
		}
		if out[i].Index != out[j].Index {
			return out[i].Index < out[j].Index
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// MarkDelivered implements event.Repository: compare-and-swap on the
// processing state (§11.4).
func (s *EventStore) MarkDelivered(_ context.Context, id event.EventID, at time.Time) error {
	return s.casProcessing(id, at, func(ev *event.Event) {
		ev.Processing = event.ProcessingDelivered
	})
}

// RecordFailure implements event.Repository: the event stays queued, the
// attempt count grows and the next attempt is deferred.
func (s *EventStore) RecordFailure(_ context.Context, id event.EventID, nextAttempt, at time.Time) error {
	return s.casProcessing(id, at, func(ev *event.Event) {
		ev.Attempts++
		ev.NextAttemptAt = nextAttempt
	})
}

// MarkPoisoned implements event.Repository: a permanently failing event
// becomes visible instead of vanishing (§11.4).
func (s *EventStore) MarkPoisoned(_ context.Context, id event.EventID, at time.Time) error {
	return s.casProcessing(id, at, func(ev *event.Event) {
		ev.Processing = event.ProcessingPoisoned
	})
}

// SetFinality implements event.Repository: only a queued event's finality
// moves, so delivery can start as soon as confirmation is reached.
func (s *EventStore) SetFinality(_ context.Context, id event.EventID, f event.FinalityState, at time.Time) error {
	if !f.Valid() {
		return fmt.Errorf("event: unknown finality state %q", f)
	}
	return s.casProcessing(id, at, func(ev *event.Event) {
		ev.Finality = f
	})
}

// casProcessing applies a queued-only update atomically. A record in any
// other state fails with ErrProcessingConflict; a missing record fails
// with ErrEventNotFound.
func (s *EventStore) casProcessing(id event.EventID, at time.Time, apply func(*event.Event)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev, ok := s.events[id]
	if !ok {
		return fmt.Errorf("%w: %s", event.ErrEventNotFound, id)
	}
	if ev.Processing != event.ProcessingQueued {
		return fmt.Errorf("%w: event %s is %s",
			event.ErrProcessingConflict, id, ev.Processing)
	}
	apply(&ev)
	ev.UpdatedAt = at
	s.events[id] = ev
	return nil
}

// MarkReorged implements event.Repository (§11.6 steps 2 and 3): queued
// events at blockHeight or later are invalidated and never delivered;
// delivered events stay terminal — their compensation is the
// application's decision.
func (s *EventStore) MarkReorged(_ context.Context, key event.CursorKey, blockHeight uint64) (int, error) {
	if err := key.Validate(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, ev := range s.events {
		if ev.Network != key.Network || ev.SourceType != key.SourceType {
			continue
		}
		if ev.BlockHeight < blockHeight || ev.Processing != event.ProcessingQueued {
			continue
		}
		ev.Observation = event.ObservationReorged
		ev.Processing = event.ProcessingReorged
		s.events[id] = ev
		n++
	}
	return n, nil
}

// GetCursor implements event.Repository.
func (s *EventStore) GetCursor(_ context.Context, key event.CursorKey) (event.Cursor, error) {
	if err := key.Validate(); err != nil {
		return event.Cursor{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cursors[cursorKey(key)]
	if !ok {
		return event.Cursor{}, fmt.Errorf("%w: %s %s", event.ErrCursorNotFound, key.Network, key.SourceType)
	}
	return c, nil
}

// SaveCursor implements event.Repository: monotonicity guard (§11.5). A
// backwards height or a different hash at the same height is a regression
// — the chain the cursor trusted no longer exists.
func (s *EventStore) SaveCursor(_ context.Context, c event.Cursor) error {
	if err := c.Key().Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := cursorKey(c.Key())
	stored, ok := s.cursors[k]
	if ok {
		if c.BlockHeight < stored.BlockHeight {
			return fmt.Errorf("%w: height %d is below stored %d",
				event.ErrCursorRegression, c.BlockHeight, stored.BlockHeight)
		}
		if c.BlockHeight == stored.BlockHeight && c.BlockHash != "" && stored.BlockHash != "" && c.BlockHash != stored.BlockHash {
			return fmt.Errorf("%w: height %d already holds block %s, not %s",
				event.ErrCursorRegression, c.BlockHeight, stored.BlockHash, c.BlockHash)
		}
	}
	s.cursors[k] = c
	return nil
}

// RollbackCursor implements event.Repository (§11.6 steps 5 and 6): the
// cursor moves to an earlier canonical position and the rollback is
// recorded — RollbackCount increments and LastRollback stamps the time.
func (s *EventStore) RollbackCursor(_ context.Context, c event.Cursor) error {
	if err := c.Key().Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := cursorKey(c.Key())
	stored, ok := s.cursors[k]
	if ok {
		c.RollbackCount = stored.RollbackCount + 1
	}
	if c.LastRollback.IsZero() {
		c.LastRollback = c.UpdatedAt
	}
	s.cursors[k] = c
	return nil
}

var _ event.Repository = (*EventStore)(nil)
