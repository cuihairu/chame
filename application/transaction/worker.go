// Package transaction implements the application-level transaction
// coordination (spec §9): driving the shared state machine across
// preparation, signing, submission, and recovery of unknown outcomes.
// The recovery worker (spec §10.4) scans for stuck transactions, leases
// them, and drives each through a pluggable Reconciler with bounded
// concurrency and task timeouts.
package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/clock"
)

// Reconciler resolves one stuck transaction (§10.4). It is the
// chain-specific half of recovery: the worker supplies the record and a
// deadline, the reconciler observes the chain, and returns the status
// the record should advance to. Returning the record's current status is
// a no-op observation. The worker applies the result through the
// repository's conditional update, so a reconciler never writes state
// directly and concurrent workers cannot contradict each other.
type Reconciler interface {
	Reconcile(ctx context.Context, tx txmodel.Transaction) (txmodel.TransactionStatus, error)
}

// Metrics carries the optional observability hooks (§10.4). Every field
// may be nil.
type Metrics struct {
	// Scan fires once per scan pass with the candidate count.
	Scan func(candidates int)
	// Claimed fires when a record was leased to this worker.
	Claimed func(id txmodel.TransactionID)
	// Skipped fires when a candidate was not reconciled here, with the
	// reason ("held by another worker", "advanced by another worker").
	Skipped func(id txmodel.TransactionID, reason string)
	// Reconciled fires when a record advanced to a new status.
	Reconciled func(id txmodel.TransactionID, from, to txmodel.TransactionStatus)
	// Failed fires when a claim, reconcile, or apply step failed.
	Failed func(id txmodel.TransactionID, err error)
}

// Config tunes the worker. Zero fields take the defaults; negative
// values are rejected at construction.
type Config struct {
	// ScanInterval paces the scan loop and is the base retry backoff: a
	// record that fails reconciliation is retried on a later scan.
	// Default 30s.
	ScanInterval time.Duration
	// LeaseTTL is how long a claim on a record lasts. It must outlive
	// TaskTimeout. Default 5m.
	LeaseTTL time.Duration
	// TaskTimeout bounds one reconcile call. Default 30s.
	TaskTimeout time.Duration
	// MaxConcurrent bounds records reconciled in parallel per scan.
	// Default 8.
	MaxConcurrent int
	// BatchSize bounds one scan page. Default 100.
	BatchSize int
	// MinStaleAge keeps freshly updated records out of the scan: an
	// in-flight flow may still be advancing them. Default 30s.
	MinStaleAge time.Duration
	// StuckStatuses selects which statuses count as stuck. Default:
	// prepared, signed, submitted, unknown, reorged. Included records
	// wait on the confirmation policy, not on recovery.
	StuckStatuses []txmodel.TransactionStatus
}

// withDefaults fills zero fields.
func (c Config) withDefaults() Config {
	if c.ScanInterval == 0 {
		c.ScanInterval = 30 * time.Second
	}
	if c.LeaseTTL == 0 {
		c.LeaseTTL = 5 * time.Minute
	}
	if c.TaskTimeout == 0 {
		c.TaskTimeout = 30 * time.Second
	}
	if c.MaxConcurrent == 0 {
		c.MaxConcurrent = 8
	}
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
	if c.MinStaleAge == 0 {
		c.MinStaleAge = 30 * time.Second
	}
	if len(c.StuckStatuses) == 0 {
		c.StuckStatuses = []txmodel.TransactionStatus{
			txmodel.TxPrepared,
			txmodel.TxSigned,
			txmodel.TxSubmitted,
			txmodel.TxUnknown,
			txmodel.TxReorged,
		}
	}
	return c
}

// validate rejects configurations that cannot run.
func (c Config) validate() error {
	if c.ScanInterval <= 0 {
		return fmt.Errorf("transaction worker: scan interval must be positive")
	}
	if c.LeaseTTL <= 0 {
		return fmt.Errorf("transaction worker: lease ttl must be positive")
	}
	if c.TaskTimeout <= 0 {
		return fmt.Errorf("transaction worker: task timeout must be positive")
	}
	if c.MaxConcurrent < 1 {
		return fmt.Errorf("transaction worker: max concurrent must be at least 1")
	}
	if c.BatchSize < 1 {
		return fmt.Errorf("transaction worker: batch size must be at least 1")
	}
	if c.MinStaleAge < 0 {
		return fmt.Errorf("transaction worker: min stale age must not be negative")
	}
	if len(c.StuckStatuses) == 0 {
		return fmt.Errorf("transaction worker: at least one stuck status is required")
	}
	return nil
}

// Worker is the recovery worker (§10.4). Workers may run on many
// instances at once; the repository's lease protocol keeps one worker
// per record, and conditional status updates keep their decisions from
// contradicting each other.
type Worker struct {
	repo       txmodel.Repository
	reconciler Reconciler
	holder     string
	cfg        Config
	now        clock.Clock
	logger     *slog.Logger
	metrics    Metrics
}

// Option customizes the worker.
type Option func(*Worker)

// WithHolder sets the lease holder id. Default: hostname-pid, unique per
// instance.
func WithHolder(h string) Option {
	return func(w *Worker) { w.holder = h }
}

// WithLogger injects the diagnostic logger. Default: slog.Default.
func WithLogger(l *slog.Logger) Option {
	return func(w *Worker) { w.logger = l }
}

// WithMetrics injects the observability hooks.
func WithMetrics(m Metrics) Option {
	return func(w *Worker) { w.metrics = m }
}

// WithClock injects the time source. Default: clock.System.
func WithClock(c clock.Clock) Option {
	return func(w *Worker) { w.now = c }
}

// NewWorker builds the worker. Repository and reconciler are required: a
// worker without either would scan forever and never recover anything.
func NewWorker(repo txmodel.Repository, reconciler Reconciler, cfg Config, opts ...Option) (*Worker, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if repo == nil {
		return nil, fmt.Errorf("transaction worker: repository is required")
	}
	if reconciler == nil {
		return nil, fmt.Errorf("transaction worker: reconciler is required")
	}
	w := &Worker{
		repo:       repo,
		reconciler: reconciler,
		holder:     defaultHolder(),
		cfg:        cfg,
		now:        clock.System{},
		logger:     slog.Default(),
	}
	for _, opt := range opts {
		opt(w)
	}
	return w, nil
}

// ScanReport summarizes one scan pass.
type ScanReport struct {
	Scanned    int
	Claimed    int
	Reconciled int
	Failed     int
	Skipped    int
}

// Run scans until ctx is done, one page per interval. A scan that
// overruns its interval drops the next tick instead of piling up. Run
// returns ctx.Err() on cancellation.
func (w *Worker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.cfg.ScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			report, err := w.ScanOnce(ctx)
			if err != nil {
				w.log().Error("recovery scan failed", "err", err)
				continue
			}
			if report.Scanned > 0 {
				w.log().Info("recovery scan",
					"scanned", report.Scanned,
					"reconciled", report.Reconciled,
					"failed", report.Failed,
					"skipped", report.Skipped)
			}
		}
	}
}

// ScanOnce runs one bounded scan pass (§10.4): one page of candidates,
// each leased and reconciled under a task timeout, at most MaxConcurrent
// at a time.
func (w *Worker) ScanOnce(ctx context.Context) (ScanReport, error) {
	var report ScanReport
	candidates, err := w.repo.FindStale(ctx, txmodel.StaleQuery{
		Statuses:  w.cfg.StuckStatuses,
		OlderThan: w.now.Now().Add(-w.cfg.MinStaleAge),
		Limit:     w.cfg.BatchSize,
	})
	if err != nil {
		return report, err
	}
	report.Scanned = len(candidates)
	if w.metrics.Scan != nil {
		w.metrics.Scan(len(candidates))
	}

	sem := make(chan struct{}, w.cfg.MaxConcurrent)
	st := &scanState{report: &report}
	var wg sync.WaitGroup
	for _, tx := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(tx txmodel.Transaction) {
			defer wg.Done()
			defer func() { <-sem }()
			reconciled, err := w.recoverOne(ctx, tx)
			st.record(reconciled, err)
		}(tx)
	}
	wg.Wait()
	return report, nil
}

// ReconcileOne manually reconciles one record (§10.4 manual entry). It
// returns ErrRecordNotFound for an unknown id.
func (w *Worker) ReconcileOne(ctx context.Context, id txmodel.TransactionID) error {
	tx, err := w.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = w.recoverOne(ctx, tx)
	return err
}

// scanState aggregates one scan pass's counters across goroutines.
type scanState struct {
	mu     sync.Mutex
	report *ScanReport
}

func (s *scanState) record(reconciled bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case err != nil:
		s.report.Failed++
	case reconciled:
		s.report.Reconciled++
	default:
		s.report.Skipped++
	}
}

// recoverOne leases and reconciles one record, always releasing the
// lease afterwards so the next scan can retry. It reports whether the
// record advanced.
func (w *Worker) recoverOne(ctx context.Context, tx txmodel.Transaction) (bool, error) {
	now := w.now.Now()
	lease := txmodel.Lease{Holder: w.holder, At: now, ExpiresAt: now.Add(w.cfg.LeaseTTL)}
	claimed, err := w.repo.Claim(ctx, tx.ID, lease)
	if err != nil {
		return false, err
	}
	if !claimed {
		w.skip(tx.ID, "held by another worker")
		return false, nil
	}
	if w.metrics.Claimed != nil {
		w.metrics.Claimed(tx.ID)
	}

	taskCtx, cancel := context.WithTimeout(ctx, w.cfg.TaskTimeout)
	defer cancel()
	next, err := w.reconciler.Reconcile(taskCtx, tx)
	if err != nil {
		w.fail(tx.ID, err)
		_ = w.repo.Release(ctx, tx.ID, w.holder)
		return false, err
	}
	if next == tx.Status {
		// No-op observation: nothing to apply.
		_ = w.repo.Release(ctx, tx.ID, w.holder)
		return false, nil
	}
	if err := w.repo.UpdateStatus(ctx, tx.ID, tx.Status, next, w.now.Now()); err != nil {
		if errors.Is(err, txmodel.ErrStatusConflict) {
			// Another worker advanced the record first; its decision
			// stands (§10.2).
			w.skip(tx.ID, "advanced by another worker")
			_ = w.repo.Release(ctx, tx.ID, w.holder)
			return false, nil
		}
		w.fail(tx.ID, err)
		_ = w.repo.Release(ctx, tx.ID, w.holder)
		return false, err
	}
	if w.metrics.Reconciled != nil {
		w.metrics.Reconciled(tx.ID, tx.Status, next)
	}
	_ = w.repo.Release(ctx, tx.ID, w.holder)
	return true, nil
}

func (w *Worker) skip(id txmodel.TransactionID, reason string) {
	if w.metrics.Skipped != nil {
		w.metrics.Skipped(id, reason)
	}
	w.log().Info("recovery skip", "tx", id, "reason", reason)
}

func (w *Worker) fail(id txmodel.TransactionID, err error) {
	if w.metrics.Failed != nil {
		w.metrics.Failed(id, err)
	}
	w.log().Error("recovery failed", "tx", id, "err", err)
}

func (w *Worker) log() *slog.Logger {
	if w.logger != nil {
		return w.logger
	}
	return slog.Default()
}

// defaultHolder builds a per-instance lease holder id.
func defaultHolder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}
