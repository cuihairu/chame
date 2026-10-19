package projection

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

var testKey = event.CursorKey{Network: identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "projection", ChainID: "1337"}, SourceType: event.SourceEVMLog}

func fixture(t *testing.T) (*memory.EventStore, []event.Event) {
	t.Helper()
	raw := memory.NewEventStore()
	now := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	items := []event.Event{
		{ID: "ev-1", Network: testKey.Network, SourceType: testKey.SourceType, TransactionID: "tx1", BlockHash: "h1", BlockHeight: 1, Type: "asset.delta", Data: json.RawMessage(`{"asset":"sword","owner":"alice","delta":1}`), At: now, Observation: event.ObservationObserved, Finality: event.FinalityFinal, Processing: event.ProcessingDelivered, CreatedAt: now, UpdatedAt: now},
		{ID: "ev-2", Network: testKey.Network, SourceType: testKey.SourceType, TransactionID: "tx2", BlockHash: "h2", BlockHeight: 2, Type: "asset.delta", Data: json.RawMessage(`{"asset":"sword","owner":"alice","delta":2}`), At: now, Observation: event.ObservationObserved, Finality: event.FinalityFinal, Processing: event.ProcessingDelivered, CreatedAt: now, UpdatedAt: now},
	}
	for _, ev := range items {
		if err := raw.Create(context.Background(), ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.SaveCursor(context.Background(), event.Cursor{Network: testKey.Network, SourceType: testKey.SourceType, BlockHeight: 2, BlockHash: "h2", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return raw, items
}

func TestRebuildMatchesDeletedProjection(t *testing.T) {
	ctx := context.Background()
	raw, _ := fixture(t)
	b := NewFakeA()
	replay := Replayer{Raw: raw, Backend: b}
	if err := replay.Rebuild(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	before, _ := b.Snapshot(ctx)
	if err := b.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	if err := replay.Rebuild(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	after, _ := b.Snapshot(ctx)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("before=%v after=%v", before, after)
	}
	t.Logf("TestRebuildMatchesDeletedProjection PASS before=%v after=%v", before, after)
}

func TestRebuildPreservesRawEvidence(t *testing.T) {
	ctx := context.Background()
	raw, items := fixture(t)
	b := NewFakeA()
	replay := Replayer{Raw: raw, Backend: b}
	if err := replay.Rebuild(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	got, err := raw.ReplayEvents(ctx, testKey, event.Cursor{BlockHeight: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(items) {
		t.Fatalf("raw events=%d want=%d", len(got), len(items))
	}
	for i := range items {
		if got[i].ID != items[i].ID || !reflect.DeepEqual(got[i].Data, items[i].Data) {
			t.Fatalf("raw evidence changed: got=%+v want=%+v", got[i], items[i])
		}
	}
	t.Logf("TestRebuildPreservesRawEvidence PASS raw_events=%d", len(got))
}

func TestIndexerBackendSwapPreservesProjectionSemantics(t *testing.T) {
	ctx := context.Background()
	raw, _ := fixture(t)
	a, b := NewFakeA(), NewFakeB()
	if err := (Replayer{Raw: raw, Backend: a}).Rebuild(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	if err := (Replayer{Raw: raw, Backend: b}).Rebuild(ctx, testKey); err != nil {
		t.Fatal(err)
	}
	sa, _ := a.Snapshot(ctx)
	sb, _ := b.Snapshot(ctx)
	if !reflect.DeepEqual(sa, sb) {
		t.Fatalf("fake A=%v fake B=%v", sa, sb)
	}
	t.Logf("TestIndexerBackendSwapPreservesProjectionSemantics PASS fakeA=%v fakeB=%v", sa, sb)
}
