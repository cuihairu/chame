// Package sql provides the PostgreSQL persistence implementation for
// transaction and event records (spec §10.5, §11, ADR-0002): database/sql
// with the pgx stdlib driver, explicit versioned migrations embedded from
// schema/, and the idempotency, state-machine, lease, and cursor
// semantics enforced in SQL so multiple application instances coordinate
// through the database.
package sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// driverName is the database/sql driver the store opens with.
const driverName = "pgx"

// txColumns lists the record columns, in scan order.
const txColumns = `id, operation_id,
network_family, network_name, network_chain_id,
status,
idempotency_application, idempotency_operation_type, idempotency_key, request_hash,
sender_family, sender_value, recipient_family, recipient_value,
amount_raw, amount_decimals, amount_asset,
chain_hash, chain_data,
created_at, updated_at`

// Store is a PostgreSQL-backed transaction.Repository (ADR-0002). It is
// safe for concurrent use; all coordination between instances happens
// through the database. Construct with Open (or New around an existing
// *sql.DB) and apply Migrate before first use.
type Store struct {
	db *sql.DB
}

// Open connects to the given PostgreSQL DSN with the pgx stdlib driver.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, cherrs.New(cherrs.InvalidConfiguration, "transaction.sql.open", "%v", err)
	}
	db.SetMaxOpenConns(16)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, cherrs.New(cherrs.NetworkUnavailable, "transaction.sql.ping", "%v", err)
	}
	return &Store{db: db}, nil
}

// New wraps an existing *sql.DB (it must use the pgx driver).
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB exposes the underlying pool for host concerns such as health checks
// and reporting queries.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the underlying pool.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// migrationTable tracks applied schema versions (ADR-0002: explicit,
// versioned SQL, no ORM).
const migrationTable = `CREATE TABLE IF NOT EXISTS chame_schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Migrate applies every embedded migration not yet recorded, in
// filename order, each in its own transaction. It is idempotent and safe
// to call on every start.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, migrationTable); err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.migrate", "%v", err)
	}
	for _, m := range migrations {
		if err := s.applyMigration(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one migration inside a transaction, recorded in
// chame_schema_migrations so a partial application never repeats.
func (s *Store) applyMigration(ctx context.Context, m migration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.migrate", "%v", err)
	}
	defer func() { _ = tx.Rollback() }()

	var applied bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM chame_schema_migrations WHERE version = $1)`, m.version,
	).Scan(&applied); err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.migrate", "%v", err)
	}
	if applied {
		return nil
	}
	if _, err := tx.ExecContext(ctx, m.body); err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.migrate", "version %s: %v", m.version, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO chame_schema_migrations (version) VALUES ($1)`, m.version,
	); err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.migrate", "%v", err)
	}
	return tx.Commit()
}

// Create implements transaction.Repository. The insert targets the
// idempotency index: a duplicate key with the same request hash is a
// no-op (the first operation stands, §10.1), a duplicate with a
// different hash conflicts. A keyless duplicate id is a persistence
// failure.
func (s *Store) Create(ctx context.Context, tx transaction.Transaction) error {
	if err := transaction.ValidateRecord(tx); err != nil {
		return err
	}
	data, err := encodeChainData(tx.ChainData)
	if err != nil {
		return err
	}

	const keyed = `INSERT INTO chame_transactions (` + txColumns + `)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (idempotency_application, idempotency_operation_type, idempotency_key)
     WHERE idempotency_key <> ''
DO NOTHING
RETURNING id`

	const keyless = `INSERT INTO chame_transactions (` + txColumns + `)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
ON CONFLICT (id)
DO NOTHING
RETURNING id`

	args := insertArgs(tx, data)
	query := keyless
	if tx.IdempotencyKey != "" {
		query = keyed
	}
	var returned string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&returned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if tx.IdempotencyKey == "" {
				return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.create",
					"transaction %s already exists", tx.ID)
			}
			return s.resolveKeyedCreate(ctx, tx)
		}
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.create", "%v", err)
	}
	return nil
}

// resolveKeyedCreate handles the lost insert race: re-read the record
// holding the key and apply the §10.1 request-hash rule.
func (s *Store) resolveKeyedCreate(ctx context.Context, tx transaction.Transaction) error {
	existing, err := s.FindByIdempotencyKey(ctx, tx.IdempotencyScope, tx.IdempotencyKey)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.create", "%v", err)
	}
	if existing.RequestHash != tx.RequestHash {
		return transaction.IdempotencyConflict(tx.IdempotencyScope, tx.IdempotencyKey)
	}
	return nil // same request: the stored record is the operation
}

// Get implements transaction.Repository.
func (s *Store) Get(ctx context.Context, id transaction.TransactionID) (transaction.Transaction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+txColumns+` FROM chame_transactions WHERE id = $1`, id)
	return scanTx(row, string(id))
}

// FindByIdempotencyKey implements transaction.Repository.
func (s *Store) FindByIdempotencyKey(ctx context.Context, scope transaction.IdempotencyScope, key string) (transaction.Transaction, error) {
	if err := scope.Validate(); err != nil {
		return transaction.Transaction{}, err
	}
	if key == "" {
		return transaction.Transaction{}, fmt.Errorf("idempotency key must not be empty")
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT `+txColumns+` FROM chame_transactions
WHERE idempotency_application = $1 AND idempotency_operation_type = $2 AND idempotency_key = $3`,
		scope.Application, scope.OperationType, key)
	return scanTx(row, fmt.Sprintf("key %q in scope %s", key, scope))
}

// UpdateStatus implements transaction.Repository: the matrix is checked
// on the read view, then applied as a compare-and-swap so a concurrent
// advance surfaces as ErrStatusConflict, never a contradictory state.
func (s *Store) UpdateStatus(ctx context.Context, id transaction.TransactionID, expected, next transaction.TransactionStatus, at time.Time) error {
	current, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != expected {
		return fmt.Errorf("%w: transaction %s is %s, expected %s",
			transaction.ErrStatusConflict, id, current.Status, expected)
	}
	if !transaction.CanTransition(expected, next) {
		return fmt.Errorf("%w: %s -> %s on transaction %s",
			transaction.ErrIllegalTransition, expected, next, id)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE chame_transactions SET status = $1, updated_at = $2 WHERE id = $3 AND status = $4`,
		next, at, id, expected)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.update_status", "%v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.update_status", "%v", err)
		}
		return fmt.Errorf("%w: transaction %s is no longer %s", transaction.ErrStatusConflict, id, expected)
	}
	return nil
}

// Attach implements transaction.Repository: the matrix is checked on
// the read view, then status, chain reference, and chain data move in
// one compare-and-swap (§10.2) — a submitted record can never lack the
// reference that names it.
func (s *Store) Attach(ctx context.Context, id transaction.TransactionID, expected, next transaction.TransactionStatus, chainHash string, chainData any, at time.Time) error {
	current, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != expected {
		return fmt.Errorf("%w: transaction %s is %s, expected %s",
			transaction.ErrStatusConflict, id, current.Status, expected)
	}
	if !transaction.CanTransition(expected, next) {
		return fmt.Errorf("%w: %s -> %s on transaction %s",
			transaction.ErrIllegalTransition, expected, next, id)
	}
	data, err := encodeChainData(chainData)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE chame_transactions
SET status = $1, chain_hash = $2, chain_data = $3, updated_at = $4
WHERE id = $5 AND status = $6`,
		next, chainHash, data, at, id, expected)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.attach", "%v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.attach", "%v", err)
		}
		return fmt.Errorf("%w: transaction %s is no longer %s", transaction.ErrStatusConflict, id, expected)
	}
	return nil
}

// FindStale implements transaction.Repository: matching records,
// oldest-first, deterministically tie-broken by id.
func (s *Store) FindStale(ctx context.Context, q transaction.StaleQuery) ([]transaction.Transaction, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	statuses := make([]string, len(q.Statuses))
	for i, st := range q.Statuses {
		statuses[i] = string(st)
	}
	// Statuses are framework identifiers without commas, so a joined
	// parameter keeps the query static.
	joined := strings.Join(statuses, ",")
	query := `SELECT ` + txColumns + ` FROM chame_transactions
WHERE status = ANY (string_to_array($1, ',')) AND updated_at < $2
ORDER BY updated_at ASC, id ASC`
	args := []any{joined, q.OlderThan}
	if q.Limit > 0 {
		query += ` LIMIT $3`
		args = append(args, q.Limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "transaction.sql.find_stale", "%v", err)
	}
	defer func() { _ = rows.Close() }()

	var out []transaction.Transaction
	for rows.Next() {
		tx, err := scanTx(rows, "")
		if err != nil {
			return nil, err
		}
		out = append(out, tx)
	}
	if err := rows.Err(); err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "transaction.sql.find_stale", "%v", err)
	}
	return out, nil
}

// Claim implements transaction.Repository. The conditional update grants
// the lease when it is free, expired at or before the claim instant, or
// already the caller's own (renewal) — all atomically in one statement.
func (s *Store) Claim(ctx context.Context, id transaction.TransactionID, lease transaction.Lease) (bool, error) {
	if err := lease.Validate(); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE chame_transactions
SET lease_holder = $1, lease_expires_at = $2
WHERE id = $3
  AND (lease_holder = $1 OR lease_holder = '' OR lease_expires_at IS NULL OR lease_expires_at <= $4)`,
		lease.Holder, lease.ExpiresAt, id, lease.At)
	if err != nil {
		return false, cherrs.New(cherrs.PersistenceFailure, "transaction.sql.claim", "%v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, cherrs.New(cherrs.PersistenceFailure, "transaction.sql.claim", "%v", err)
	}
	if n == 1 {
		return true, nil
	}
	// Refused or missing: distinguish for the contract.
	if _, err := s.Get(ctx, id); err != nil {
		return false, err
	}
	return false, nil
}

// Release implements transaction.Repository: owner-only; a stale worker
// releasing after a takeover is a no-op, not an error.
func (s *Store) Release(ctx context.Context, id transaction.TransactionID, holder string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE chame_transactions
SET lease_holder = '', lease_expires_at = NULL
WHERE id = $1 AND lease_holder = $2`, id, holder)
	if err != nil {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.release", "%v", err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err != nil {
			return cherrs.New(cherrs.PersistenceFailure, "transaction.sql.release", "%v", err)
		}
		if _, err := s.Get(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// insertArgs flattens a record into insert parameter order.
func insertArgs(tx transaction.Transaction, chainData any) []any {
	return []any{
		tx.ID,
		tx.OperationID,
		tx.Network.Family,
		tx.Network.Name,
		tx.Network.ChainID,
		tx.Status,
		tx.IdempotencyScope.Application,
		tx.IdempotencyScope.OperationType,
		tx.IdempotencyKey,
		tx.RequestHash,
		tx.Sender.Family,
		tx.Sender.Value,
		tx.Recipient.Family,
		tx.Recipient.Value,
		tx.Amount.Raw,
		int(tx.Amount.Decimals),
		tx.Amount.AssetID,
		tx.ChainHash,
		chainData,
		tx.CreatedAt,
		tx.UpdatedAt,
	}
}

// encodeChainData prepares the opaque payload: nil stays NULL,
// everything else must JSON-encode (durable stores persist by encoding).
func encodeChainData(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, cherrs.New(cherrs.PersistenceFailure, "transaction.sql.create",
			"chain data is not encodable: %v", err)
	}
	return raw, nil
}

// scanner abstracts *sql.Row and *sql.Rows for one record scan.
type scanner interface{ Scan(dest ...any) error }

// scanTx reads one record row into the shared model. label names the
// lookup in the not-found error.
func scanTx(row scanner, label string) (transaction.Transaction, error) {
	var tx transaction.Transaction
	var chainData []byte
	var decimals int
	err := row.Scan(
		&tx.ID,
		&tx.OperationID,
		&tx.Network.Family,
		&tx.Network.Name,
		&tx.Network.ChainID,
		&tx.Status,
		&tx.IdempotencyScope.Application,
		&tx.IdempotencyScope.OperationType,
		&tx.IdempotencyKey,
		&tx.RequestHash,
		&tx.Sender.Family,
		&tx.Sender.Value,
		&tx.Recipient.Family,
		&tx.Recipient.Value,
		&tx.Amount.Raw,
		&decimals,
		&tx.Amount.AssetID,
		&tx.ChainHash,
		&chainData,
		&tx.CreatedAt,
		&tx.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return transaction.Transaction{}, fmt.Errorf("%w: %s", transaction.ErrRecordNotFound, label)
		}
		return transaction.Transaction{}, cherrs.New(cherrs.PersistenceFailure, "transaction.sql.scan", "%v", err)
	}
	tx.Amount.Decimals = uint8(decimals)
	if len(chainData) > 0 {
		tx.ChainData = json.RawMessage(chainData)
	}
	return tx, nil
}

var _ transaction.Repository = (*Store)(nil)
