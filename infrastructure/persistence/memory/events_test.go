package memory

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/identifiers"
)

var (
	solNet = identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet", ChainID: "genesis"}
	evmKey = event.CursorKey{Network: evmNet, SourceType: event.SourceEVMLog}
)

func validEvent(id string, height, index uint64) event.Event {
	return event.Event{
		ID:            event.EventID(id),
		Network:       evmNet,
		SourceType:    event.SourceEVMLog,
		TransactionID: "0xtx" + id,
		BlockHash:     "0xblock",
		BlockHeight:   height,
		Index:         index,
		Type:          "Transfer",
		Data:          json.RawMessage(`{}`),
		At:            t0,
		CreatedAt:     t0,
		UpdatedAt:     t0,
		Observation:   event.ObservationObserved,
		Finality:      event.FinalityPending,
		Processing:    event.ProcessingQueued,
	}
}

func mustCreateEvent(t *testing.T, s *EventStore, ev event.Event) {
	t.Helper()
	if err := s.Create(context.Background(), ev); err != nil {
		t.Fatalf("Create(%s): %v", ev.ID, err)
	}
}

// TestEventStoreCreateValidation checks that Create applies the shared
// Event.Validate shape rules, so memory and SQL stores reject the same
// malformed records.
func TestEventStoreCreateValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*event.Event)
	}{
		{"empty id", func(e *event.Event) { e.ID = "" }},
		{"bad network", func(e *event.Event) { e.Network = identifiers.NetworkID{Family: "cosmos", Name: "x", ChainID: "1"} }},
		{"unknown source type", func(e *event.Event) { e.SourceType = "rpc-push" }},
		{"unknown observation", func(e *event.Event) { e.Observation = "maybe" }},
		{"unknown finality", func(e *event.Event) { e.Finality = "settled" }},
		{"unknown processing", func(e *event.Event) { e.Processing = "sent" }},
		{"empty type", func(e *event.Event) { e.Type = "" }},
		{"zero timestamps", func(e *event.Event) { e.At = time.Time{} }},
	}
	for _, tc := range cases {
		ev := validEvent("ev-1", 1, 0)
		tc.mut(&ev)
		if err := NewEventStore().Create(context.Background(), ev); err == nil {
			t.Errorf("%s: accepted invalid event", tc.name)
		}
	}
}

// TestEventStoreCreateAndGet covers identity uniqueness (§11.3): the same
// identity is a duplicate whatever its content, and a re-poll never
// stores an event twice.
func TestEventStoreCreateAndGet(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	if _, err := s.Get(ctx, "ev-1"); !errors.Is(err, event.ErrEventNotFound) {
		t.Fatalf("Get missing: err=%v, want ErrEventNotFound", err)
	}

	mustCreateEvent(t, s, validEvent("ev-1", 5, 0))
	got, err := s.Get(ctx, "ev-1")
	if err != nil || got.ID != "ev-1" {
		t.Fatalf("Get=%+v err=%v", got, err)
	}

	dup := validEvent("ev-1", 5, 0)
	dup.Type = "Other"
	if err := s.Create(ctx, dup); !errors.Is(err, event.ErrDuplicateEvent) {
		t.Fatalf("duplicate: err=%v, want ErrDuplicateEvent", err)
	}
}

// TestEventStoreReplay pins §11 replay semantics: raw evidence for one
// (network, source) through the caller's saved cursor, in chain order,
// regardless of delivery state.
func TestEventStoreReplay(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	if _, err := s.ReplayEvents(ctx, event.CursorKey{}, event.Cursor{}); err == nil {
		t.Fatal("invalid cursor key accepted")
	}

	mustCreateEvent(t, s, validEvent("ev-c2", 3, 2))
	mustCreateEvent(t, s, validEvent("ev-c1", 3, 1))
	mustCreateEvent(t, s, validEvent("ev-a", 5, 0))
	sol := validEvent("ev-sol", 7, 0)
	sol.Network = solNet
	sol.SourceType = event.SourceSolanaLog
	mustCreateEvent(t, s, sol)
	above := validEvent("ev-above", 9, 0)
	mustCreateEvent(t, s, above)
	delivered := validEvent("ev-delivered", 3, 3)
	delivered.Processing = event.ProcessingDelivered
	mustCreateEvent(t, s, delivered)

	through := event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 5}
	out, err := s.ReplayEvents(ctx, evmKey, through)
	if err != nil {
		t.Fatal(err)
	}
	ids := []event.EventID{}
	for _, ev := range out {
		ids = append(ids, ev.ID)
	}
	want := []event.EventID{"ev-c1", "ev-c2", "ev-delivered", "ev-a"}
	if len(ids) != len(want) {
		t.Fatalf("replay=%v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("replay=%v, want %v (chain order, delivery state ignored)", ids, want)
		}
	}
}

// TestEventStoreFindPendingQueryValidation covers the query-shape errors.
func TestEventStoreFindPendingQueryValidation(t *testing.T) {
	base := event.PendingQuery{NotBefore: t1, MinFinality: event.FinalityPending, Limit: 10}
	cases := []struct {
		name string
		mut  func(*event.PendingQuery)
	}{
		{"unknown finality", func(q *event.PendingQuery) { q.MinFinality = "settled" }},
		{"zero limit", func(q *event.PendingQuery) { q.Limit = 0 }},
		{"negative limit", func(q *event.PendingQuery) { q.Limit = -1 }},
		{"invalid network", func(q *event.PendingQuery) {
			q.Network = identifiers.NetworkID{Family: "cosmos", Name: "x", ChainID: "1"}
		}},
		{"invalid source type", func(q *event.PendingQuery) { q.SourceType = "rpc-push" }},
	}
	for _, tc := range cases {
		q := base
		tc.mut(&q)
		if _, err := NewEventStore().FindPending(context.Background(), q); err == nil {
			t.Errorf("%s: accepted invalid query", tc.name)
		}
	}
}

// TestEventStoreFindPending pins the readiness filter (§11.4): queued and
// observed only, finality at or above the requirement, attempt due,
// scoped by network and source, in chain order, bounded by Limit.
func TestEventStoreFindPending(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	ready := validEvent("ev-b", 4, 0)
	ready.Finality = event.FinalityConfirmed
	mustCreateEvent(t, s, ready)

	// Below the required finality.
	low := validEvent("ev-low", 5, 0)
	low.Finality = event.FinalityPending
	mustCreateEvent(t, s, low)
	confirmed := validEvent("ev-conf", 1, 0)
	confirmed.Finality = event.FinalityConfirmed
	mustCreateEvent(t, s, confirmed)

	// Not queued.
	gone := validEvent("ev-gone", 6, 0)
	gone.Processing = event.ProcessingDelivered
	mustCreateEvent(t, s, gone)

	// Not observed anymore.
	reorged := validEvent("ev-reorg", 6, 1)
	reorged.Observation = event.ObservationReorged
	mustCreateEvent(t, s, reorged)

	// Attempt deferred past the bound.
	deferred := validEvent("ev-deferred", 7, 0)
	deferred.Finality = event.FinalityConfirmed
	deferred.NextAttemptAt = t2
	mustCreateEvent(t, s, deferred)

	// Other network and source.
	sol := validEvent("ev-sol", 0, 0)
	sol.Network = solNet
	sol.SourceType = event.SourceSolanaLog
	mustCreateEvent(t, s, sol)

	q := event.PendingQuery{NotBefore: t1, MinFinality: event.FinalityConfirmed, Limit: 10}
	out, err := s.FindPending(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %+v, want [ev-conf ev-b] (chain order)", out)
	}
	if out[0].ID != "ev-conf" || out[1].ID != "ev-b" {
		t.Fatalf("order wrong: [%s %s], want [ev-conf ev-b]", out[0].ID, out[1].ID)
	}

	q.Network = evmNet
	q.SourceType = event.SourceEVMLog
	q.Limit = 1
	out, err = s.FindPending(ctx, q)
	if err != nil || len(out) != 1 || out[0].ID != "ev-conf" {
		t.Fatalf("scoped limited query: out=%+v err=%v", out, err)
	}

	// Past the deferral instant the event becomes ready again.
	q.Limit = 10
	q.NotBefore = t3
	out, err = s.FindPending(ctx, q)
	if err != nil || len(out) != 3 {
		t.Fatalf("after deferral: out=%+v err=%v", out, err)
	}
}

// TestEventStoreDeliveryCAS pins the §11.4 compare-and-swap: queued-only
// updates, the not-found path, and the per-mutator effects.
func TestEventStoreDeliveryCAS(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	if err := s.MarkDelivered(ctx, "ev-1", t1); !errors.Is(err, event.ErrEventNotFound) {
		t.Fatalf("mutate missing: err=%v", err)
	}
	mustCreateEvent(t, s, validEvent("ev-1", 1, 0))

	if err := s.RecordFailure(ctx, "ev-1", t2, t1); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	got, _ := s.Get(ctx, "ev-1")
	if got.Attempts != 1 || !got.NextAttemptAt.Equal(t2) || got.Processing != event.ProcessingQueued || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("record after failure: %+v", got)
	}

	if err := s.SetFinality(ctx, "ev-1", "settled", t1); err == nil {
		t.Fatal("unknown finality accepted")
	}
	if err := s.SetFinality(ctx, "ev-1", event.FinalityFinal, t1); err != nil {
		t.Fatalf("SetFinality: %v", err)
	}

	if err := s.MarkDelivered(ctx, "ev-1", t2); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	for name, mutate := range map[string]func() error{
		"MarkDelivered twice": func() error { return s.MarkDelivered(ctx, "ev-1", t3) },
		"RecordFailure":       func() error { return s.RecordFailure(ctx, "ev-1", t3, t3) },
		"MarkPoisoned":        func() error { return s.MarkPoisoned(ctx, "ev-1", t3) },
		"SetFinality":         func() error { return s.SetFinality(ctx, "ev-1", event.FinalityFinal, t3) },
	} {
		if err := mutate(); !errors.Is(err, event.ErrProcessingConflict) {
			t.Errorf("%s on delivered: err=%v, want ErrProcessingConflict", name, err)
		}
	}

	// Poisoning is terminal too.
	p := NewEventStore()
	mustCreateEvent(t, p, validEvent("ev-2", 1, 0))
	if err := p.MarkPoisoned(ctx, "ev-2", t1); err != nil {
		t.Fatalf("MarkPoisoned: %v", err)
	}
	got, _ = p.Get(ctx, "ev-2")
	if got.Processing != event.ProcessingPoisoned || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("poisoned record: %+v", got)
	}
	if err := p.MarkDelivered(ctx, "ev-2", t2); !errors.Is(err, event.ErrProcessingConflict) {
		t.Fatalf("deliver poisoned: err=%v", err)
	}
}

// TestEventStoreMarkReorged pins §11.6 steps 2 and 3: queued events at or
// past the reorg height on that (network, source) are invalidated;
// delivered events and other sources are untouched.
func TestEventStoreMarkReorged(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	if _, err := s.MarkReorged(ctx, event.CursorKey{}, 1); err == nil {
		t.Fatal("invalid cursor key accepted")
	}

	at10 := validEvent("ev-at10", 10, 0)
	mustCreateEvent(t, s, at10)
	below := validEvent("ev-below", 9, 0)
	mustCreateEvent(t, s, below)
	delivered := validEvent("ev-delivered", 12, 0)
	delivered.Processing = event.ProcessingDelivered
	mustCreateEvent(t, s, delivered)
	sol := validEvent("ev-sol", 12, 0)
	sol.Network = solNet
	sol.SourceType = event.SourceSolanaLog
	mustCreateEvent(t, s, sol)

	n, err := s.MarkReorged(ctx, evmKey, 10)
	if err != nil || n != 1 {
		t.Fatalf("MarkReorged n=%d err=%v, want 1", n, err)
	}

	got, _ := s.Get(ctx, "ev-at10")
	if got.Observation != event.ObservationReorged || got.Processing != event.ProcessingReorged {
		t.Fatalf("reorged record: %+v", got)
	}
	if got, _ = s.Get(ctx, "ev-below"); got.Processing != event.ProcessingQueued {
		t.Fatalf("event below the reorg height touched: %+v", got)
	}
	if got, _ = s.Get(ctx, "ev-delivered"); got.Processing != event.ProcessingDelivered {
		t.Fatalf("delivered event invalidated: %+v", got)
	}
	if got, _ = s.Get(ctx, "ev-sol"); got.Processing != event.ProcessingQueued {
		t.Fatalf("other network touched: %+v", got)
	}
}

// TestEventStoreCursors pins §11.5 and §11.6: monotonic advances with
// hash consistency, the explicit rollback audit trail, and store
// ownership of the audit state.
func TestEventStoreCursors(t *testing.T) {
	ctx := context.Background()
	s := NewEventStore()

	if _, err := s.GetCursor(ctx, event.CursorKey{}); err == nil {
		t.Fatal("invalid cursor key accepted")
	}
	if err := s.SaveCursor(ctx, event.Cursor{}); err == nil {
		t.Fatal("invalid cursor key accepted")
	}
	if err := s.RollbackCursor(ctx, event.Cursor{}); err == nil {
		t.Fatal("invalid cursor key accepted")
	}
	if _, err := s.GetCursor(ctx, evmKey); !errors.Is(err, event.ErrCursorNotFound) {
		t.Fatalf("fresh store: err=%v, want ErrCursorNotFound", err)
	}

	first := event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 5, BlockHash: "0x5", UpdatedAt: t0}
	if err := s.SaveCursor(ctx, first); err != nil {
		t.Fatalf("first save: %v", err)
	}
	got, err := s.GetCursor(ctx, evmKey)
	if err != nil || got.BlockHeight != 5 || got.BlockHash != "0x5" {
		t.Fatalf("GetCursor=%+v err=%v", got, err)
	}

	// Forward advance, including a re-save of the same position.
	if err := s.SaveCursor(ctx, event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 6, BlockHash: "0x6", UpdatedAt: t1}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if err := s.SaveCursor(ctx, event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 6, BlockHash: "0x6", UpdatedAt: t1}); err != nil {
		t.Fatalf("same-position re-save: %v", err)
	}

	// A backwards height is a regression: the chain moved underneath.
	back := event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 5, BlockHash: "0x5", UpdatedAt: t2}
	if err := s.SaveCursor(ctx, back); !errors.Is(err, event.ErrCursorRegression) {
		t.Fatalf("backwards: err=%v, want ErrCursorRegression", err)
	}
	// So is a different block at the same height.
	fork := event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 6, BlockHash: "0x6-alt", UpdatedAt: t2}
	if err := s.SaveCursor(ctx, fork); !errors.Is(err, event.ErrCursorRegression) {
		t.Fatalf("same height other hash: err=%v, want ErrCursorRegression", err)
	}
	// An empty hash carries no position claim and is not a regression.
	if err := s.SaveCursor(ctx, event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 6, UpdatedAt: t2}); err != nil {
		t.Fatalf("hash-less re-save rejected: %v", err)
	}

	// Rollback: explicit, audited, and preserved across later advances.
	rb := event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 5, BlockHash: "0x5", UpdatedAt: t2}
	if err := s.RollbackCursor(ctx, rb); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	got, err = s.GetCursor(ctx, evmKey)
	if err != nil || got.BlockHeight != 5 || got.RollbackCount != 1 || !got.LastRollback.Equal(t2) {
		t.Fatalf("after rollback: %+v err=%v", got, err)
	}
	if err := s.SaveCursor(ctx, event.Cursor{Network: evmNet, SourceType: event.SourceEVMLog, BlockHeight: 7, BlockHash: "0x7", UpdatedAt: t3}); err != nil {
		t.Fatalf("advance after rollback: %v", err)
	}
	got, _ = s.GetCursor(ctx, evmKey)
	if got.RollbackCount != 1 || !got.LastRollback.Equal(t2) {
		t.Fatalf("audit trail erased by advance: %+v", got)
	}

	// A caller-supplied LastRollback is kept, not overwritten.
	rb.LastRollback = t1
	rb.BlockHeight = 4
	if err := s.RollbackCursor(ctx, rb); err != nil {
		t.Fatalf("second rollback: %v", err)
	}
	got, _ = s.GetCursor(ctx, evmKey)
	if got.RollbackCount != 2 || !got.LastRollback.Equal(t1) {
		t.Fatalf("after second rollback: %+v", got)
	}
}
