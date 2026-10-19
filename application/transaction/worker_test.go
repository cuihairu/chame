package transaction_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	txapp "github.com/cuihairu/chame/application/transaction"
	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

var testNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "sepolia", ChainID: "11155111"}

// fixedClock serves one frozen instant, optionally advancing per call.
type fixedClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fixedClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// fakeReconciler maps records to outcomes: a next status or an error.
type fakeReconciler struct {
	mu       sync.Mutex
	next     map[transaction.TransactionID]transaction.TransactionStatus
	err      map[transaction.TransactionID]error
	blockFor map[transaction.TransactionID]time.Duration
	calls    []transaction.TransactionID
}

func (f *fakeReconciler) Reconcile(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error) {
	f.mu.Lock()
	f.calls = append(f.calls, tx.ID)
	next := f.next[tx.ID]
	wait := f.blockFor[tx.ID]
	rerr := f.err[tx.ID]
	f.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	if rerr != nil {
		return "", rerr
	}
	if next == "" {
		return tx.Status, nil // no-op observation
	}
	return next, nil
}

func (f *fakeReconciler) callCount(id transaction.TransactionID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == id {
			n++
		}
	}
	return n
}

// suite wires a memory store, a clock, a reconciler, and a worker.
type suite struct {
	repo   transaction.Repository
	clock  *fixedClock
	rec    *fakeReconciler
	worker *txapp.Worker
}

func newSuite(t *testing.T, cfg txapp.Config) *suite {
	t.Helper()
	s := &suite{
		repo:  memory.New(),
		clock: &fixedClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)},
		rec:   &fakeReconciler{next: map[transaction.TransactionID]transaction.TransactionStatus{}, err: map[transaction.TransactionID]error{}, blockFor: map[transaction.TransactionID]time.Duration{}},
	}
	w, err := txapp.NewWorker(s.repo, s.rec, cfg,
		txapp.WithHolder("worker-under-test"),
		txapp.WithClock(s.clock),
		txapp.WithLogger(slog.New(slog.DiscardHandler)),
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	s.worker = w
	return s
}

func (s *suite) seed(t *testing.T, id string, st transaction.TransactionStatus, age time.Duration) transaction.Transaction {
	t.Helper()
	at := s.clock.Now().Add(-age)
	tx := transaction.Transaction{
		ID:          transaction.TransactionID(id),
		OperationID: transaction.OperationID("op-" + id),
		Network:     testNet,
		Status:      st,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if _, err := s.repo.Create(context.Background(), tx); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return tx
}

func TestScanOnceReconcilesStuckRecords(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	s.seed(t, "stuck-prepared", transaction.TxPrepared, time.Hour)
	s.seed(t, "stuck-submitted", transaction.TxSubmitted, 2*time.Hour)
	s.seed(t, "stuck-unknown", transaction.TxUnknown, time.Hour)
	s.seed(t, "fresh-submitted", transaction.TxSubmitted, 5*time.Second) // too fresh
	s.seed(t, "included-waiting", transaction.TxIncluded, 9*time.Hour)   // not a stuck status
	s.rec.next["stuck-prepared"] = transaction.TxSigned
	s.rec.next["stuck-submitted"] = transaction.TxPending
	s.rec.next["stuck-unknown"] = transaction.TxFinalized

	report, err := s.worker.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan once: %v", err)
	}
	if report.Scanned != 3 || report.Reconciled != 3 {
		t.Fatalf("want 3 scanned and 3 reconciled, got %+v", report)
	}
	want := map[string]transaction.TransactionStatus{
		"stuck-prepared":  transaction.TxSigned,
		"stuck-submitted": transaction.TxPending,
		"stuck-unknown":   transaction.TxFinalized,
	}
	for id, st := range want {
		got, err := s.repo.Get(ctx, transaction.TransactionID(id))
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if got.Status != st {
			t.Fatalf("%s: want %s, got %s", id, st, got.Status)
		}
	}
}

func TestScanOnceSkipsRecordsHeldByOthers(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	s.seed(t, "held", transaction.TxUnknown, time.Hour)
	lease := transaction.Lease{
		Holder:    "someone-else",
		At:        s.clock.Now(),
		ExpiresAt: s.clock.Now().Add(time.Hour),
	}
	if ok, err := s.repo.Claim(ctx, "held", lease); err != nil || !ok {
		t.Fatalf("setup claim: ok=%v err=%v", ok, err)
	}

	report, err := s.worker.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan once: %v", err)
	}
	if report.Scanned != 1 || report.Skipped != 1 || report.Reconciled != 0 {
		t.Fatalf("want 1 scanned 1 skipped, got %+v", report)
	}
	if len(s.rec.calls) != 0 {
		t.Fatalf("reconciler must not run on held records, got %v", s.rec.calls)
	}
}

func TestScanOnceReportsReconcileFailureAndReleases(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	s.seed(t, "broken", transaction.TxUnknown, time.Hour)
	boom := errors.New("rpc down")
	s.rec.err["broken"] = boom

	var failedIDs []transaction.TransactionID
	var failedErrs []error
	w, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{},
		txapp.WithHolder("worker-under-test"),
		txapp.WithClock(s.clock),
		txapp.WithLogger(slog.New(slog.DiscardHandler)),
		txapp.WithMetrics(txapp.Metrics{
			Failed: func(id transaction.TransactionID, err error) {
				failedIDs = append(failedIDs, id)
				failedErrs = append(failedErrs, err)
			},
		}))
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	report, err := w.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan once: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("want 1 failed, got %+v", report)
	}
	if len(failedIDs) != 1 || failedIDs[0] != "broken" || !errors.Is(failedErrs[0], boom) {
		t.Fatalf("failure hook missing: %v %v", failedIDs, failedErrs)
	}
	// The record stays stuck and the lease is released: the next scan can
	// retry it.
	got, err := s.repo.Get(ctx, "broken")
	if err != nil || got.Status != transaction.TxUnknown {
		t.Fatalf("failed record must stay stuck: %+v %v", got, err)
	}
	if ok, err := s.repo.Claim(ctx, "broken", transaction.Lease{Holder: "other", At: s.clock.Now(), ExpiresAt: s.clock.Now().Add(time.Minute)}); err != nil || !ok {
		t.Fatalf("lease must be released after failure: ok=%v err=%v", ok, err)
	}
}

func TestScanOnceBoundsTaskTime(t *testing.T) {
	s := newSuite(t, txapp.Config{TaskTimeout: 30 * time.Millisecond})
	ctx := context.Background()
	s.seed(t, "slow", transaction.TxUnknown, time.Hour)
	s.rec.blockFor["slow"] = 5 * time.Second

	report, err := s.worker.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan once: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("want the timed-out task to fail, got %+v", report)
	}
}

func TestScanOnceAppliesMatrixAndSkipsIllegalResults(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	s.seed(t, "illegal", transaction.TxUnknown, time.Hour)
	s.rec.next["illegal"] = transaction.TxReorged // unknown -> reorged is not legal

	report, err := s.worker.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan once: %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("illegal reconciler result must fail the task, got %+v", report)
	}
	got, _ := s.repo.Get(ctx, "illegal")
	if got.Status != transaction.TxUnknown {
		t.Fatalf("illegal result must not move the record, got %s", got.Status)
	}
}

func TestReconcileOneManualEntry(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	s.seed(t, "manual", transaction.TxUnknown, time.Hour)
	s.rec.next["manual"] = transaction.TxConfirmed

	if err := s.worker.ReconcileOne(ctx, "manual"); err != nil {
		t.Fatalf("reconcile one: %v", err)
	}
	got, _ := s.repo.Get(ctx, "manual")
	if got.Status != transaction.TxConfirmed {
		t.Fatalf("want confirmed, got %s", got.Status)
	}

	if err := s.worker.ReconcileOne(ctx, "missing"); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("want ErrRecordNotFound, got %v", err)
	}
}

func TestScanOnceConcurrentWorkersDedupByLease(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	const stuck = 8
	for i := 0; i < stuck; i++ {
		s.seed(t, fmt.Sprintf("stuck-%d", i), transaction.TxUnknown, time.Hour)
	}
	workers := make([]*txapp.Worker, 2)
	for i := range workers {
		w, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{},
			txapp.WithHolder(fmt.Sprintf("worker-%d", i)),
			txapp.WithClock(s.clock),
			txapp.WithLogger(slog.New(slog.DiscardHandler)),
		)
		if err != nil {
			t.Fatalf("new worker: %v", err)
		}
		workers[i] = w
	}
	s.rec.next = map[transaction.TransactionID]transaction.TransactionStatus{}
	for i := 0; i < stuck; i++ {
		s.rec.next[transaction.TransactionID(fmt.Sprintf("stuck-%d", i))] = transaction.TxFinalized
	}

	var wg sync.WaitGroup
	reports := make([]txapp.ScanReport, len(workers))
	for i, w := range workers {
		wg.Add(1)
		go func(i int, w *txapp.Worker) {
			defer wg.Done()
			r, err := w.ScanOnce(ctx)
			if err != nil {
				t.Errorf("worker %d scan: %v", i, err)
			}
			reports[i] = r
		}(i, w)
	}
	wg.Wait()

	// Each record advances exactly once: the lease dedups live claims and
	// the conditional update rejects any worker whose view went stale. A
	// record whose lease was already released may still be observed twice
	// — that is wasted observation, never a double advance.
	total := 0
	for _, r := range reports {
		total += r.Reconciled
		if r.Failed != 0 {
			t.Fatalf("no recovery may fail here, got %+v", r)
		}
	}
	if total != stuck {
		t.Fatalf("each record must advance exactly once, got %d of %d", total, stuck)
	}
	for i := 0; i < stuck; i++ {
		id := transaction.TransactionID(fmt.Sprintf("stuck-%d", i))
		if n := s.rec.callCount(id); n < 1 || n > 2 {
			t.Fatalf("%s observed %d times, want 1 or 2", id, n)
		}
		got, err := s.repo.Get(ctx, id)
		if err != nil || got.Status != transaction.TxFinalized {
			t.Fatalf("%s: want finalized, got %+v %v", id, got, err)
		}
	}
}

func TestRunStopsOnCancellation(t *testing.T) {
	s := newSuite(t, txapp.Config{ScanInterval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.worker.Run(ctx) }()
	time.Sleep(40 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want context.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("run did not stop on cancellation")
	}
}

func TestWorkerConfigDefaultsAndValidation(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	// Defaults applied silently: construction succeeded above.
	if _, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{}); err != nil {
		t.Fatalf("zero config must take defaults: %v", err)
	}
	if _, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{ScanInterval: -1}); err == nil {
		t.Fatalf("negative scan interval must be rejected")
	}
	if _, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{MaxConcurrent: -1}); err == nil {
		t.Fatalf("negative concurrency must be rejected")
	}
	if _, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{BatchSize: -1}); err == nil {
		t.Fatalf("negative batch size must be rejected")
	}
	if _, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{LeaseTTL: -time.Second}); err == nil {
		t.Fatalf("negative lease ttl must be rejected")
	}
	if _, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{MinStaleAge: -time.Second}); err == nil {
		t.Fatalf("negative min stale age must be rejected")
	}
	if _, err := txapp.NewWorker(nil, s.rec, txapp.Config{}); err == nil {
		t.Fatalf("nil repository must be rejected")
	}
}

func TestScanOnceFiresScanMetric(t *testing.T) {
	s := newSuite(t, txapp.Config{})
	ctx := context.Background()
	s.seed(t, "stuck", transaction.TxUnknown, time.Hour)

	var scanned int
	w, err := txapp.NewWorker(s.repo, s.rec, txapp.Config{},
		txapp.WithHolder("worker-under-test"),
		txapp.WithClock(s.clock),
		txapp.WithLogger(slog.New(slog.DiscardHandler)),
		txapp.WithMetrics(txapp.Metrics{Scan: func(n int) { scanned = n }}),
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	if _, err := w.ScanOnce(ctx); err != nil {
		t.Fatalf("scan once: %v", err)
	}
	if scanned != 1 {
		t.Fatalf("scan hook got %d, want 1", scanned)
	}
}
