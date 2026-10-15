// Package eventing implements event delivery orchestration (spec §11):
// it reads chain events from pluggable sources, persists them with
// identity deduplication, gates delivery on finality, retries failures,
// advances resumable cursors, and handles reorganizations by invalidating
// and rescanning. Delivery is at least once; consumers must be safe to
// run repeatedly for the same event (§11.4).
package eventing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/clock"
)

// Source is the chain event origin contract (§11.1), defined in the
// capability layer so chain adapters can implement it without importing
// the application layer.
type Source = event.Source

// Handler consumes one event (§11.4). Delivery is at least once, so a
// handler must be safe to run repeatedly for the same event; consumers
// use the event identity, business unique keys, or transaction
// constraints for idempotency (§11.4).
type Handler = func(ctx context.Context, ev event.Event) error

// Config tunes the dispatcher. Zero fields take the defaults; negative
// values are rejected at construction.
type Config struct {
	// PollInterval is how often Run polls every source. Default 10s.
	PollInterval time.Duration
	// DeliverLimit bounds the events delivered per source per tick.
	// Default 100.
	DeliverLimit int
	// MinFinality gates delivery (§11.4): an event is delivered once its
	// finality reaches this state. Default confirmed.
	MinFinality event.FinalityState
	// MaxAttempts is how many handler attempts one event gets before it
	// is poisoned. Default 10.
	MaxAttempts int
	// RetryBase is the first retry delay. Default 1s.
	RetryBase time.Duration
	// RetryMax caps the exponential retry delay. Default 1m.
	RetryMax time.Duration
}

// Defaults applied to zero config fields.
const (
	defaultPollInterval = 10 * time.Second
	defaultDeliverLimit = 100
	defaultMaxAttempts  = 10
	defaultRetryBase    = time.Second
	defaultRetryMax     = time.Minute
)

func (c Config) withDefaults() Config {
	if c.PollInterval == 0 {
		c.PollInterval = defaultPollInterval
	}
	if c.DeliverLimit == 0 {
		c.DeliverLimit = defaultDeliverLimit
	}
	if !c.MinFinality.Valid() {
		c.MinFinality = event.FinalityConfirmed
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = defaultMaxAttempts
	}
	if c.RetryBase == 0 {
		c.RetryBase = defaultRetryBase
	}
	if c.RetryMax == 0 {
		c.RetryMax = defaultRetryMax
	}
	return c
}

func (c Config) validate() error {
	if c.PollInterval < 0 {
		return fmt.Errorf("eventing: poll interval must not be negative")
	}
	if c.DeliverLimit < 0 {
		return fmt.Errorf("eventing: deliver limit must not be negative")
	}
	if c.MaxAttempts < 0 {
		return fmt.Errorf("eventing: max attempts must not be negative")
	}
	if c.RetryBase < 0 || c.RetryMax < 0 {
		return fmt.Errorf("eventing: retry delays must not be negative")
	}
	return nil
}

// Metrics are optional observability hooks (§15). Nil entries are skipped.
type Metrics struct {
	// Tick fires after every completed cycle.
	Tick func(report Report)
	// Reorg fires when a source cursor was rolled back (§11.6).
	Reorg func(key event.CursorKey, from, to event.Cursor, invalidated int)
	// Poisoned fires when an event exhausts its retry budget (§11.4).
	Poisoned func(id event.EventID, attempts int)
}

// Report summarizes one tick across all sources.
type Report struct {
	// Stored counts newly persisted events.
	Stored int
	// Delivered counts handler successes.
	Delivered int
	// Failed counts handler failures that stay retryable.
	Failed int
	// Poisoned counts events that exhausted the retry budget.
	Poisoned int
	// Promoted counts events whose finality reached the requirement.
	Promoted int
	// Reorgs counts cursor rollbacks (§11.6 step 5).
	Reorgs int
	// ReorgInvalidated counts events invalidated by reorgs (§11.6 step 2).
	ReorgInvalidated int
	// Sources counts the sources processed.
	Sources int
}

// Dispatcher runs the §11.4 pipeline over a set of sources: poll, store,
// gate on finality, deliver, retry, advance cursors, and recover from
// reorganizations.
type Dispatcher struct {
	repo     event.Repository
	cfg      Config
	now      clock.Clock
	logger   *slog.Logger
	metrics  Metrics
	sources  []Source
	handlers map[string]Handler
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// WithSource adds one event source. At least one is required.
func WithSource(s Source) Option {
	return func(d *Dispatcher) {
		if s != nil {
			d.sources = append(d.sources, s)
		}
	}
}

// WithLogger injects the logger. Default: slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(d *Dispatcher) { d.logger = l }
}

// WithClock injects the time source. Default: clock.System.
func WithClock(c clock.Clock) Option {
	return func(d *Dispatcher) { d.now = c }
}

// WithMetrics installs observability hooks.
func WithMetrics(m Metrics) Option {
	return func(d *Dispatcher) { d.metrics = m }
}

// NewDispatcher validates the configuration and returns a dispatcher.
// A nil repository or an empty source set is a construction error.
func NewDispatcher(repo event.Repository, cfg Config, opts ...Option) (*Dispatcher, error) {
	if repo == nil {
		return nil, errors.New("eventing: repository must not be nil")
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	d := &Dispatcher{
		repo:     repo,
		cfg:      cfg.withDefaults(),
		now:      clock.System{},
		logger:   slog.Default(),
		handlers: map[string]Handler{},
	}
	for _, o := range opts {
		o(d)
	}
	if len(d.sources) == 0 {
		return nil, errors.New("eventing: at least one source is required")
	}
	seen := map[event.CursorKey]bool{}
	for _, s := range d.sources {
		k := event.CursorKey{Network: s.Network(), SourceType: s.Type()}
		if seen[k] {
			return nil, fmt.Errorf("eventing: duplicate source %s %s", k.Network, k.SourceType)
		}
		seen[k] = true
	}
	return d, nil
}

// Handle registers the handler for one normalized event type. One handler
// per type: a duplicate registration is a configuration error (§11.4 —
// an event has one consumer path; fan-out is the handler's job).
func (d *Dispatcher) Handle(eventType string, h Handler) error {
	if eventType == "" {
		return errors.New("eventing: event type must not be empty")
	}
	if h == nil {
		return errors.New("eventing: handler must not be nil")
	}
	if _, dup := d.handlers[eventType]; dup {
		return fmt.Errorf("eventing: event type %q already has a handler", eventType)
	}
	d.handlers[eventType] = h
	return nil
}

// Run ticks on the configured interval until ctx is done. A tick error is
// logged and does not stop the loop — chain sources fail transiently —
// but the context is honored between ticks.
func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.cfg.PollInterval)
	defer ticker.Stop()
	for {
		if _, err := d.Tick(ctx); err != nil {
			d.logger.Error("event tick failed", slog.String("err", err.Error()))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Tick runs one full consumption cycle over every source. An error from
// one source is reported but does not stop the others; the joined error
// summarizes the cycle.
func (d *Dispatcher) Tick(ctx context.Context) (Report, error) {
	var report Report
	var errs []error
	for _, src := range d.sources {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		srcReport, err := d.tickSource(ctx, src)
		report.Stored += srcReport.Stored
		report.Delivered += srcReport.Delivered
		report.Failed += srcReport.Failed
		report.Poisoned += srcReport.Poisoned
		report.Promoted += srcReport.Promoted
		report.Reorgs += srcReport.Reorgs
		report.ReorgInvalidated += srcReport.ReorgInvalidated
		report.Sources++
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s %s: %w", src.Network(), src.Type(), err))
		}
	}
	if d.metrics.Tick != nil {
		d.metrics.Tick(report)
	}
	return report, errors.Join(errs...)
}

// tickSource runs the pipeline for one source: reorg check, poll, store,
// cursor advance, then delivery (§11.4, §11.5, §11.6).
func (d *Dispatcher) tickSource(ctx context.Context, src Source) (Report, error) {
	var report Report
	key := event.CursorKey{Network: src.Network(), SourceType: src.Type()}
	cursor, err := d.repo.GetCursor(ctx, key)
	if errors.Is(err, event.ErrCursorNotFound) {
		cursor = event.Cursor{Network: key.Network, SourceType: key.SourceType}
	} else if err != nil {
		return report, fmt.Errorf("read cursor: %w", err)
	}

	// §11.6: detect a reorganization under the cursor before polling.
	next, invalidated, reorged, err := d.checkReorg(ctx, src, cursor)
	if err != nil {
		return report, err
	}
	cursor = next
	report.ReorgInvalidated += invalidated
	if reorged {
		report.Reorgs++
	}

	// §11.5: poll new events from the (possibly rolled back) position.
	events, head, err := src.Poll(ctx, cursor)
	if err != nil {
		return report, fmt.Errorf("poll from %d: %w", cursor.BlockHeight, err)
	}

	// §11.5: persist events first, advance the cursor after.
	for _, ev := range events {
		if err := d.repo.Create(ctx, ev); err != nil {
			if errors.Is(err, event.ErrDuplicateEvent) {
				continue // already stored: replay stays a no-op (§11.3)
			}
			return report, fmt.Errorf("store event %s: %w", ev.ID, err)
		}
		report.Stored++
	}
	if head.Key() != key || head.BlockHeight < cursor.BlockHeight {
		return report, fmt.Errorf("source %s %s returned cursor %d for the wrong position",
			key.Network, key.SourceType, head.BlockHeight)
	}
	if head.BlockHeight > cursor.BlockHeight {
		head.UpdatedAt = d.now.Now()
		if err := d.repo.SaveCursor(ctx, head); err != nil {
			// A regression here means the chain moved under us; the next
			// tick's reorg check picks it up (§11.5 detection).
			if !errors.Is(err, event.ErrCursorRegression) {
				return report, fmt.Errorf("advance cursor: %w", err)
			}
			d.logger.Warn("cursor regression on advance, deferring to reorg check",
				slog.String("network", key.Network.String()),
				slog.String("source", string(key.SourceType)))
		}
	}

	// §11.4: gate on finality, then deliver.
	err = d.deliverReady(ctx, src, &report)
	return report, err
}

// deliverReady promotes stored events whose finality reached the
// requirement and delivers every ready event in chain order.
func (d *Dispatcher) deliverReady(ctx context.Context, src Source, report *Report) error {
	now := d.now.Now()
	pending, err := d.repo.FindPending(ctx, event.PendingQuery{
		NotBefore:   now,
		MinFinality: event.FinalityPending, // everything queued; gating happens below
		Limit:       d.cfg.DeliverLimit,
		Network:     src.Network(),
		SourceType:  src.Type(),
	})
	if err != nil {
		return fmt.Errorf("scan pending: %w", err)
	}
	for _, ev := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		// §11.4: wait for confirmation/finality per configuration.
		if ev.Finality.Rank() < d.cfg.MinFinality.Rank() {
			promoted, err := d.promote(ctx, src, &ev, now)
			if err != nil {
				return err
			}
			if promoted {
				report.Promoted++
			}
			if !promoted {
				continue
			}
		}
		delivered, err := d.deliver(ctx, ev, now)
		if err != nil {
			return err
		}
		switch delivered {
		case outcomeDelivered:
			report.Delivered++
		case outcomeFailed:
			report.Failed++
		case outcomePoisoned:
			report.Poisoned++
		}
	}
	return nil
}

// promote re-observes one event's finality and stores the promotion. It
// reports whether the event reached the requirement, and leaves the
// promoted state on the record so the handler sees what was persisted
// (spec §11.4). An observation failure is transient: the event stays
// queued for the next tick.
func (d *Dispatcher) promote(ctx context.Context, src Source, ev *event.Event, now time.Time) (bool, error) {
	finality, err := src.Observe(ctx, *ev)
	if err != nil {
		if errors.Is(err, event.ErrPositionUnknown) {
			return false, nil // position cannot be verified yet: keep waiting
		}
		return false, fmt.Errorf("observe %s: %w", ev.ID, err)
	}
	if !finality.Valid() {
		return false, fmt.Errorf("observe %s: source returned invalid finality %q", ev.ID, finality)
	}
	if finality == ev.Finality || finality.Rank() < ev.Finality.Rank() {
		return false, nil // nothing new
	}
	if err := d.repo.SetFinality(ctx, ev.ID, finality, now); err != nil {
		return false, fmt.Errorf("store finality for %s: %w", ev.ID, err)
	}
	ev.Finality = finality
	return ev.Finality.Rank() >= d.cfg.MinFinality.Rank(), nil
}

// outcome describes one delivery attempt.
type outcome int

const (
	outcomeDelivered outcome = iota
	outcomeFailed
	outcomePoisoned
)

// deliver runs the handler once and records the outcome. A missing
// handler is a configuration failure and counts as an attempt, so the
// event surfaces as poisoned instead of looping forever.
func (d *Dispatcher) deliver(ctx context.Context, ev event.Event, now time.Time) (outcome, error) {
	h, ok := d.handlers[ev.Type]
	if !ok {
		err := d.repo.RecordFailure(ctx, ev.ID, d.backoff(ev.Attempts+1, now), now)
		if err != nil {
			return outcomeFailed, fmt.Errorf("record missing handler for %s: %w", ev.ID, err)
		}
		if ev.Attempts+1 >= d.cfg.MaxAttempts {
			return d.poison(ctx, ev.ID, now)
		}
		d.logger.Warn("no handler registered for event type",
			slog.String("type", ev.Type), slog.String("id", string(ev.ID)))
		return outcomeFailed, nil
	}
	if err := h(ctx, ev); err != nil {
		// §11.4: a failed delivery retries with backoff, then poisons.
		if err := d.repo.RecordFailure(ctx, ev.ID, d.backoff(ev.Attempts+1, now), now); err != nil {
			return outcomeFailed, fmt.Errorf("record failure for %s: %w", ev.ID, err)
		}
		if ev.Attempts+1 >= d.cfg.MaxAttempts {
			return d.poison(ctx, ev.ID, now)
		}
		d.logger.Warn("event handler failed",
			slog.String("id", string(ev.ID)),
			slog.String("type", ev.Type),
			slog.Int("attempt", ev.Attempts+1),
			slog.String("err", err.Error()))
		return outcomeFailed, nil
	}
	if err := d.repo.MarkDelivered(ctx, ev.ID, now); err != nil {
		return outcomeFailed, fmt.Errorf("mark delivered %s: %w", ev.ID, err)
	}
	return outcomeDelivered, nil
}

// poison moves an exhausted event to poisoned and fires the hook. A
// conflicting update means another worker got there first: the winner's
// decision stands.
func (d *Dispatcher) poison(ctx context.Context, id event.EventID, now time.Time) (outcome, error) {
	if err := d.repo.MarkPoisoned(ctx, id, now); err != nil {
		if errors.Is(err, event.ErrProcessingConflict) {
			return outcomePoisoned, nil
		}
		return outcomePoisoned, fmt.Errorf("poison %s: %w", id, err)
	}
	if d.metrics.Poisoned != nil {
		d.metrics.Poisoned(id, d.cfg.MaxAttempts)
	}
	d.logger.Error("event poisoned after exhausting retries",
		slog.String("id", string(id)), slog.Int("attempts", d.cfg.MaxAttempts))
	return outcomePoisoned, nil
}

// backoff computes the delay before the given attempt: exponential from
// RetryBase, capped at RetryMax.
func (d *Dispatcher) backoff(attempt int, now time.Time) time.Time {
	delay := d.cfg.RetryBase
	for i := 1; i < attempt && delay < d.cfg.RetryMax; i++ {
		delay *= 2
	}
	if delay > d.cfg.RetryMax {
		delay = d.cfg.RetryMax
	}
	return now.Add(delay)
}

// checkReorg verifies the cursor's block still belongs to the canonical
// chain (§11.6). On a mismatch it invalidates the queued events from the
// cursor height on, rolls the cursor back one level (or to the start when
// the parent cannot be verified), and reports the new position for the
// poll that follows. The reorged flag distinguishes an actual rollback
// from an untouched position.
func (d *Dispatcher) checkReorg(ctx context.Context, src Source, cursor event.Cursor) (next event.Cursor, invalidated int, reorged bool, err error) {
	key := cursor.Key()
	if cursor.BlockHeight == 0 || cursor.BlockHash == "" {
		return cursor, 0, false, nil // nothing verified yet
	}
	hash, err := src.Head(ctx, cursor.BlockHeight)
	if errors.Is(err, event.ErrPositionUnknown) {
		return cursor, 0, false, nil // cannot verify: keep position
	}
	if err != nil {
		return cursor, 0, false, fmt.Errorf("head %d: %w", cursor.BlockHeight, err)
	}
	if hash == cursor.BlockHash {
		return cursor, 0, false, nil
	}

	// §11.6: the chain the cursor trusted no longer exists.
	invalidated, err = d.repo.MarkReorged(ctx, key, cursor.BlockHeight)
	if err != nil {
		return cursor, invalidated, true, fmt.Errorf("mark reorged from %d: %w", cursor.BlockHeight, err)
	}

	// §11.6 step 5: roll back to a safe position and rescan. The parent
	// block is the conservative target; when even it cannot be verified,
	// rescan from the start — identity deduplication makes that safe.
	target := cursor.BlockHeight - 1
	targetHash := ""
	if target > 0 {
		h, err := src.Head(ctx, target)
		if err != nil {
			target = 0
		} else {
			targetHash = h
		}
	}
	now := d.now.Now()
	next = event.Cursor{Network: key.Network, SourceType: key.SourceType, BlockHeight: target, BlockHash: targetHash, UpdatedAt: now}
	if err := d.repo.RollbackCursor(ctx, next); err != nil {
		return cursor, invalidated, true, fmt.Errorf("rollback cursor to %d: %w", target, err)
	}
	if d.metrics.Reorg != nil {
		d.metrics.Reorg(key, cursor, next, invalidated)
	}
	d.logger.Warn("reorg detected, cursor rolled back",
		slog.String("network", key.Network.String()),
		slog.String("source", string(key.SourceType)),
		slog.Uint64("from", cursor.BlockHeight),
		slog.Uint64("to", target),
		slog.Int("invalidated", invalidated))
	return next, invalidated, true, nil
}
