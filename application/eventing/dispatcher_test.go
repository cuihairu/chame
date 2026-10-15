package eventing

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

// fakeClock is an advanceable clock for deterministic backoff tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeSource simulates one chain: canonical hashes per height, events
// per height, and re-observation answers. Reorgs are simulated by
// rewriting the chain between ticks.
type fakeSource struct {
	net     identifiers.NetworkID
	srcType event.SourceType

	mu      sync.Mutex
	chain   map[uint64]string        // canonical hash per height
	blocks  map[uint64][]event.Event // events per height (block hash field ignored; rebuilt on poll)
	observe map[event.EventID]event.FinalityState

	pollErr   error
	headErr   error
	replayAll bool // return every block regardless of the cursor
	polls     int
}

func newFakeSource(net identifiers.NetworkID, srcType event.SourceType) *fakeSource {
	return &fakeSource{
		net:     net,
		srcType: srcType,
		chain:   map[uint64]string{},
		blocks:  map[uint64][]event.Event{},
		observe: map[event.EventID]event.FinalityState{},
	}
}

// addBlock appends a block with its events; the event identities follow
// the canonical hash, so a reorged block's events are new identities.
func (s *fakeSource) addBlock(height uint64, hash string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chain[height] = hash
	s.blocks[height] = nil
	for i := 0; i < count; i++ {
		ev := event.Event{
			ID:            event.PositionKey(s.net, s.srcType, "0xtx", hash, height, uint64(i), ""),
			Network:       s.net,
			SourceType:    s.srcType,
			TransactionID: "0xtx",
			BlockHash:     hash,
			BlockHeight:   height,
			Index:         uint64(i),
			Type:          "deposit.detected",
			At:            time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Observation:   event.ObservationObserved,
			Finality:      event.FinalityConfirmed,
			Processing:    event.ProcessingQueued,
		}
		ev.CreatedAt, ev.UpdatedAt = ev.At, ev.At
		s.blocks[height] = append(s.blocks[height], ev)
	}
}

func (s *fakeSource) Network() identifiers.NetworkID { return s.net }
func (s *fakeSource) Type() event.SourceType         { return s.srcType }

func (s *fakeSource) Poll(_ context.Context, cursor event.Cursor) ([]event.Event, event.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.polls++
	if s.pollErr != nil {
		return nil, cursor, s.pollErr
	}
	// Highest canonical height.
	var head uint64
	for h := range s.chain {
		if h > head {
			head = h
		}
	}
	from := cursor.BlockHeight + 1
	if s.replayAll {
		from = 0
	}
	var out []event.Event
	for h := from; h <= head; h++ {
		hash, ok := s.chain[h]
		if !ok {
			continue
		}
		for _, ev := range s.blocks[h] {
			ev.BlockHash = hash
			out = append(out, ev)
		}
	}
	return out, event.Cursor{Network: s.net, SourceType: s.srcType, BlockHeight: head, BlockHash: s.chain[head]}, nil
}

func (s *fakeSource) Head(_ context.Context, height uint64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.headErr != nil {
		return "", s.headErr
	}
	hash, ok := s.chain[height]
	if !ok {
		return "", event.ErrPositionUnknown
	}
	return hash, nil
}

func (s *fakeSource) Observe(_ context.Context, ev event.Event) (event.FinalityState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.observe[ev.ID]; ok {
		return f, nil
	}
	return ev.Finality, nil
}

var testNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "eventing", ChainID: "1337"}

type suite struct {
	repo  *memory.EventStore
	src   *fakeSource
	clock *fakeClock
	disp  *Dispatcher
	// deliveries records (event id, count) per type.
	deliveries map[string]int
	// finality records the finality each delivered event carried.
	finality map[string]event.FinalityState
	failFor  map[string]error // handler error by event id
	mu       sync.Mutex
}

func newSuite(t *testing.T, cfg Config) *suite {
	t.Helper()
	s := &suite{
		repo:       memory.NewEventStore(),
		src:        newFakeSource(testNet, event.SourceEVMLog),
		clock:      &fakeClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		deliveries: map[string]int{},
		finality:   map[string]event.FinalityState{},
		failFor:    map[string]error{},
	}
	d, err := NewDispatcher(s.repo, cfg,
		WithSource(s.src),
		WithClock(s.clock),
	)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	if err := d.Handle("deposit.detected", func(_ context.Context, ev event.Event) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if err := s.failFor[string(ev.ID)]; err != nil {
			return err
		}
		s.deliveries[string(ev.ID)]++
		s.finality[string(ev.ID)] = ev.Finality
		return nil
	}); err != nil {
		t.Fatalf("handle: %v", err)
	}
	s.disp = d
	return s
}

func TestTickPersistsDeliversAndAdvances(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 2)
	s.src.addBlock(11, "0xb", 1)

	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Stored != 3 || report.Delivered != 3 || report.Sources != 1 {
		t.Fatalf("report: %+v", report)
	}
	cursor, err := s.repo.GetCursor(context.Background(), event.CursorKey{Network: testNet, SourceType: event.SourceEVMLog})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor.BlockHeight != 11 || cursor.BlockHash != "0xb" {
		t.Fatalf("cursor not advanced to the head: %+v", cursor)
	}

	// A second tick with nothing new: no stores, no redelivery.
	report, err = s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Stored != 0 || report.Delivered != 0 {
		t.Fatalf("idle tick must be a no-op: %+v", report)
	}
	if len(s.deliveries) != 3 {
		t.Fatalf("want exactly 3 delivered events, got %d", len(s.deliveries))
	}
}

func TestTickReplayStaysANoop(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	s.src.addBlock(11, "0xb", 2)
	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Simulate a crash-resume: the cursor is lost and the whole window is
	// re-read (§11.5). Dedup keeps the store and the business effects
	// single-shot (§11.3, §11.4).
	s.src.replayAll = true
	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Stored != 0 || report.Delivered != 0 {
		t.Fatalf("a re-read window must produce no duplicate effects: %+v", report)
	}
	total := 0
	for _, n := range s.deliveries {
		total += n
	}
	if total != 3 {
		t.Fatalf("handlers must have run exactly once per event, got %d", total)
	}
}

func TestTickWaitsForFinality(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	// The only event is still pending confirmation.
	for i, ev := range s.src.blocks[10] {
		ev.Finality = event.FinalityPending
		s.src.blocks[10][i] = ev
		s.src.observe[ev.ID] = event.FinalityPending
	}

	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Stored != 1 || report.Delivered != 0 || report.Promoted != 0 {
		t.Fatalf("a pending event must be stored but not delivered: %+v", report)
	}

	// Confirmation lands; the next tick promotes and delivers.
	for _, ev := range s.src.blocks[10] {
		s.src.observe[ev.ID] = event.FinalityConfirmed
	}
	report, err = s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Promoted != 1 || report.Delivered != 1 {
		t.Fatalf("a confirmed event must be promoted and delivered: %+v", report)
	}
	// The delivered record carries the promoted finality, not the one
	// the scan saw (spec §11.4).
	var id event.EventID
	for _, ev := range s.src.blocks[10] {
		id = ev.ID
	}
	s.mu.Lock()
	got := s.finality[string(id)]
	s.mu.Unlock()
	if got != event.FinalityConfirmed {
		t.Fatalf("handler saw finality %q, want confirmed", got)
	}
}

func TestTickRetriesThenPoisons(t *testing.T) {
	s := newSuite(t, Config{MaxAttempts: 2, RetryBase: time.Second})
	s.src.addBlock(10, "0xa", 1)
	for _, ev := range s.src.blocks[10] {
		s.failFor[string(ev.ID)] = errors.New("handler exploded")
	}

	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Failed != 1 || report.Delivered != 0 {
		t.Fatalf("first attempt fails and stays retryable: %+v", report)
	}
	var id event.EventID
	for _, ev := range s.src.blocks[10] {
		id = ev.ID
	}
	got, err := s.repo.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Attempts != 1 || !got.NextAttemptAt.After(s.clock.Now()) {
		t.Fatalf("failure must defer the next attempt: %+v", got)
	}

	// Before the backoff elapses the event is not due.
	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// After the backoff the second attempt exhausts the budget.
	poisoned := false
	s.disp.metrics.Poisoned = func(_ event.EventID, attempts int) { poisoned = true; _ = attempts }
	s.clock.advance(2 * time.Second)
	report, err = s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Poisoned != 1 || report.Failed != 0 {
		t.Fatalf("second attempt must poison, not fail: %+v", report)
	}
	if !poisoned {
		t.Fatal("the poisoned metric must fire")
	}
	got, err = s.repo.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Processing != event.ProcessingPoisoned || got.Attempts != 2 {
		t.Fatalf("poison must be visible and counted: %+v", got)
	}

	// A poisoned event is never retried, even much later.
	s.clock.advance(time.Hour)
	report, err = s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Failed != 0 && report.Delivered != 0 {
		t.Fatalf("a poisoned event must never run again: %+v", report)
	}
}

func TestTickHandlesReorg(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	s.src.addBlock(11, "0xb", 1)
	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// Reorg: height 11 is replaced by a different block with its own
	// event. The old event was already delivered and stays delivered —
	// compensation is the application's decision (§11.6).
	s.src.mu.Lock()
	oldID := s.src.blocks[11][0].ID
	s.src.mu.Unlock()
	s.src.addBlock(11, "0xc", 1)

	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Reorgs != 1 {
		t.Fatalf("the reorg must be counted: %+v", report)
	}
	cursor, err := s.repo.GetCursor(context.Background(), event.CursorKey{Network: testNet, SourceType: event.SourceEVMLog})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	// The cursor rolled back to the verified parent, then the poll
	// re-advanced it over the replacement block.
	if cursor.BlockHash != "0xc" || cursor.BlockHeight != 11 {
		t.Fatalf("cursor must sit on the new canonical block: %+v", cursor)
	}
	if cursor.RollbackCount != 1 || cursor.LastRollback.IsZero() {
		t.Fatalf("the rollback must be audited: %+v", cursor)
	}
	old, err := s.repo.Get(context.Background(), oldID)
	if err != nil {
		t.Fatalf("get old event: %v", err)
	}
	if old.Processing != event.ProcessingDelivered {
		t.Fatalf("delivered events stay terminal across a reorg: %+v", old)
	}
	newEv, err := s.repo.Get(context.Background(), event.PositionKey(testNet, event.SourceEVMLog, "0xtx", "0xc", 11, 0, ""))
	if err != nil {
		t.Fatalf("get new event: %v", err)
	}
	if newEv.Processing != event.ProcessingDelivered {
		t.Fatalf("the replacement event must be delivered: %+v", newEv)
	}
}

func TestTickReorgInvalidatesQueuedEvents(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	// Height 11 events are not deliverable yet: pending finality.
	s.src.addBlock(11, "0xb", 1)
	var queuedID event.EventID
	for i, ev := range s.src.blocks[11] {
		ev.Finality = event.FinalityPending
		s.src.blocks[11][i] = ev
		s.src.observe[ev.ID] = event.FinalityPending
		queuedID = ev.ID
	}
	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	s.src.addBlock(11, "0xc", 1)
	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.ReorgInvalidated != 1 {
		t.Fatalf("the queued event under the reorged block must be invalidated: %+v", report)
	}
	got, err := s.repo.Get(context.Background(), queuedID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Processing != event.ProcessingReorged || got.Observation != event.ObservationReorged {
		t.Fatalf("queued events under a reorged block are invalidated: %+v", got)
	}

	// Even after finality is confirmed elsewhere, a reorged event is
	// never delivered (§11.6 step 3).
	s.src.observe[queuedID] = event.FinalityFinal
	report, err = s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Delivered != 0 {
		t.Fatalf("a reorged event must never be delivered: %+v", report)
	}
}

func TestTickReorgWithUnverifiableParentRescansFromStart(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	s.src.addBlock(11, "0xb", 1)
	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}

	// A deep reorg: the parent can no longer be verified, so the cursor
	// rolls all the way back and the full window is rescanned (§11.5,
	// §11.6 step 5).
	s.src.mu.Lock()
	delete(s.src.chain, 10)
	s.src.chain[11] = "0xc"
	s.src.blocks[11] = nil
	s.src.mu.Unlock()
	s.src.addBlock(12, "0xd", 1)

	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	cursor, err := s.repo.GetCursor(context.Background(), event.CursorKey{Network: testNet, SourceType: event.SourceEVMLog})
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor.BlockHeight != 12 || cursor.BlockHash != "0xd" {
		t.Fatalf("the rescan must rebuild the cursor on the new chain: %+v", cursor)
	}
	if cursor.RollbackCount != 1 {
		t.Fatalf("the full rollback must be audited once: %+v", cursor)
	}
}

func TestTickMissingHandlerFailsVisibly(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	// The suite's dispatcher handles deposit.detected; forge an event of
	// an unhandled type directly in the store.
	ev := s.src.blocks[10][0]
	ev.ID = event.PositionKey(testNet, event.SourceEVMLog, "0xtx", "0xa", 10, 7, "")
	ev.Index = 7
	ev.Type = "unknown.type"
	if err := s.repo.Create(context.Background(), ev); err != nil {
		t.Fatalf("create: %v", err)
	}
	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("an unhandled type must count as a failure, not vanish: %+v", report)
	}
	got, err := s.repo.Get(context.Background(), ev.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Attempts != 1 {
		t.Fatalf("the missing handler must count as an attempt: %+v", got)
	}
}

func TestTickIsolatesSourceFailures(t *testing.T) {
	good := newFakeSource(testNet, event.SourceEVMLog)
	good.addBlock(10, "0xa", 1)
	bad := newFakeSource(identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "eventing", ChainID: "devnet"}, event.SourceSolanaLog)
	bad.pollErr = errors.New("rpc down")

	d, err := NewDispatcher(memory.NewEventStore(), Config{}, WithSource(good), WithSource(bad))
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	report, err := d.Tick(context.Background())
	if err == nil {
		t.Fatal("the failing source must surface its error")
	}
	if report.Sources != 2 || report.Stored != 1 {
		t.Fatalf("the healthy source must still be processed: %+v", report)
	}
}

func TestTickKeepsPositionWhenHeadIsUnknown(t *testing.T) {
	s := newSuite(t, Config{})
	s.src.addBlock(10, "0xa", 1)
	if _, err := s.disp.Tick(context.Background()); err != nil {
		t.Fatalf("tick: %v", err)
	}
	// The node cannot verify the cursor's block (pruned below its
	// horizon): the dispatcher must keep the position, not treat it as
	// a reorg, and keep making progress.
	s.src.headErr = event.ErrPositionUnknown
	s.src.addBlock(11, "0xb", 1)
	report, err := s.disp.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Reorgs != 0 || report.Stored != 1 {
		t.Fatalf("an unverifiable head is not a reorg: %+v", report)
	}
	cursor, _ := s.repo.GetCursor(context.Background(), event.CursorKey{Network: testNet, SourceType: event.SourceEVMLog})
	if cursor.BlockHeight != 11 {
		t.Fatalf("progress must continue: %+v", cursor)
	}
}

func TestRunStopsWithContext(t *testing.T) {
	s := newSuite(t, Config{PollInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.disp.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestNewDispatcherAndHandleValidation(t *testing.T) {
	if _, err := NewDispatcher(nil, Config{}, WithSource(newFakeSource(testNet, event.SourceEVMLog))); err == nil {
		t.Fatal("a nil repository must be rejected")
	}
	if _, err := NewDispatcher(memory.NewEventStore(), Config{}); err == nil {
		t.Fatal("an empty source set must be rejected")
	}
	if _, err := NewDispatcher(memory.NewEventStore(), Config{PollInterval: -time.Second}, WithSource(newFakeSource(testNet, event.SourceEVMLog))); err == nil {
		t.Fatal("negative config must be rejected")
	}
	two := newFakeSource(testNet, event.SourceEVMLog)
	if _, err := NewDispatcher(memory.NewEventStore(), Config{}, WithSource(two), WithSource(two)); err == nil {
		t.Fatal("duplicate sources must be rejected")
	}

	d, err := NewDispatcher(memory.NewEventStore(), Config{}, WithSource(two))
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	if err := d.Handle("", func(context.Context, event.Event) error { return nil }); err == nil {
		t.Fatal("an empty event type must be rejected")
	}
	if err := d.Handle("t", nil); err == nil {
		t.Fatal("a nil handler must be rejected")
	}
	if err := d.Handle("t", func(context.Context, event.Event) error { return nil }); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := d.Handle("t", func(context.Context, event.Event) error { return nil }); err == nil {
		t.Fatal("a duplicate handler must be rejected")
	}
}
