package solana

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/capability/event"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// sourceAccount is the address the signature source polls in tests.
var sourceAccount = voteAccount

// pollSource wires a source over a fake whose latest slot is slot. The
// window-end block is pinned so the cursor has a verifiable anchor, and
// the account defaults to the shared source address.
func pollSource(t *testing.T, slot uint64, cfg SignatureSourceConfig) (*fakeClient, *SignatureSource) {
	t.Helper()
	if cfg.Account.Value == "" {
		cfg.Account = mustAddress(t, sourceAccount)
	}
	f := newFakeClient(solanaHashFor(0x10))
	f.slot = slot
	f.withBlock(slot, solanaHashFor(0x30))
	src := NewSignatureSource(newTestNetwork(f), cfg)
	return f, src
}

func TestSignatureSourcePollEmitsWindowSignatures(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{})
	// The listing arrives newest first, like the node reports it.
	f.signatures = []*rpcTransactionSignature{
		// Beyond the window end: a confirmed-commitment listing never
		// carries it, and the source defends the window anyway.
		{Signature: sigFor(0x05), Slot: 101, ConfirmationStatus: "confirmed"},
		{Signature: sigFor(0x04), Slot: 100, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x03), Slot: 99, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x02), Slot: 98, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x01), Slot: 97, ConfirmationStatus: "finalized"},
	}

	events, cursor, err := src.Poll(context.Background(), event.Cursor{Network: src.Network(), SourceType: src.Type()})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %d, want 4", len(events))
	}
	// Chain order, oldest first (§11.4 delivery order).
	for i, ev := range events {
		if err := ev.Validate(); err != nil {
			t.Fatalf("event %d invalid: %v", i, err)
		}
		wantSlot := uint64(97 + i)
		wantID := event.PositionKey(src.Network(), event.SourceSolanaLog, testSignature(byte(0x01+i)), "", wantSlot, 0, "signature")
		if ev.ID != wantID {
			t.Errorf("event %d id = %s, want %s", i, ev.ID, wantID)
		}
		if ev.Type != "signature" {
			t.Errorf("type = %s, want signature", ev.Type)
		}
		if ev.BlockHeight != wantSlot {
			t.Errorf("slot = %d, want %d", ev.BlockHeight, wantSlot)
		}
		if ev.TransactionID != testSignature(byte(0x01+i)) {
			t.Errorf("transaction = %s", ev.TransactionID)
		}
		if ev.Finality != event.FinalityFinal || ev.Observation != event.ObservationObserved || ev.Processing != event.ProcessingQueued {
			t.Errorf("states = %s %s %s, want final/observed/queued", ev.Finality, ev.Observation, ev.Processing)
		}
		if ev.At.IsZero() || ev.CreatedAt.IsZero() || ev.UpdatedAt.IsZero() {
			t.Error("observation and timestamps must be set")
		}
		var payload signaturePayload
		if err := json.Unmarshal(ev.Data, &payload); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if payload.Signature != testSignature(byte(0x01+i)) || payload.Slot != wantSlot || payload.Status != "finalized" {
			t.Errorf("payload = %+v", payload)
		}
	}

	windowHash := f.blocksBySlot[100].Blockhash.String()
	if cursor.BlockHeight != 100 || cursor.BlockHash != windowHash {
		t.Errorf("cursor = %d %s, want 100 %s", cursor.BlockHeight, cursor.BlockHash, windowHash)
	}
	if cursor.Key().Network != src.Network() || cursor.Key().SourceType != src.Type() {
		t.Errorf("cursor key = %+v", cursor.Key())
	}
	if len(f.capturedAccounts()) != 1 || f.capturedAccounts()[0].String() != sourceAccount {
		t.Errorf("account = %v, want %s", f.capturedAccounts(), sourceAccount)
	}
	// The window is read at the network's default commitment (finalized,
	// ADR-0006).
	for _, c := range f.capturedCommitments() {
		if c != CommitmentFinalized {
			t.Errorf("commitment = %q, want finalized", c)
		}
	}
}

func TestSignatureSourcePollFiltersByCursorWindow(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{})
	f.signatures = []*rpcTransactionSignature{
		{Signature: sigFor(0x04), Slot: 100, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x03), Slot: 99, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x02), Slot: 98, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x01), Slot: 97, ConfirmationStatus: "finalized"},
	}
	cursor := event.Cursor{Network: src.Network(), SourceType: src.Type(), BlockHeight: 98, BlockHash: f.blocksBySlot[100].Blockhash.String()}

	events, next, err := src.Poll(context.Background(), cursor)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	// Everything at or before the cursor slot is already consumed; the
	// newest-first listing ends at the first such record.
	if len(events) != 2 || events[0].BlockHeight != 99 || events[1].BlockHeight != 100 {
		t.Fatalf("events = %v, want slots 99 and 100", events)
	}
	if next.BlockHeight != 100 || next.BlockHash != f.blocksBySlot[100].Blockhash.String() {
		t.Errorf("cursor = %d %s, want 100 %s", next.BlockHeight, next.BlockHash, f.blocksBySlot[100].Blockhash.String())
	}
}

func TestSignatureSourcePollAdvancesPastIdle(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{})
	cursor := event.Cursor{Network: src.Network(), SourceType: src.Type(), BlockHeight: 100, BlockHash: f.blocksBySlot[100].Blockhash.String()}

	events, next, err := src.Poll(context.Background(), cursor)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want none while the chain idles", len(events))
	}
	if next.BlockHeight != cursor.BlockHeight || next.BlockHash != cursor.BlockHash {
		t.Errorf("cursor moved to %d %q, want unchanged", next.BlockHeight, next.BlockHash)
	}
	if f.callCount("signatures") != 0 {
		t.Error("no signature listing may run while the window has no new slot")
	}
}

func TestSignatureSourcePollRejectsUnverifiableWindow(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.slot = 100
	// No block pinned for slot 100: the node cannot anchor the window.
	src := NewSignatureSource(newTestNetwork(f), SignatureSourceConfig{})

	if _, _, err := src.Poll(context.Background(), event.Cursor{}); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("err = %v, want position unknown", err)
	}
	if f.callCount("signatures") != 0 {
		t.Error("no signature listing may run before the window end is verified")
	}
}

func TestSignatureSourcePollRejectsOverrun(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{Limit: 2})
	f.signatures = []*rpcTransactionSignature{
		{Signature: sigFor(0x04), Slot: 100, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x03), Slot: 99, ConfirmationStatus: "finalized"},
	}
	cursor := event.Cursor{Network: src.Network(), SourceType: src.Type(), BlockHeight: 98}

	if _, _, err := src.Poll(context.Background(), cursor); !errors.Is(err, cherrs.ErrInternalError) {
		t.Errorf("err = %v, want internal error", err)
	}
	// The same history under a larger page: the window is covered and
	// the cursor advances.
	f2, src2 := pollSource(t, 100, SignatureSourceConfig{Limit: 5})
	f2.signatures = []*rpcTransactionSignature{
		{Signature: sigFor(0x04), Slot: 100, ConfirmationStatus: "finalized"},
		{Signature: sigFor(0x03), Slot: 99, ConfirmationStatus: "finalized"},
	}
	events, next, err := src2.Poll(context.Background(), cursor)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	if next.BlockHeight != 100 {
		t.Errorf("cursor = %d, want 100", next.BlockHeight)
	}
}

func TestSignatureSourcePollCarriesCommitment(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{Commitment: CommitmentConfirmed})
	f.signatures = []*rpcTransactionSignature{
		{Signature: sigFor(0x03), Slot: 99, ConfirmationStatus: "confirmed"},
		{Signature: sigFor(0x04), Slot: 100, ConfirmationStatus: "confirmed"},
	}

	events, _, err := src.Poll(context.Background(), event.Cursor{})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	// A confirmed-commitment listing yields confirmed events, which
	// Observe can still promote.
	if events[0].Finality != event.FinalityConfirmed {
		t.Errorf("finality = %s, want confirmed", events[0].Finality)
	}
	for _, c := range f.capturedCommitments() {
		if c != CommitmentConfirmed {
			t.Errorf("commitment = %q, want confirmed", c)
		}
	}
}

func TestSignatureSourceHeadReturnsCanonicalHash(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{})

	got, err := src.Head(context.Background(), 100)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if want := f.blocksBySlot[100].Blockhash.String(); got != want {
		t.Errorf("Head(100) = %s, want %s", got, want)
	}
	if _, err := src.Head(context.Background(), 0); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("Head(0) = %v, want position unknown", err)
	}
	if _, err := src.Head(context.Background(), 99); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("Head(99) = %v, want position unknown for an unpinned slot", err)
	}
	f.fail("block", netErr{msg: "connection refused"})
	if _, err := src.Head(context.Background(), 100); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("Head on a failing endpoint = %v, want position unknown", err)
	}
}

func TestSignatureSourceObserveLadder(t *testing.T) {
	f, src := pollSource(t, 100, SignatureSourceConfig{})
	ev := event.Event{ID: "e", BlockHeight: 100, TransactionID: testSignature(0x04)}

	steps := []struct {
		status  string
		want    event.FinalityState
		wantErr bool
	}{
		{"finalized", event.FinalityFinal, false},
		{"confirmed", event.FinalityConfirmed, false},
		{"processed", event.FinalityPending, false},
		{"", event.FinalityPending, false},
	}
	for _, step := range steps {
		f.signatureStat = []*rpcSignatureStatusesResult{{ConfirmationStatus: rpc.ConfirmationStatusType(step.status)}}
		finality, err := src.Observe(context.Background(), ev)
		if step.wantErr {
			if !errors.Is(err, event.ErrPositionUnknown) {
				t.Errorf("status %q: err = %v, want position unknown", step.status, err)
			}
			continue
		}
		if err != nil || finality != step.want {
			t.Errorf("status %q: finality = %s err = %v, want %s", step.status, finality, err, step.want)
		}
	}

	// The node holds no status: never observed, or dropped when its slot
	// left the canonical chain — the reorg path's answer.
	f.signatureStat = nil
	if _, err := src.Observe(context.Background(), ev); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("missing status: err = %v, want position unknown", err)
	}
	f.fail("signature_status", netErr{msg: "connection refused"})
	if _, err := src.Observe(context.Background(), ev); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("Observe on a failing endpoint = %v, want position unknown", err)
	}
}

func TestSignatureSourceDefaults(t *testing.T) {
	_, src := pollSource(t, 100, SignatureSourceConfig{})
	if src.commitment != CommitmentFinalized {
		t.Errorf("commitment = %q, want the network default finalized", src.commitment)
	}
	if src.limit != 1000 {
		t.Errorf("limit = %d, want 1000", src.limit)
	}
}

func TestNetworkBlock(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	hash := solanaHashFor(0x40)
	bt := solana.UnixTimeSeconds(1700000000)
	f.blocksBySlot = map[uint64]*blockResult{
		55: {Blockhash: hash, BlockTime: &bt},
	}
	n := newTestNetwork(f)

	info, err := n.Block(context.Background(), 55, CommitmentConfirmed)
	if err != nil {
		t.Fatalf("Block: %v", err)
	}
	if info.Slot != 55 || info.Blockhash != hash.String() || info.TimeSec != 1700000000 {
		t.Errorf("summary = %+v, want slot 55 %s time 1700000000", info, hash)
	}
	if got := f.capturedCommitments(); len(got) != 1 || got[0] != CommitmentConfirmed {
		t.Errorf("commitments = %v, want [confirmed]", got)
	}

	// A skipped slot: no block, no error — the position is unverifiable.
	info, err = n.Block(context.Background(), 56, CommitmentConfirmed)
	if err != nil || info != nil {
		t.Errorf("Block(56) = %+v, %v, want nil, nil", info, err)
	}

	f.fail("block", netErr{msg: "connection refused"})
	if _, err := n.Block(context.Background(), 55, CommitmentConfirmed); err == nil {
		t.Error("Block on a failing endpoint: err = nil, want error")
	}
}

func TestFinalityOfStatus(t *testing.T) {
	steps := []struct {
		status string
		want   event.FinalityState
	}{
		{"finalized", event.FinalityFinal},
		{"confirmed", event.FinalityConfirmed},
		{"processed", event.FinalityPending},
		{"", event.FinalityPending},
	}
	for _, step := range steps {
		if got := finalityOfStatus(step.status); got != step.want {
			t.Errorf("finalityOfStatus(%q) = %s, want %s", step.status, got, step.want)
		}
	}
}
