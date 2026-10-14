package event

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cuihairu/chame/core/identifiers"
)

// ErrEventNotFound is returned by read operations when no event with the
// given identity exists. A missing event is a query outcome, not a chain
// failure; use errors.Is to detect it.
var ErrEventNotFound = errors.New("event: record not found")

// ErrDuplicateEvent is returned when Create is called for an event whose
// identity already exists (§11.3). Delivery is at least once, so the
// store deduplicates and the caller skips: a re-poll never produces a
// second stored event, and replay of an existing record stays a no-op.
var ErrDuplicateEvent = errors.New("event: record already exists")

// ErrProcessingConflict is returned when a delivery update no longer
// matches the stored processing state: another worker or an earlier
// attempt already changed the record. The caller must re-read and decide
// again — the winner's decision stands.
var ErrProcessingConflict = errors.New("event: processing state conflict")

// ErrCursorNotFound is returned when no cursor has been saved for the
// (network, source) pair. A first poll starts from the beginning.
var ErrCursorNotFound = errors.New("event: cursor not found")

// ErrCursorRegression is returned when a cursor update would move the
// resume position backwards or replace the block hash at the same height
// (§11.5). Both cases mean the chain the cursor trusted no longer exists,
// i.e. a reorganization; reorg recovery goes through RollbackCursor, which
// records the rollback explicitly.
var ErrCursorRegression = errors.New("event: cursor regression")

// PendingQuery selects events ready for delivery (§11.4): queued events
// whose attempt is due, whose finality has reached the requirement, and
// whose block has not been reorganized. A zero Network or SourceType
// matches every network or source type, so one query can drain the whole
// store and one query can drain one source.
type PendingQuery struct {
	// NotBefore bounds NextAttemptAt: only events whose next attempt is
	// due are returned. A zero-time next attempt is always due.
	NotBefore time.Time
	// MinFinality gates delivery (§11.4): an event is returned only when
	// its finality rank is at least this state's rank.
	MinFinality FinalityState
	// Limit bounds the page; it must be positive so a bounded scan can
	// drain the queue fairly.
	Limit int
	// Network scopes the query; the zero value matches every network.
	Network identifiers.NetworkID
	// SourceType scopes the query; the zero value matches every source type.
	SourceType SourceType
}

// Validate checks the query shape.
func (q PendingQuery) Validate() error {
	if !q.MinFinality.Valid() {
		return fmt.Errorf("pending query: unknown finality state %q", q.MinFinality)
	}
	if q.Limit < 1 {
		return fmt.Errorf("pending query: limit must be positive")
	}
	zeroNetwork := identifiers.NetworkID{}
	if q.Network != zeroNetwork {
		if err := q.Network.Validate(); err != nil {
			return fmt.Errorf("pending query: %w", err)
		}
	}
	if q.SourceType != "" && !q.SourceType.Valid() {
		return fmt.Errorf("pending query: unknown source type %q", q.SourceType)
	}
	return nil
}

// CursorProgress reports the position of one cursor after a save.
type CursorProgress struct {
	// Height and Hash are the stored position.
	Height uint64
	Hash   string
}

// Repository persists event records and cursors (spec §11). Implementations
// must be safe for concurrent use and must enforce, atomically:
//
//   - identity uniqueness (§11.3): one record per event identity, so a
//     re-poll after a crash cannot store the same event twice and business
//     effects stay deduplicated at the source.
//   - conditional delivery updates (§11.4): MarkDelivered, RecordFailure
//     and MarkPoisoned compare the stored processing state, so two workers
//     cannot both report one event delivered and one failed.
//   - cursor monotonicity (§11.5): a cursor never moves backwards and the
//     block hash at a height is never silently replaced, so a reorg is
//     detected instead of swallowed. A rollback is always explicit.
//
// Data is opaque to the model. Durable implementations persist it by JSON
// encoding and return json.RawMessage on read; a JSON column may
// normalize the encoding (whitespace, object key order), so consumers
// decode the payload instead of comparing it byte for byte. Create never
// accepts a zero At, CreatedAt or UpdatedAt: pending scans and cursor
// resume depend on real timestamps.
type Repository interface {
	// Create inserts an event record. A record with the same identity
	// fails with ErrDuplicateEvent, whatever its content.
	Create(ctx context.Context, ev Event) error
	// Get returns the event with the given identity, or ErrEventNotFound.
	Get(ctx context.Context, id EventID) (Event, error)
	// FindPending returns ready events oldest-chain-position-first (§11.4):
	// queued, still observed on the canonical chain, finality at or above
	// MinFinality, attempt due, bounded by Limit.
	FindPending(ctx context.Context, q PendingQuery) ([]Event, error)
	// MarkDelivered moves a queued event to delivered and stamps UpdatedAt
	// to at. A record in any other state fails with ErrProcessingConflict,
	// including one already delivered.
	MarkDelivered(ctx context.Context, id EventID, at time.Time) error
	// RecordFailure keeps a queued event queued, increments Attempts and
	// defers its next attempt to nextAttempt. A record in any other state
	// fails with ErrProcessingConflict.
	RecordFailure(ctx context.Context, id EventID, nextAttempt, at time.Time) error
	// MarkPoisoned moves a queued event to poisoned after the retry budget
	// is exhausted (§11.4: failed events must stay visible, not vanish).
	// A record in any other state fails with ErrProcessingConflict.
	MarkPoisoned(ctx context.Context, id EventID, at time.Time) error
	// SetFinality updates the finality state of an event that is still
	// queued, so delivery can start as soon as confirmation is reached
	// (§11.4). A delivered, reorged or poisoned event fails with
	// ErrProcessingConflict.
	SetFinality(ctx context.Context, id EventID, f FinalityState, at time.Time) error
	// MarkReorged invalidates the queued events of one (network, source)
	// at blockHeight or later (§11.6 steps 2 and 3): they are marked
	// reorged and never delivered. Delivered events are left alone — their
	// business compensation is the application's decision (§11.6). It
	// returns how many events were invalidated.
	MarkReorged(ctx context.Context, key CursorKey, blockHeight uint64) (int, error)
	// GetCursor returns the resume position of one (network, source) pair,
	// or ErrCursorNotFound when nothing has been saved yet.
	GetCursor(ctx context.Context, key CursorKey) (Cursor, error)
	// SaveCursor advances the cursor. It fails with ErrCursorRegression on
	// a backwards height or a different hash at the same height.
	SaveCursor(ctx context.Context, c Cursor) error
	// RollbackCursor moves the cursor to an earlier canonical position and
	// records the rollback (§11.6 steps 5 and 6): RollbackCount increments
	// and LastRollback stamps the time, so the audit trail stays in the
	// store. It skips the monotonicity check.
	RollbackCursor(ctx context.Context, c Cursor) error
}
