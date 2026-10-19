package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cuihairu/chame/capability/event"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// eventColumns lists the event record columns, in scan order.
const eventColumns = `id, network_family, network_name, network_chain_id, source_type,
transaction_id, block_hash, block_height, event_index, locator,
event_type, data, decode_version, observed_at, observation, finality, processing,
attempts, next_attempt_at, created_at, updated_at`

// EventStore is a PostgreSQL-backed event.Repository (spec §11, ADR-0002).
// It is a separate type from Store because both repositories declare a
// Create method, which Go does not allow on one type. It shares the
// connection pool: construct with sql.Open plus NewEventStore, or share a
// *sql.DB. Safe for concurrent use; all coordination between instances
// happens through the database.
type EventStore struct {
	db *sql.DB
}

// NewEventStore wraps an existing *sql.DB (it must use the pgx driver).
// Apply Store.Migrate before first use.
func NewEventStore(db *sql.DB) *EventStore {
	return &EventStore{db: db}
}

// Create implements event.Repository: identity uniqueness (§11.3). The
// insert targets the primary key, so a replay of an already stored event
// is a no-op that returns ErrDuplicateEvent, not a second record.
func (s *EventStore) Create(ctx context.Context, ev event.Event) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	data, err := encodeEventData(ev.Data)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO chame_events (`+eventColumns+`)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (id) DO NOTHING`, eventInsertArgs(ev, data)...)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.create", "%v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.create", "%v", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", event.ErrDuplicateEvent, ev.ID)
	}
	return nil
}

// Get implements event.Repository.
func (s *EventStore) Get(ctx context.Context, id event.EventID) (event.Event, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+eventColumns+` FROM chame_events WHERE id = $1`, id)
	return scanEvent(row, string(id))
}

// ReplayEvents returns retained raw evidence through a saved cursor, including
// delivered/reorged rows; projection policy decides which observations apply.
func (s *EventStore) ReplayEvents(ctx context.Context, key event.CursorKey, through event.Cursor) ([]event.Event, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+eventColumns+` FROM chame_events
WHERE network_family=$1 AND network_name=$2 AND network_chain_id=$3 AND source_type=$4 AND block_height <= $5
ORDER BY block_height ASC, event_index ASC, id ASC`, key.Network.Family, key.Network.Name, key.Network.ChainID, key.SourceType, through.BlockHeight)
	if err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "event.sql.replay", "%v", err)
	}
	defer func() { _ = rows.Close() }()
	var out []event.Event
	for rows.Next() {
		ev, err := scanEvent(rows, "")
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "event.sql.replay", "%v", err)
	}
	return out, nil
}

// FindPending implements event.Repository: ready events in chain order,
// scoped per source when asked (§11.4).
func (s *EventStore) FindPending(ctx context.Context, q event.PendingQuery) ([]event.Event, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	minRank := q.MinFinality.Rank()
	finality := make([]string, 0, 3)
	for _, f := range []event.FinalityState{event.FinalityPending, event.FinalityConfirmed, event.FinalityFinal} {
		if f.Rank() >= minRank {
			finality = append(finality, string(f))
		}
	}
	// Finality states are framework identifiers without commas, so a
	// joined parameter keeps the query static.
	query := `SELECT ` + eventColumns + ` FROM chame_events
WHERE processing = 'queued' AND observation = 'observed'
  AND finality = ANY (string_to_array($1, ','))
  AND (next_attempt_at IS NULL OR next_attempt_at <= $2)`
	args := []any{strings.Join(finality, ","), q.NotBefore}
	n := 3
	zeroNetwork := identifiers.NetworkID{}
	if q.Network != zeroNetwork {
		query += ` AND network_family = $3 AND network_name = $4 AND network_chain_id = $5`
		args = append(args, q.Network.Family, q.Network.Name, q.Network.ChainID)
		n += 3
	}
	if q.SourceType != "" {
		query += fmt.Sprintf(` AND source_type = $%d`, n)
		args = append(args, q.SourceType)
		n++
	}
	query += fmt.Sprintf(` ORDER BY block_height ASC, event_index ASC, created_at ASC, id ASC LIMIT $%d`, n)
	args = append(args, q.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "event.sql.find_pending", "%v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []event.Event
	for rows.Next() {
		ev, err := scanEvent(rows, "")
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "event.sql.find_pending", "%v", err)
	}
	return out, nil
}

// casProcessing applies a queued-only update. A missing event fails with
// ErrEventNotFound; a record in any other state fails with
// ErrProcessingConflict (§11.4 conditional updates).
func (s *EventStore) casProcessing(ctx context.Context, id event.EventID, set string, at time.Time, args ...any) error {
	query := `UPDATE chame_events SET ` + set + `, updated_at = $1 WHERE id = $2 AND processing = 'queued'`
	execArgs := append([]any{at, id}, args...)
	res, err := s.db.ExecContext(ctx, query, execArgs...)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.cas", "%v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.cas", "%v", err)
	}
	if n == 1 {
		return nil
	}
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("%w: event %s is not queued", event.ErrProcessingConflict, id)
}

// MarkDelivered implements event.Repository.
func (s *EventStore) MarkDelivered(ctx context.Context, id event.EventID, at time.Time) error {
	return s.casProcessing(ctx, id, "processing = 'delivered'", at)
}

// RecordFailure implements event.Repository: the attempt count grows in
// the store so two workers cannot lose one another's accounting.
func (s *EventStore) RecordFailure(ctx context.Context, id event.EventID, nextAttempt, at time.Time) error {
	return s.casProcessing(ctx, id, `attempts = attempts + 1, next_attempt_at = $3`, at, nextAttempt)
}

// MarkPoisoned implements event.Repository (§11.4: a permanently failing
// event stays visible, not silent).
func (s *EventStore) MarkPoisoned(ctx context.Context, id event.EventID, at time.Time) error {
	return s.casProcessing(ctx, id, "processing = 'poisoned'", at)
}

// SetFinality implements event.Repository.
func (s *EventStore) SetFinality(ctx context.Context, id event.EventID, f event.FinalityState, at time.Time) error {
	if !f.Valid() {
		return fmt.Errorf("event: unknown finality state %q", f)
	}
	return s.casProcessing(ctx, id, "finality = $3", at, string(f))
}

// MarkReorged implements event.Repository (§11.6 steps 2 and 3): queued
// events of one source at blockHeight or later are invalidated; delivered
// events stay terminal. Timestamps are left alone: they describe the
// event's own lifecycle, not the reorg marker.
func (s *EventStore) MarkReorged(ctx context.Context, key event.CursorKey, blockHeight uint64) (int, error) {
	if err := key.Validate(); err != nil {
		return 0, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE chame_events
SET observation = 'reorged', processing = 'reorged'
WHERE network_family = $1 AND network_name = $2 AND network_chain_id = $3
  AND source_type = $4 AND block_height >= $5 AND processing = 'queued'`,
		key.Network.Family, key.Network.Name, key.Network.ChainID, key.SourceType, blockHeight)
	if err != nil {
		return 0, cherrs.New(cherrs.PersistenceFailure, "event.sql.mark_reorged", "%v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, cherrs.New(cherrs.PersistenceFailure, "event.sql.mark_reorged", "%v", err)
	}
	return int(n), nil
}

// GetCursor implements event.Repository.
func (s *EventStore) GetCursor(ctx context.Context, key event.CursorKey) (event.Cursor, error) {
	if err := key.Validate(); err != nil {
		return event.Cursor{}, err
	}
	var c event.Cursor
	var height int64
	var lastRollback sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT block_height, block_hash, rollback_count, last_rollback, updated_at
FROM chame_event_cursors
WHERE network_family = $1 AND network_name = $2 AND network_chain_id = $3 AND source_type = $4`,
		key.Network.Family, key.Network.Name, key.Network.ChainID, key.SourceType,
	).Scan(&height, &c.BlockHash, &c.RollbackCount, &lastRollback, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return event.Cursor{}, fmt.Errorf("%w: %s %s", event.ErrCursorNotFound, key.Network, key.SourceType)
		}
		return event.Cursor{}, cherrs.New(cherrs.PersistenceFailure, "event.sql.get_cursor", "%v", err)
	}
	c.Network = key.Network
	c.SourceType = key.SourceType
	c.BlockHeight = uint64(height)
	c.LastRollback = lastRollback.Time
	return c, nil
}

// SaveCursor implements event.Repository: monotonicity guard (§11.5). The
// check happens on the read view and again in the conditional upsert, so
// a concurrent advance still surfaces as a regression. The audit trail is
// store-owned and preserved.
func (s *EventStore) SaveCursor(ctx context.Context, c event.Cursor) error {
	if err := c.Key().Validate(); err != nil {
		return err
	}
	stored, err := s.GetCursor(ctx, c.Key())
	if err != nil && !errors.Is(err, event.ErrCursorNotFound) {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.save_cursor", "%v", err)
	}
	if err == nil {
		if c.BlockHeight < stored.BlockHeight {
			return fmt.Errorf("%w: height %d is below stored %d",
				event.ErrCursorRegression, c.BlockHeight, stored.BlockHeight)
		}
		if c.BlockHeight == stored.BlockHeight && c.BlockHash != "" && stored.BlockHash != "" && c.BlockHash != stored.BlockHash {
			return fmt.Errorf("%w: height %d already holds block %s, not %s",
				event.ErrCursorRegression, c.BlockHeight, stored.BlockHash, c.BlockHash)
		}
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO chame_event_cursors
(network_family, network_name, network_chain_id, source_type, block_height, block_hash, rollback_count, last_rollback, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (network_family, network_name, network_chain_id, source_type)
DO UPDATE SET block_height = EXCLUDED.block_height, block_hash = EXCLUDED.block_hash, updated_at = EXCLUDED.updated_at
WHERE chame_event_cursors.block_height < EXCLUDED.block_height
   OR (chame_event_cursors.block_height = EXCLUDED.block_height
       AND (chame_event_cursors.block_hash = EXCLUDED.block_hash
            OR chame_event_cursors.block_hash = ''
            OR EXCLUDED.block_hash = ''))`,
		c.Network.Family, c.Network.Name, c.Network.ChainID, c.SourceType, c.BlockHeight, c.BlockHash,
		c.RollbackCount, nullableTime(c.LastRollback), c.UpdatedAt)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.save_cursor", "%v", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.save_cursor", "%v", err)
	} else if n == 0 {
		return fmt.Errorf("%w: the cursor moved concurrently", event.ErrCursorRegression)
	}
	return nil
}

// RollbackCursor implements event.Repository (§11.6 steps 5 and 6): the
// cursor moves to an earlier canonical position and the rollback is
// recorded in the same statement, so the audit trail cannot be lost.
func (s *EventStore) RollbackCursor(ctx context.Context, c event.Cursor) error {
	if err := c.Key().Validate(); err != nil {
		return err
	}
	lastRollback := nullableTime(c.LastRollback)
	if lastRollback == nil {
		lastRollback = c.UpdatedAt // audit timestamp, stored-owned
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO chame_event_cursors
(network_family, network_name, network_chain_id, source_type, block_height, block_hash, rollback_count, last_rollback, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (network_family, network_name, network_chain_id, source_type)
DO UPDATE SET block_height = EXCLUDED.block_height, block_hash = EXCLUDED.block_hash,
    rollback_count = chame_event_cursors.rollback_count + 1,
    last_rollback = EXCLUDED.last_rollback, updated_at = EXCLUDED.updated_at`,
		c.Network.Family, c.Network.Name, c.Network.ChainID, c.SourceType, c.BlockHeight, c.BlockHash,
		c.RollbackCount, lastRollback, c.UpdatedAt)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "event.sql.rollback_cursor", "%v", err)
	}
	return nil
}

// nullableTime maps a zero time to NULL, which is how the store keeps
// unset audit and retry timestamps.
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// eventInsertArgs flattens an event into insert parameter order.
func eventInsertArgs(ev event.Event, data any) []any {
	return []any{
		ev.ID,
		ev.Network.Family, ev.Network.Name, ev.Network.ChainID,
		ev.SourceType,
		ev.TransactionID, ev.BlockHash, ev.BlockHeight, ev.Index, ev.Locator,
		ev.Type, data, ev.DecodeVersion, ev.At,
		ev.Observation, ev.Finality, ev.Processing,
		ev.Attempts, nullableTime(ev.NextAttemptAt),
		ev.CreatedAt, ev.UpdatedAt,
	}
}

// encodeEventData prepares the opaque payload: nil stays NULL,
// everything else must JSON-encode (durable stores persist by encoding).
func encodeEventData(v json.RawMessage) (any, error) {
	if v == nil {
		return nil, nil
	}
	if !json.Valid(v) {
		return nil, cherrs.New(cherrs.PersistenceFailure, "event.sql.create",
			"event data is not valid JSON")
	}
	return json.RawMessage(v), nil
}

// scanEvent reads one event row into the shared model. label names the
// lookup in the not-found error.
func scanEvent(row scanner, label string) (event.Event, error) {
	var ev event.Event
	var height, index int64
	var data []byte
	var nextAttempt sql.NullTime
	err := row.Scan(
		&ev.ID,
		&ev.Network.Family, &ev.Network.Name, &ev.Network.ChainID,
		&ev.SourceType,
		&ev.TransactionID, &ev.BlockHash, &height, &index, &ev.Locator,
		&ev.Type, &data, &ev.DecodeVersion, &ev.At,
		&ev.Observation, &ev.Finality, &ev.Processing,
		&ev.Attempts, &nextAttempt,
		&ev.CreatedAt, &ev.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return event.Event{}, fmt.Errorf("%w: %s", event.ErrEventNotFound, label)
		}
		return event.Event{}, cherrs.New(cherrs.PersistenceFailure, "event.sql.scan", "%v", err)
	}
	ev.BlockHeight = uint64(height)
	ev.Index = uint64(index)
	if len(data) > 0 {
		ev.Data = json.RawMessage(data)
	}
	ev.NextAttemptAt = nextAttempt.Time
	return ev, nil
}

var _ event.Repository = (*EventStore)(nil)
