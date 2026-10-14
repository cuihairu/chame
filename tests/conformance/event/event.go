package event

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/identifiers"
)

// newStoreFunc builds a fresh, empty repository for one subtest. The
// store must contain no events or cursors when the subtest starts.
type newStoreFunc func(t *testing.T) event.Repository

// Run executes the full event store suite against a fresh store per
// subtest. The store is expected to be empty at start; tests never share
// records.
func Run(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	t.Run("IdentityUniqueness", func(t *testing.T) { runIdentity(t, newStore) })
	t.Run("PendingScan", func(t *testing.T) { runPendingScan(t, newStore) })
	t.Run("DeliveryUpdates", func(t *testing.T) { runDelivery(t, newStore) })
	t.Run("ReorgInvalidation", func(t *testing.T) { runReorg(t, newStore) })
	t.Run("Cursors", func(t *testing.T) { runCursors(t, newStore) })
	t.Run("RecordValidation", func(t *testing.T) { runValidation(t, newStore) })
}

var (
	// evmNet and solNet pin different networks so scoping can be pinned.
	evmNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "conformance", ChainID: "1337"}
	solNet = identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "conformance", ChainID: "devnet"}
)

// newEvent builds a delivery-ready event: observed on the canonical
// chain, confirmed finality, queued for delivery.
func newEvent(net identifiers.NetworkID, src event.SourceType, txHash, blockHash string, height, index uint64, at time.Time) event.Event {
	id := event.PositionKey(net, src, txHash, blockHash, height, index, "")
	return event.Event{
		ID:            id,
		Network:       net,
		SourceType:    src,
		TransactionID: txHash,
		BlockHash:     blockHash,
		BlockHeight:   height,
		Index:         index,
		Type:          "deposit.detected",
		Data:          json.RawMessage(`{"amount":"1"}`),
		DecodeVersion: 1,
		At:            at,
		Observation:   event.ObservationObserved,
		Finality:      event.FinalityConfirmed,
		Processing:    event.ProcessingQueued,
		CreatedAt:     at,
		UpdatedAt:     at,
	}
}

func mustCreate(t *testing.T, repo event.Repository, ev event.Event) {
	t.Helper()
	if err := repo.Create(context.Background(), ev); err != nil {
		t.Fatalf("create %s: unexpected error: %v", ev.ID, err)
	}
}

// runIdentity pins §11.3: one stored record per event identity. A
// transaction hash alone is not an identity — the position components
// separate events of the same transaction — and a re-poll of an already
// stored event stays a no-op at the store.
func runIdentity(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	ctx := context.Background()
	repo := newStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	base := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xblock", 10, 2, at)
	mustCreate(t, repo, base)

	t.Run("same identity conflicts even with other content", func(t *testing.T) {
		twin := base
		twin.Type = "deposit.leg-two"
		err := repo.Create(ctx, twin)
		if !errors.Is(err, event.ErrDuplicateEvent) {
			t.Fatalf("want ErrDuplicateEvent, got %v", err)
		}
	})

	t.Run("siblings of one transaction are distinct identities", func(t *testing.T) {
		mustCreate(t, repo, newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xblock", 10, 3, at))
		got, err := repo.Get(ctx, base.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ID != base.ID || got.Index != 2 {
			t.Fatalf("index 2 event was overwritten by its sibling: %+v", got)
		}
	})

	t.Run("get round trips the record", func(t *testing.T) {
		got, err := repo.Get(ctx, base.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Network != base.Network || got.SourceType != base.SourceType ||
			got.TransactionID != base.TransactionID || got.BlockHash != base.BlockHash ||
			got.BlockHeight != base.BlockHeight || got.Index != base.Index ||
			got.Type != base.Type || got.Finality != base.Finality ||
			got.Processing != base.Processing || got.Observation != base.Observation ||
			string(got.Data) != string(base.Data) || got.DecodeVersion != base.DecodeVersion ||
			!got.At.Equal(base.At) {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", got, base)
		}
	})

	t.Run("missing record", func(t *testing.T) {
		_, err := repo.Get(ctx, "event:missing")
		if !errors.Is(err, event.ErrEventNotFound) {
			t.Fatalf("want ErrEventNotFound, got %v", err)
		}
	})
}

// runPendingScan pins the §11.4 delivery queue: chain-order scanning,
// bounded pages, deferral by NextAttemptAt, and finality gating.
func runPendingScan(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	ctx := context.Background()
	repo := newStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Out-of-chain-order insertion: the scan must still walk chain order.
	notReady := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb4", 40, 0, at)
	notReady.Finality = event.FinalityPending
	deferred := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb5", 50, 0, at)
	deferred.NextAttemptAt = at.Add(time.Hour)
	otherNet := newEvent(solNet, event.SourceSolanaLog, "sig", "slot", 60, 0, at)
	seed := []event.Event{
		newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb3", 30, 0, at),
		newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb1", 10, 5, at),
		newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb1", 10, 2, at),
		notReady,
		deferred,
		otherNet,
	}
	for _, ev := range seed {
		mustCreate(t, repo, ev)
	}

	t.Run("chain order, gated", func(t *testing.T) {
		got, err := repo.FindPending(ctx, event.PendingQuery{
			// After the seed instant but before the deferred attempt.
			NotBefore:   at.Add(30 * time.Minute),
			MinFinality: event.FinalityConfirmed,
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		// Ready: heights 10 (index 2 before 5) and 30, plus the other
		// network at 60. Not ready: pending finality (40), deferred
		// attempt (50, due in an hour).
		if len(got) != 4 {
			t.Fatalf("want 4 ready events, got %d: %+v", len(got), got)
		}
		// Chain position order across networks too.
		if got[0].BlockHeight != 10 || got[0].Index != 2 ||
			got[1].BlockHeight != 10 || got[1].Index != 5 ||
			got[2].BlockHeight != 30 || got[3].BlockHeight != 60 {
			t.Fatalf("wrong order: %+v", got)
		}
	})

	t.Run("limit bounds the page", func(t *testing.T) {
		got, err := repo.FindPending(ctx, event.PendingQuery{
			NotBefore:   at.Add(2 * time.Hour),
			MinFinality: event.FinalityConfirmed,
			Limit:       2,
		})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		if len(got) != 2 || got[0].BlockHeight != 10 {
			t.Fatalf("want the first 2 chain-ordered events, got %+v", got)
		}
	})

	t.Run("deferred attempts wait", func(t *testing.T) {
		got, err := repo.FindPending(ctx, event.PendingQuery{
			NotBefore:   at, // before the deferred attempt
			MinFinality: event.FinalityConfirmed,
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		for _, ev := range got {
			if ev.BlockHeight == 50 {
				t.Fatal("a deferred event must not be due yet")
			}
		}
	})

	t.Run("finality gates delivery", func(t *testing.T) {
		got, err := repo.FindPending(ctx, event.PendingQuery{
			NotBefore:   at.Add(2 * time.Hour),
			MinFinality: event.FinalityFinal,
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("no event reached final finality, got %+v", got)
		}
	})

	t.Run("network and source scope", func(t *testing.T) {
		got, err := repo.FindPending(ctx, event.PendingQuery{
			NotBefore:   at.Add(2 * time.Hour),
			MinFinality: event.FinalityConfirmed,
			Limit:       10,
			Network:     solNet,
			SourceType:  event.SourceSolanaLog,
		})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		if len(got) != 1 || got[0].ID != otherNet.ID {
			t.Fatalf("want only the solana event, got %+v", got)
		}
	})
}

// runDelivery pins the §11.4 conditional updates: exactly one outcome per
// event, delivered is terminal, failures accumulate visibly.
func runDelivery(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	ctx := context.Background()
	repo := newStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("delivered is terminal", func(t *testing.T) {
		ev := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb1", 10, 0, at)
		mustCreate(t, repo, ev)
		if err := repo.MarkDelivered(ctx, ev.ID, at.Add(time.Minute)); err != nil {
			t.Fatalf("deliver: %v", err)
		}
		got, err := repo.Get(ctx, ev.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Processing != event.ProcessingDelivered || !got.UpdatedAt.Equal(at.Add(time.Minute)) {
			t.Fatalf("delivery not stamped: %+v", got)
		}
		if err := repo.MarkDelivered(ctx, ev.ID, at.Add(2*time.Minute)); !errors.Is(err, event.ErrProcessingConflict) {
			t.Fatalf("second delivery must conflict, got %v", err)
		}
		if err := repo.RecordFailure(ctx, ev.ID, at.Add(3*time.Minute), at.Add(2*time.Minute)); !errors.Is(err, event.ErrProcessingConflict) {
			t.Fatalf("failure after delivery must conflict, got %v", err)
		}
		if err := repo.MarkPoisoned(ctx, ev.ID, at.Add(2*time.Minute)); !errors.Is(err, event.ErrProcessingConflict) {
			t.Fatalf("poison after delivery must conflict, got %v", err)
		}
		if err := repo.SetFinality(ctx, ev.ID, event.FinalityFinal, at.Add(2*time.Minute)); !errors.Is(err, event.ErrProcessingConflict) {
			t.Fatalf("finality after delivery must conflict, got %v", err)
		}
	})

	t.Run("failure counts and defers", func(t *testing.T) {
		ev := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb2", 20, 0, at)
		mustCreate(t, repo, ev)
		next := at.Add(5 * time.Minute)
		if err := repo.RecordFailure(ctx, ev.ID, next, at.Add(time.Minute)); err != nil {
			t.Fatalf("record failure: %v", err)
		}
		got, err := repo.Get(ctx, ev.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Processing != event.ProcessingQueued || got.Attempts != 1 ||
			!got.NextAttemptAt.Equal(next) {
			t.Fatalf("failure not recorded: %+v", got)
		}
		// The deferred event is skipped by scans until it is due.
		due, err := repo.FindPending(ctx, event.PendingQuery{NotBefore: at.Add(time.Minute), MinFinality: event.FinalityConfirmed, Limit: 10})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		if len(due) != 0 {
			t.Fatalf("a deferred event must not be due: %+v", due)
		}
	})

	t.Run("poisoned is terminal and visible", func(t *testing.T) {
		ev := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb3", 30, 0, at)
		mustCreate(t, repo, ev)
		if err := repo.MarkPoisoned(ctx, ev.ID, at.Add(time.Minute)); err != nil {
			t.Fatalf("poison: %v", err)
		}
		got, err := repo.Get(ctx, ev.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Processing != event.ProcessingPoisoned {
			t.Fatalf("want poisoned, got %s", got.Processing)
		}
		due, err := repo.FindPending(ctx, event.PendingQuery{NotBefore: at.Add(time.Hour), MinFinality: event.FinalityConfirmed, Limit: 10})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		for _, e := range due {
			if e.ID == ev.ID {
				t.Fatal("a poisoned event must never be delivered")
			}
		}
	})

	t.Run("set finality unlocks delivery", func(t *testing.T) {
		ev := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xb4", 40, 0, at)
		ev.Finality = event.FinalityPending
		mustCreate(t, repo, ev)
		due, err := repo.FindPending(ctx, event.PendingQuery{NotBefore: at, MinFinality: event.FinalityConfirmed, Limit: 10})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		if len(due) != 0 {
			t.Fatalf("a pending event must wait: %+v", due)
		}
		if err := repo.SetFinality(ctx, ev.ID, event.FinalityConfirmed, at.Add(time.Minute)); err != nil {
			t.Fatalf("set finality: %v", err)
		}
		due, err = repo.FindPending(ctx, event.PendingQuery{NotBefore: at.Add(time.Minute), MinFinality: event.FinalityConfirmed, Limit: 10})
		if err != nil {
			t.Fatalf("find pending: %v", err)
		}
		if len(due) != 1 || due[0].ID != ev.ID {
			t.Fatalf("confirmed event must be deliverable: %+v", due)
		}
	})

	t.Run("missing records fail as not found", func(t *testing.T) {
		if err := repo.MarkDelivered(ctx, "event:missing", at); !errors.Is(err, event.ErrEventNotFound) {
			t.Fatalf("want ErrEventNotFound, got %v", err)
		}
		if err := repo.RecordFailure(ctx, "event:missing", at, at); !errors.Is(err, event.ErrEventNotFound) {
			t.Fatalf("want ErrEventNotFound, got %v", err)
		}
		if err := repo.MarkPoisoned(ctx, "event:missing", at); !errors.Is(err, event.ErrEventNotFound) {
			t.Fatalf("want ErrEventNotFound, got %v", err)
		}
		if err := repo.SetFinality(ctx, "event:missing", event.FinalityConfirmed, at); !errors.Is(err, event.ErrEventNotFound) {
			t.Fatalf("want ErrEventNotFound, got %v", err)
		}
	})
}

// runReorg pins §11.6 steps 2 and 3: queued events at or after the
// rollback height are invalidated and never delivered; delivered events
// stay terminal (their compensation is the application's decision).
func runReorg(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	ctx := context.Background()
	repo := newStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	seed := map[uint64]event.Event{}
	for h := uint64(1); h <= 5; h++ {
		ev := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xblock", h*10, 0, at)
		seed[h*10] = ev
		mustCreate(t, repo, ev)
	}
	// Height 40 was already delivered: compensation is app-level.
	if err := repo.MarkDelivered(ctx, seed[40].ID, at.Add(time.Minute)); err != nil {
		t.Fatalf("deliver: %v", err)
	}

	n, err := repo.MarkReorged(ctx, event.CursorKey{Network: evmNet, SourceType: event.SourceEVMLog}, 30)
	if err != nil {
		t.Fatalf("mark reorged: %v", err)
	}
	if n != 2 {
		t.Fatalf("want 2 invalidated events (heights 30 and 50), got %d", n)
	}

	reorged, err := repo.Get(ctx, seed[30].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if reorged.Observation != event.ObservationReorged || reorged.Processing != event.ProcessingReorged {
		t.Fatalf("height 30 event not invalidated: %+v", reorged)
	}
	delivered, err := repo.Get(ctx, seed[40].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if delivered.Processing != event.ProcessingDelivered {
		t.Fatalf("delivered events stay terminal, got %s", delivered.Processing)
	}
	untouched, err := repo.Get(ctx, seed[10].ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if untouched.Processing != event.ProcessingQueued {
		t.Fatalf("events below the rollback height stay queued, got %s", untouched.Processing)
	}

	due, err := repo.FindPending(ctx, event.PendingQuery{NotBefore: at.Add(time.Hour), MinFinality: event.FinalityConfirmed, Limit: 10})
	if err != nil {
		t.Fatalf("find pending: %v", err)
	}
	for _, ev := range due {
		if ev.BlockHeight >= 30 {
			t.Fatalf("a reorged event must never be delivered: %+v", ev)
		}
	}

	t.Run("other sources are untouched", func(t *testing.T) {
		ev := newEvent(solNet, event.SourceSolanaLog, "sig", "slot", 100, 0, at)
		mustCreate(t, repo, ev)
		// A full rollback of the EVM scope sweeps its remaining queued
		// events (heights 10 and 20; 30 and 50 are already reorged, 40
		// is delivered) and never the other network.
		n, err := repo.MarkReorged(ctx, event.CursorKey{Network: evmNet, SourceType: event.SourceEVMLog}, 0)
		if err != nil {
			t.Fatalf("mark reorged: %v", err)
		}
		if n != 2 {
			t.Fatalf("want 2 swept events, got %d", n)
		}
		got, err := repo.Get(ctx, ev.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Processing != event.ProcessingQueued {
			t.Fatalf("a different network must be untouched, got %s", got.Processing)
		}
	})
}

// runCursors pins §11.5: per (network, source) cursors that never move
// backwards or swap chains silently, with rollbacks explicit and audited
// (§11.6).
func runCursors(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	ctx := context.Background()
	repo := newStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	key := event.CursorKey{Network: evmNet, SourceType: event.SourceEVMLog}

	t.Run("first cursor is missing", func(t *testing.T) {
		_, err := repo.GetCursor(ctx, key)
		if !errors.Is(err, event.ErrCursorNotFound) {
			t.Fatalf("want ErrCursorNotFound, got %v", err)
		}
	})

	t.Run("save and get round trip", func(t *testing.T) {
		c := event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 10, BlockHash: "0xa", UpdatedAt: at}
		if err := repo.SaveCursor(ctx, c); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := repo.GetCursor(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.BlockHeight != 10 || got.BlockHash != "0xa" || !got.UpdatedAt.Equal(at) {
			t.Fatalf("round trip mismatch: %+v", got)
		}
	})

	t.Run("same height and hash is allowed", func(t *testing.T) {
		c := event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 10, BlockHash: "0xa", UpdatedAt: at.Add(time.Minute)}
		if err := repo.SaveCursor(ctx, c); err != nil {
			t.Fatalf("save: %v", err)
		}
	})

	t.Run("same height other hash is a regression", func(t *testing.T) {
		c := event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 10, BlockHash: "0xb", UpdatedAt: at.Add(time.Minute)}
		if err := repo.SaveCursor(ctx, c); !errors.Is(err, event.ErrCursorRegression) {
			t.Fatalf("want ErrCursorRegression, got %v", err)
		}
	})

	t.Run("backwards height is a regression", func(t *testing.T) {
		c := event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 5, BlockHash: "0xa", UpdatedAt: at.Add(time.Minute)}
		if err := repo.SaveCursor(ctx, c); !errors.Is(err, event.ErrCursorRegression) {
			t.Fatalf("want ErrCursorRegression, got %v", err)
		}
	})

	t.Run("forward is allowed", func(t *testing.T) {
		c := event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 20, BlockHash: "0xb", UpdatedAt: at.Add(2 * time.Minute)}
		if err := repo.SaveCursor(ctx, c); err != nil {
			t.Fatalf("save: %v", err)
		}
	})

	t.Run("rollback is explicit and audited", func(t *testing.T) {
		c := event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 12, BlockHash: "0xc", UpdatedAt: at.Add(3 * time.Minute)}
		if err := repo.RollbackCursor(ctx, c); err != nil {
			t.Fatalf("rollback: %v", err)
		}
		got, err := repo.GetCursor(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.BlockHeight != 12 || got.RollbackCount != 1 || got.LastRollback.IsZero() {
			t.Fatalf("rollback not recorded: %+v", got)
		}
		if err := repo.RollbackCursor(ctx, event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 11, BlockHash: "0xd", UpdatedAt: at.Add(4 * time.Minute)}); err != nil {
			t.Fatalf("second rollback: %v", err)
		}
		got, err = repo.GetCursor(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.RollbackCount != 2 {
			t.Fatalf("rollbacks must accumulate, got %d", got.RollbackCount)
		}

		// The audit trail is store-owned: a forward advance must not
		// erase it (§11.6 step 6).
		if err := repo.SaveCursor(ctx, event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: 20, BlockHash: "0xe", UpdatedAt: at.Add(5 * time.Minute)}); err != nil {
			t.Fatalf("save after rollback: %v", err)
		}
		got, err = repo.GetCursor(ctx, key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.RollbackCount != 2 || got.LastRollback.IsZero() {
			t.Fatalf("an advance must preserve the audit trail: %+v", got)
		}
	})

	t.Run("cursors are per source", func(t *testing.T) {
		solKey := event.CursorKey{Network: solNet, SourceType: event.SourceSolanaLog}
		if err := repo.SaveCursor(ctx, event.Cursor{Network: solKey.Network, SourceType: solKey.SourceType, BlockHeight: 7, BlockHash: "slot-7", UpdatedAt: at}); err != nil {
			t.Fatalf("save: %v", err)
		}
		got, err := repo.GetCursor(ctx, solKey)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.BlockHeight != 7 {
			t.Fatalf("the solana cursor must be independent, got %+v", got)
		}
	})
}

// runValidation pins that stores reject records a consumer could never
// interpret (§11.2): the model's Validate is part of the store contract.
func runValidation(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	ctx := context.Background()
	repo := newStore(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name   string
		mutate func(*event.Event)
	}{
		{"no type", func(e *event.Event) { e.Type = "" }},
		{"no id", func(e *event.Event) { e.ID = "" }},
		{"bad finality", func(e *event.Event) { e.Finality = "bogus" }},
		{"bad processing", func(e *event.Event) { e.Processing = "bogus" }},
		{"no timestamps", func(e *event.Event) { e.CreatedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := newEvent(evmNet, event.SourceEVMLog, "0xtx", "0xblock", 10, 0, at)
			tc.mutate(&ev)
			err := repo.Create(ctx, ev)
			if err == nil {
				t.Fatal("invalid records must be rejected")
			}
		})
	}

	t.Run("cursors validate their key", func(t *testing.T) {
		err := repo.SaveCursor(ctx, event.Cursor{SourceType: event.SourceEVMLog, BlockHeight: 1, UpdatedAt: at})
		if err == nil {
			t.Fatal("a cursor without a network must be rejected")
		}
	})
}
