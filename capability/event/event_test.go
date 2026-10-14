package event

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/chame/core/identifiers"
)

var testNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "conformance", ChainID: "1337"}

func TestSourceAndStateValidity(t *testing.T) {
	if !SourceEVMLog.Valid() || !SourceSolanaLog.Valid() {
		t.Error("known source types must be valid")
	}
	if SourceType("bogus").Valid() {
		t.Error("unknown source type must be invalid")
	}
	if !ObservationObserved.Valid() || !ObservationReorged.Valid() {
		t.Error("known observation states must be valid")
	}
	if ObservationState("bogus").Valid() {
		t.Error("unknown observation state must be invalid")
	}
	for _, f := range []FinalityState{FinalityPending, FinalityConfirmed, FinalityFinal} {
		if !f.Valid() {
			t.Errorf("%s must be valid", f)
		}
	}
	if FinalityState("bogus").Valid() {
		t.Error("unknown finality state must be invalid")
	}
	for _, s := range []ProcessingState{ProcessingQueued, ProcessingDelivered, ProcessingReorged, ProcessingPoisoned} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	if ProcessingState("bogus").Valid() {
		t.Error("unknown processing state must be invalid")
	}
}

func TestFinalityRankOrdersDelivery(t *testing.T) {
	ranks := []int{FinalityPending.Rank(), FinalityConfirmed.Rank(), FinalityFinal.Rank()}
	for i := 1; i < len(ranks); i++ {
		if ranks[i-1] >= ranks[i] {
			t.Fatalf("pending < confirmed < final, got %v", ranks)
		}
	}
	if FinalityState("bogus").Rank() >= 0 {
		t.Fatal("unknown states must rank below every known one")
	}
}

func TestPositionKeySeparatesEveryEvent(t *testing.T) {
	base := PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock", 10, 2, "")
	same := PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock", 10, 2, "")
	if base != same {
		t.Fatal("the same position must build the same identity")
	}
	other := map[string]EventID{
		"network":     PositionKey(identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "other", ChainID: "1"}, SourceEVMLog, "0xtx", "0xblock", 10, 2, ""),
		"source":      PositionKey(testNet, SourceSolanaLog, "0xtx", "0xblock", 10, 2, ""),
		"transaction": PositionKey(testNet, SourceEVMLog, "0xtx2", "0xblock", 10, 2, ""),
		"block hash":  PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock2", 10, 2, ""),
		"height":      PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock", 11, 2, ""),
		"index":       PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock", 10, 3, ""),
		"locator":     PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock", 10, 2, "log-3"),
	}
	// One transaction may emit several events (§11.3): the identity is
	// never the transaction hash alone.
	seen := map[EventID]bool{base: true}
	for name, key := range other {
		if key == base {
			t.Errorf("%s must not collapse into the base identity", name)
		}
		if seen[key] {
			t.Errorf("%s must not collide with a sibling identity", name)
		}
		seen[key] = true
	}
}

func TestEventValidate(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := func() Event {
		return Event{
			ID:            PositionKey(testNet, SourceEVMLog, "0xtx", "0xblock", 10, 2, ""),
			Network:       testNet,
			SourceType:    SourceEVMLog,
			TransactionID: "0xtx",
			BlockHash:     "0xblock",
			BlockHeight:   10,
			Type:          "deposit.detected",
			Data:          json.RawMessage(`{"amount":"1"}`),
			At:            at,
			Observation:   ObservationObserved,
			Finality:      FinalityConfirmed,
			Processing:    ProcessingQueued,
			CreatedAt:     at,
			UpdatedAt:     at,
		}
	}
	cases := []struct {
		name    string
		mutate  func(*Event)
		wantErr bool
	}{
		{"valid", func(*Event) {}, false},
		{"no id", func(e *Event) { e.ID = "" }, true},
		{"bad network", func(e *Event) { e.Network = identifiers.NetworkID{} }, true},
		{"bad source type", func(e *Event) { e.SourceType = "bogus" }, true},
		{"bad observation", func(e *Event) { e.Observation = "bogus" }, true},
		{"bad finality", func(e *Event) { e.Finality = "bogus" }, true},
		{"bad processing", func(e *Event) { e.Processing = "bogus" }, true},
		{"no type", func(e *Event) { e.Type = "" }, true},
		{"no observation instant", func(e *Event) { e.At = time.Time{} }, true},
		{"no created", func(e *Event) { e.CreatedAt = time.Time{} }, true},
		{"no updated", func(e *Event) { e.UpdatedAt = time.Time{} }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := valid()
			tc.mutate(&ev)
			err := ev.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCursorKeyValidate(t *testing.T) {
	cases := []struct {
		name    string
		key     CursorKey
		wantErr bool
	}{
		{"valid", CursorKey{Network: testNet, SourceType: SourceEVMLog}, false},
		{"bad network", CursorKey{SourceType: SourceEVMLog}, true},
		{"bad source", CursorKey{Network: testNet, SourceType: "bogus"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.key.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}

	t.Run("cursor key round trips", func(t *testing.T) {
		c := Cursor{Network: testNet, SourceType: SourceEVMLog, BlockHeight: 7, BlockHash: "0xh"}
		if got := c.Key(); got.Network != testNet || got.SourceType != SourceEVMLog {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestPendingQueryValidate(t *testing.T) {
	cases := []struct {
		name    string
		query   PendingQuery
		wantErr bool
	}{
		{"valid", PendingQuery{MinFinality: FinalityConfirmed, Limit: 10}, false},
		{"bad finality", PendingQuery{MinFinality: "bogus", Limit: 10}, true},
		{"zero limit", PendingQuery{MinFinality: FinalityConfirmed}, true},
		{"negative limit", PendingQuery{MinFinality: FinalityConfirmed, Limit: -1}, true},
		{"bad network", PendingQuery{MinFinality: FinalityConfirmed, Limit: 10,
			Network: identifiers.NetworkID{Name: "broken", Family: "bogus", ChainID: "1"}}, true},
		{"zero network matches all", PendingQuery{MinFinality: FinalityConfirmed, Limit: 10}, false},
		{"bad source type", PendingQuery{MinFinality: FinalityConfirmed, Limit: 10, SourceType: "bogus"}, true},
		{"zero source type matches all", PendingQuery{MinFinality: FinalityConfirmed, Limit: 10}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.query.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestRepositorySentinelsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrEventNotFound, ErrDuplicateEvent, ErrProcessingConflict,
		ErrCursorNotFound, ErrCursorRegression,
	}
	for i, a := range sentinels {
		if a == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for _, b := range sentinels[i+1:] {
			if errors.Is(a, b) {
				t.Errorf("%v and %v must be distinct", a, b)
			}
		}
	}
}
