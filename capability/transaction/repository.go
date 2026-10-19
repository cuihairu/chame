package transaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// ErrRecordNotFound is returned by read operations when no record with
// the given identity or idempotency key exists. A missing record is a
// query outcome, not a chain failure; use errors.Is to detect it.
var ErrRecordNotFound = errors.New("transaction: record not found")

// ErrStatusConflict is returned when the stored status no longer matches
// the expected one: another worker advanced the record first (spec
// §10.2). The caller must re-read the record and decide again — the
// winner's decision stands.
var ErrStatusConflict = errors.New("transaction: status conflict")

// IdempotencyScope scopes an idempotency key (spec §10.1): the key is
// unique within Application/Tenant + OperationType. Two applications
// reusing one key string never collide, and neither do two operation
// types inside one application.
type IdempotencyScope struct {
	// Application is the application or tenant the key belongs to.
	Application string
	// OperationType is the business operation kind (payout, deposit
	// settle, ...).
	OperationType string
}

// Validate requires both parts of the scope.
func (s IdempotencyScope) Validate() error {
	if s.Application == "" {
		return fmt.Errorf("idempotency scope: application must not be empty")
	}
	if s.OperationType == "" {
		return fmt.Errorf("idempotency scope: operation type must not be empty")
	}
	return nil
}

// String renders "application/operationType" for logs and errors.
func (s IdempotencyScope) String() string {
	return s.Application + "/" + s.OperationType
}

// StaleQuery selects candidate transactions for reconciliation (spec
// §10.4). A record matches when its status is one of Statuses and its
// UpdatedAt is older than OlderThan.
type StaleQuery struct {
	// Statuses selects which statuses count as stuck: prepared too long,
	// submitted without observation, unknown, reorged, ...
	Statuses []TransactionStatus
	// OlderThan bounds the record's UpdatedAt: only records untouched
	// since this instant are candidates.
	OlderThan time.Time
	// Limit bounds the page size; zero means no explicit limit. Results
	// are ordered oldest-first so a bounded scan drains fairly.
	Limit int
}

// Validate checks the query shape.
func (q StaleQuery) Validate() error {
	if len(q.Statuses) == 0 {
		return fmt.Errorf("stale query: at least one status is required")
	}
	for _, s := range q.Statuses {
		if !s.Valid() {
			return fmt.Errorf("stale query: unknown status %q", s)
		}
	}
	if q.Limit < 0 {
		return fmt.Errorf("stale query: limit must not be negative")
	}
	return nil
}

// Lease is one worker's reconciliation claim (spec §10.4). A claim
// expires, so a crashed worker cannot block recovery forever; the same
// holder may renew its own claim.
type Lease struct {
	// Holder identifies the claiming worker (instance id, worker name).
	Holder string
	// At is the instant the claim is made, in the caller's clock. The
	// store judges an existing lease expired when its expiry is not after
	// At — the store stays clock-free and tests stay deterministic.
	At time.Time
	// ExpiresAt is when the new claim lapses. Must be after At.
	ExpiresAt time.Time
}

// Validate requires a holder and a sane time window.
func (l Lease) Validate() error {
	if l.Holder == "" {
		return fmt.Errorf("lease: holder must not be empty")
	}
	if l.At.IsZero() {
		return fmt.Errorf("lease: claim instant must be set")
	}
	if !l.ExpiresAt.After(l.At) {
		return fmt.Errorf("lease: expiry must be after the claim instant")
	}
	return nil
}

// Repository persists transaction records (spec §10.2). Implementations
// must be safe for concurrent use and must enforce, atomically:
//
//   - idempotency uniqueness (§10.1): one record per (scope, key). A
//     Create whose key already exists with the same RequestHash returns
//     nil and keeps the existing record — a duplicate request never
//     creates a second operation, including under races. The same key
//     with a different RequestHash fails with an error matching
//     cherrs.ErrIdempotencyConflict.
//   - conditional status updates (§10.2): UpdateStatus compares the
//     stored status against expected and applies the state-machine
//     matrix, so concurrent workers cannot drive one record into
//     contradictory states.
//   - chain fields with their status (§10.2): Attach writes the chain
//     reference and chain data in the same atomic step as the advance,
//     so a record's reference and its status can never disagree.
//   - leases (§10.4): at most one holder claims a record at a time, with
//     expiry and owner-only release, so multiple workers reconcile
//     without stepping on each other.
//
// ChainData is opaque to the model. Durable implementations persist it by
// JSON encoding and return json.RawMessage on read; a value that cannot
// be encoded fails with a persistence failure. Create never accepts a
// zero CreatedAt or UpdatedAt: staleness scanning depends on real
// timestamps.
type Repository interface {
	// Create inserts a record and atomically reports whether this call
	// inserted it or found the same keyed request already stored. With an
	// idempotency key it applies the uniqueness rules above; without one it
	// inserts plainly. A duplicate ID without a key is a persistence failure.
	Create(ctx context.Context, tx Transaction) (CreateResult, error)
	// Get returns the record with the given ID, or ErrRecordNotFound.
	Get(ctx context.Context, id TransactionID) (Transaction, error)
	// FindByIdempotencyKey returns the record holding the key within the
	// scope, or ErrRecordNotFound.
	FindByIdempotencyKey(ctx context.Context, scope IdempotencyScope, key string) (Transaction, error)
	// UpdateStatus moves a record from expected to next when the stored
	// status is still expected and the transition is legal (CanTransition).
	// It fails with ErrStatusConflict on a stale expectation and with
	// ErrIllegalTransition on a matrix violation; success stamps UpdatedAt
	// to at.
	UpdateStatus(ctx context.Context, id TransactionID, expected, next TransactionStatus, at time.Time) error
	// Attach moves a record from expected to next and writes its chain
	// fields in the same atomic step (§10.2). The chain reference and the
	// adapter-owned chain data belong to whoever advances the record: the
	// flow that submits writes the reference in the same write as the
	// status, so a crash can never leave a submitted record without its
	// chain hash, and a prepared record keeps the data its recovery needs.
	// chainHash is the reference the record holds after the move — empty
	// while nothing has been broadcast, the chain's own identifier once it
	// has. chainData replaces the stored payload wholesale; a nil argument
	// clears it. The same conditional rules as UpdateStatus apply:
	// ErrStatusConflict on a stale expectation, ErrIllegalTransition on a
	// matrix violation, UpdatedAt stamped to at on success.
	Attach(ctx context.Context, id TransactionID, expected, next TransactionStatus, chainHash string, chainData any, at time.Time) error
	// FindStale returns matching records oldest-first (§10.4 scan).
	FindStale(ctx context.Context, q StaleQuery) ([]Transaction, error)
	// Claim attempts to lease the record to lease.Holder. It reports
	// success when the record is unclaimed, the previous lease expired at
	// or before lease.At, or the previous lease belongs to the same
	// holder (renewal). A missing record fails with ErrRecordNotFound.
	Claim(ctx context.Context, id TransactionID, lease Lease) (bool, error)
	// Release drops the lease when it belongs to holder. A lease held by
	// someone else is left alone — the stale worker releasing after a
	// takeover is a normal event, not an error. A missing record fails
	// with ErrRecordNotFound.
	Release(ctx context.Context, id TransactionID, holder string) error
}

// CreateResult reports whether this call inserted the transaction or found
// an existing transaction with the same idempotency key and request hash.
type CreateResult uint8

const (
	// CreateInserted means this call inserted the record.
	CreateInserted CreateResult = iota + 1
	// CreateSameKeyExisting means a record with the same key and request
	// hash already existed; that stored record remains authoritative.
	CreateSameKeyExisting
)

// IdempotencyConflict returns the canonical error for a key reuse with a
// different request (§10.1). Implementations return it so callers match
// cherrs.ErrIdempotencyConflict uniformly.
func IdempotencyConflict(scope IdempotencyScope, key string) error {
	return cherrs.New(cherrs.IdempotencyConflict, "transaction.repository",
		"idempotency key %q in scope %s already used by a different request", key, scope)
}

// ValidateRecord checks the record shape every implementation stores.
// It is part of the contract so memory and SQL stores reject the same
// inputs.
func ValidateRecord(tx Transaction) error {
	if tx.ID == "" {
		return fmt.Errorf("transaction: id must not be empty")
	}
	if err := tx.Network.Validate(); err != nil {
		return fmt.Errorf("transaction %s: %w", tx.ID, err)
	}
	if !tx.Status.Valid() {
		return fmt.Errorf("transaction %s: unknown status %q", tx.ID, tx.Status)
	}
	if tx.IdempotencyKey != "" {
		if tx.RequestHash == "" {
			return fmt.Errorf("transaction %s: idempotency key %q requires a request hash (§10.1)",
				tx.ID, tx.IdempotencyKey)
		}
		if err := tx.IdempotencyScope.Validate(); err != nil {
			return fmt.Errorf("transaction %s: %w", tx.ID, err)
		}
	}
	if tx.Amount.Raw != "" || tx.Amount.AssetID != "" {
		if err := tx.Amount.Validate(); err != nil {
			return fmt.Errorf("transaction %s: %w", tx.ID, err)
		}
	}
	if tx.CreatedAt.IsZero() || tx.UpdatedAt.IsZero() {
		return fmt.Errorf("transaction %s: created and updated timestamps must be set", tx.ID)
	}
	return nil
}
