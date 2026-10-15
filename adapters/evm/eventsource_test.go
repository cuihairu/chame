package evm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/event"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// eventTxHash is a deterministic transaction hash for source tests.
var eventTxHash = common.HexToHash("0x" + strings.Repeat("cd", 32))

// pollSource wires a source over a fake whose head is head and whose
// confirmation window ends at windowHeight; it returns the fake, the
// source, and the window block's hash.
func pollSource(t *testing.T, head, windowHeight uint64, depth uint64) (*fakeClient, *LogSource, string) {
	t.Helper()
	fake := newFakeClient(1)
	fake.blockNumber = head
	windowHash := fake.withBlock(windowHeight, common.HexToHash("0x"+strings.Repeat("11", 32)))
	src := NewLogSource(testNetwork(t, fake), LogSourceConfig{Depth: depth})
	return fake, src, windowHash
}

func TestLogSourcePollEmitsCommittedLogs(t *testing.T) {
	fake, src, windowHash := pollSource(t, 10, 9, 1)
	sig := common.HexToHash("0x" + strings.Repeat("aa", 32))
	fake.logs = []types.Log{{
		BlockNumber: 9,
		BlockHash:   common.HexToHash(windowHash),
		TxHash:      eventTxHash,
		Index:       2,
		Address:     common.HexToAddress(weiHolder),
		Topics:      []common.Hash{sig},
		Data:        []byte{1, 2},
	}}

	events, cursor, err := src.Poll(context.Background(), event.Cursor{Network: src.Network(), SourceType: src.Type()})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	ev := events[0]
	if err := ev.Validate(); err != nil {
		t.Fatalf("event invalid: %v", err)
	}
	wantID := event.PositionKey(src.Network(), event.SourceEVMLog, eventTxHash.Hex(), windowHash, 9, 2, "")
	if ev.ID != wantID {
		t.Errorf("event id = %s, want %s", ev.ID, wantID)
	}
	if ev.Type != sig.Hex() {
		t.Errorf("type = %s, want the signature topic %s", ev.Type, sig.Hex())
	}
	if ev.BlockHeight != 9 || ev.BlockHash != windowHash || ev.Index != 2 {
		t.Errorf("position = %d %s %d, want 9 %s 2", ev.BlockHeight, ev.BlockHash, ev.Index, windowHash)
	}
	if ev.TransactionID != eventTxHash.Hex() {
		t.Errorf("transaction = %s, want %s", ev.TransactionID, eventTxHash.Hex())
	}
	if ev.Finality != event.FinalityPending || ev.Observation != event.ObservationObserved || ev.Processing != event.ProcessingQueued {
		t.Errorf("states = %s %s %s, want pending/observed/queued", ev.Finality, ev.Observation, ev.Processing)
	}
	if ev.At.IsZero() {
		t.Error("observation time must be set")
	}
	var payload logPayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Address != common.HexToAddress(weiHolder).Hex() {
		t.Errorf("payload address = %s", payload.Address)
	}
	if len(payload.Topics) != 1 || payload.Topics[0] != sig.Hex() {
		t.Errorf("payload topics = %v", payload.Topics)
	}
	if payload.Data != "0102" {
		t.Errorf("payload data = %s, want 0102", payload.Data)
	}

	// The cursor lands on the window with its canonical hash (§11.5).
	if cursor.BlockHeight != 9 || cursor.BlockHash != windowHash {
		t.Errorf("cursor = %d %s, want 9 %s", cursor.BlockHeight, cursor.BlockHash, windowHash)
	}
	if cursor.Key().Network != src.Network() || cursor.Key().SourceType != src.Type() {
		t.Errorf("cursor key = %+v", cursor.Key())
	}
	if cursor.UpdatedAt.IsZero() {
		t.Error("cursor timestamp must be set")
	}

	q := fake.capturedQueries()
	if len(q) != 1 || q[0].FromBlock.Uint64() != 1 || q[0].ToBlock.Uint64() != 9 {
		t.Errorf("log query = %+v, want range 1..9", q)
	}
}

func TestLogSourcePollCarriesFilter(t *testing.T) {
	fake, src, _ := pollSource(t, 10, 9, 1)
	emitter := evmAddr(t, weiHolder)
	sig := common.HexToHash("0x" + strings.Repeat("bb", 32))
	src.cfg.Filter = LogFilter{
		Addresses: []identifiers.Address{emitter},
		Topics:    [][]string{{sig.Hex()}, nil},
	}
	if _, _, err := src.Poll(context.Background(), event.Cursor{}); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	q := fake.capturedQueries()
	if len(q) != 1 {
		t.Fatalf("queries = %d, want 1", len(q))
	}
	if len(q[0].Addresses) != 1 || q[0].Addresses[0].Hex() != common.HexToAddress(weiHolder).Hex() {
		t.Errorf("query addresses = %v", q[0].Addresses)
	}
	if len(q[0].Topics) != 2 || len(q[0].Topics[0]) != 1 || q[0].Topics[0][0].Hex() != sig.Hex() {
		t.Errorf("query topics = %v", q[0].Topics)
	}
}

func TestLogSourcePollWaitsForConfirmationWindow(t *testing.T) {
	fake, src, _ := pollSource(t, 3, 0, 5)
	cur := event.Cursor{Network: src.Network(), SourceType: src.Type()}
	events, got, err := src.Poll(context.Background(), cur)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want none before the window", len(events))
	}
	if got.BlockHeight != cur.BlockHeight || got.BlockHash != cur.BlockHash {
		t.Errorf("cursor moved to %d %q, want unchanged", got.BlockHeight, got.BlockHash)
	}
	if len(fake.capturedQueries()) != 0 {
		t.Error("no log query may run before the window is reached")
	}
}

func TestLogSourcePollAdvancesPastEmptyWindow(t *testing.T) {
	_, src, windowHash := pollSource(t, 10, 9, 1)
	events, cursor, err := src.Poll(context.Background(), event.Cursor{Network: src.Network(), SourceType: src.Type()})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want none", len(events))
	}
	if cursor.BlockHeight != 9 || cursor.BlockHash != windowHash {
		t.Errorf("cursor = %d %s, want 9 %s", cursor.BlockHeight, cursor.BlockHash, windowHash)
	}
}

func TestLogSourcePollKeepsCursorWhenNothingNew(t *testing.T) {
	_, src, windowHash := pollSource(t, 10, 9, 1)
	cur := event.Cursor{Network: src.Network(), SourceType: src.Type(), BlockHeight: 9, BlockHash: windowHash}
	events, got, err := src.Poll(context.Background(), cur)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("events = %d, want none", len(events))
	}
	if got.BlockHeight != 9 || got.BlockHash != windowHash {
		t.Errorf("cursor = %d %s, want unchanged at 9 %s", got.BlockHeight, got.BlockHash, windowHash)
	}
}

func TestLogSourcePollRejectsLogWithoutIdentity(t *testing.T) {
	fake, src, _ := pollSource(t, 10, 9, 1)
	fake.logs = []types.Log{{
		BlockNumber: 9,
		TxHash:      eventTxHash,
		Index:       0,
	}}
	if _, _, err := src.Poll(context.Background(), event.Cursor{}); !errors.Is(err, cherrs.ErrEventDecodeFailure) {
		t.Errorf("zero block hash: err = %v, want event decode failure", err)
	}

	fake2, src2, _ := pollSource(t, 10, 9, 1)
	fake2.logs = []types.Log{{
		BlockNumber: 9,
		BlockHash:   common.HexToHash("0x" + strings.Repeat("22", 32)),
		Index:       0,
	}}
	if _, _, err := src2.Poll(context.Background(), event.Cursor{}); !errors.Is(err, cherrs.ErrEventDecodeFailure) {
		t.Errorf("zero transaction hash: err = %v, want event decode failure", err)
	}
}

func TestLogSourceHeadReturnsCanonicalHash(t *testing.T) {
	fake, src, windowHash := pollSource(t, 10, 9, 1)
	got, err := src.Head(context.Background(), 9)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	if got != windowHash {
		t.Errorf("Head(9) = %s, want %s", got, windowHash)
	}
	if _, err := src.Head(context.Background(), 0); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("Head(0) = %v, want position unknown", err)
	}
	fake.fail("header_by_number", netErr{msg: "connection refused"})
	if _, err := src.Head(context.Background(), 9); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("Head on a failing endpoint = %v, want position unknown", err)
	}
}

func TestLogSourceObserveLadder(t *testing.T) {
	fake, src, blockHash := pollSource(t, 20, 5, 1)
	// The default finality depth is 12 (ADR-0006 mainnet default).
	ev := event.Event{ID: "e", BlockHeight: 5, BlockHash: blockHash}

	finality, err := src.Observe(context.Background(), ev)
	if err != nil || finality != event.FinalityFinal {
		t.Errorf("depth 15: finality = %s err = %v, want final", finality, err)
	}

	fake.blockNumber = 15
	finality, err = src.Observe(context.Background(), ev)
	if err != nil || finality != event.FinalityConfirmed {
		t.Errorf("depth 10: finality = %s err = %v, want confirmed", finality, err)
	}

	fake.blockNumber = 5
	finality, err = src.Observe(context.Background(), ev)
	if err != nil || finality != event.FinalityPending {
		t.Errorf("depth 0: finality = %s err = %v, want pending", finality, err)
	}

	fake.blockNumber = 4
	finality, err = src.Observe(context.Background(), ev)
	if err != nil || finality != event.FinalityPending {
		t.Errorf("head below the block: finality = %s err = %v, want pending", finality, err)
	}
}

func TestLogSourceObserveUnverifiablePosition(t *testing.T) {
	fake, src, blockHash := pollSource(t, 20, 5, 1)
	ev := event.Event{ID: "e", BlockHeight: 5, BlockHash: blockHash}

	// A block that no longer matches is the reorg path's answer (§11.6):
	// the event stays unverifiable, never promoted on a foreign block.
	fake.withBlock(5, common.HexToHash("0x"+strings.Repeat("33", 32)))
	if _, err := src.Observe(context.Background(), ev); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("moved block: err = %v, want position unknown", err)
	}

	fake2, src2, blockHash2 := pollSource(t, 20, 5, 1)
	ev2 := event.Event{ID: "e", BlockHeight: 5, BlockHash: blockHash2}
	fake2.fail("header_by_number", netErr{msg: "connection refused"})
	if _, err := src2.Observe(context.Background(), ev2); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("failing header: err = %v, want position unknown", err)
	}

	fake3, src3, blockHash3 := pollSource(t, 20, 5, 1)
	ev3 := event.Event{ID: "e", BlockHeight: 5, BlockHash: blockHash3}
	fake3.fail("block_number", netErr{msg: "connection refused"})
	if _, err := src3.Observe(context.Background(), ev3); !errors.Is(err, event.ErrPositionUnknown) {
		t.Errorf("failing head: err = %v, want position unknown", err)
	}
}

func TestLogSourceAnonymousLogType(t *testing.T) {
	if got := normalizedLogType(nil); got != anonymousLogType {
		t.Errorf("anonymous type = %q, want %q", got, anonymousLogType)
	}
	sig := common.HexToHash("0x" + strings.Repeat("aa", 32))
	if got := normalizedLogType([]string{sig.Hex(), "0x02"}); got != sig.Hex() {
		t.Errorf("type = %q, want the first topic", got)
	}
}

func TestLogSourceDefaults(t *testing.T) {
	cfg := LogSourceConfig{}.withDefaults()
	if cfg.Depth != 1 {
		t.Errorf("default depth = %d, want 1", cfg.Depth)
	}
	if cfg.FinalityDepth != 12 {
		t.Errorf("default finality depth = %d, want 12", cfg.FinalityDepth)
	}
	if _, src, _ := pollSource(t, 30, 18, 0); src.cfg.Depth != 1 || src.cfg.FinalityDepth != 12 {
		t.Errorf("NewLogSource defaults = %+v", src.cfg)
	}
}

func TestNetworkLogsCarryBlockHash(t *testing.T) {
	fake := newFakeClient(1)
	emitter := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	blockHash := common.HexToHash("0x" + strings.Repeat("44", 32))
	fake.logs = []types.Log{{
		BlockNumber: 5,
		BlockHash:   blockHash,
		TxHash:      eventTxHash,
		Index:       1,
		Address:     emitter,
	}}
	n := testNetwork(t, fake)
	to := uint64(9)
	recs, err := n.Logs(context.Background(), LogFilter{FromBlock: 1, ToBlock: &to})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	if recs[0].BlockHash != blockHash.Hex() {
		t.Errorf("block hash = %s, want %s", recs[0].BlockHash, blockHash.Hex())
	}
}
