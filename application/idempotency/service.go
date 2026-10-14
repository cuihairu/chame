// Package idempotency implements the idempotency-key semantics (spec
// §10.1): one business operation per key, with persisted state that
// survives restarts. It sits on a transaction.Repository and turns the
// repository's uniqueness rules into the three outcomes an application
// handles: create, reuse, or conflict.
package idempotency

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/clock"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// Outcome tells the caller how Begin resolved the key.
type Outcome string

const (
	// OutcomeCreated means the key was free: the built record is now the
	// operation of record.
	OutcomeCreated Outcome = "created"
	// OutcomeExisting means the key already held an operation for the
	// same request: the returned record is authoritative and the caller
	// must not start a second one (§10.1).
	OutcomeExisting Outcome = "existing"
)

// BuildFunc fills in the business fields of a new record. The service
// supplies identity, idempotency, status, and timestamps; the builder
// returns it completed (network, parties, amount, chain material).
type BuildFunc func(tx transaction.Transaction) (transaction.Transaction, error)

// Service resolves idempotency keys (§10.1) on top of a
// transaction.Repository. It is safe for concurrent use.
type Service struct {
	repo  transaction.Repository
	now   clock.Clock
	newID func() transaction.TransactionID
}

// Option customizes the service.
type Option func(*Service)

// WithClock injects the time source. Default: clock.System.
func WithClock(c clock.Clock) Option {
	return func(s *Service) { s.now = c }
}

// WithNewID injects the transaction id mint. Default: random 16-byte hex.
func WithNewID(f func() transaction.TransactionID) Option {
	return func(s *Service) { s.newID = f }
}

// New builds the service on a repository.
func New(repo transaction.Repository, opts ...Option) *Service {
	s := &Service{repo: repo, now: clock.System{}, newID: randomID}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Begin resolves the key for one operation attempt.
//
//   - No record: the builder completes a seeded record, which is stored
//     and returned with OutcomeCreated.
//   - A record with the same request hash: returned with
//     OutcomeExisting — the replay attaches to the first operation, which
//     keeps retries from creating a second chain transaction (§10.1),
//     including when the first attempt is still in flight.
//   - A record with a different request hash: the conflict error
//     (matching cherrs.ErrIdempotencyConflict) — same key, different
//     request is a caller bug, not a retry.
//
// Under a concurrent race the create may lose to another caller with the
// same request; Begin then re-finds the key once and returns that
// operation with OutcomeExisting, so every replay converges on one
// record.
func (s *Service) Begin(ctx context.Context, scope transaction.IdempotencyScope, key, requestHash string, build BuildFunc) (transaction.Transaction, Outcome, error) {
	if err := scope.Validate(); err != nil {
		return transaction.Transaction{}, "", cherrs.New(cherrs.InvalidArgument, "idempotency.begin", "%v", err)
	}
	if key == "" {
		return transaction.Transaction{}, "", cherrs.New(cherrs.InvalidArgument, "idempotency.begin",
			"idempotency key must not be empty")
	}
	if requestHash == "" {
		return transaction.Transaction{}, "", cherrs.New(cherrs.InvalidArgument, "idempotency.begin",
			"request hash must not be empty (§10.1 fingerprint)")
	}

	existing, err := s.repo.FindByIdempotencyKey(ctx, scope, key)
	switch {
	case err == nil:
		return s.resolve(existing, requestHash)
	case !errors.Is(err, transaction.ErrRecordNotFound):
		return transaction.Transaction{}, "", err
	}

	now := s.now.Now()
	seed := transaction.Transaction{
		ID:               s.newID(),
		Status:           transaction.TxCreated,
		IdempotencyKey:   key,
		IdempotencyScope: scope,
		RequestHash:      requestHash,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	built, err := build(seed)
	if err != nil {
		return transaction.Transaction{}, "", err
	}
	if err := transaction.ValidateRecord(built); err != nil {
		return transaction.Transaction{}, "", cherrs.New(cherrs.InvalidArgument, "idempotency.begin", "%v", err)
	}
	if built.IdempotencyKey != key || built.IdempotencyScope != scope || built.RequestHash != requestHash {
		return transaction.Transaction{}, "", cherrs.New(cherrs.InvalidArgument, "idempotency.begin",
			"builder must keep the seeded identity and idempotency fields")
	}
	if err := s.repo.Create(ctx, built); err != nil {
		if !errors.Is(err, cherrs.ErrIdempotencyConflict) {
			return transaction.Transaction{}, "", err
		}
		// Lost a race for the key. Re-find once and resolve; if the
		// record is still missing the store misbehaved — surface it.
		existing, ferr := s.repo.FindByIdempotencyKey(ctx, scope, key)
		if ferr != nil {
			return transaction.Transaction{}, "", ferr
		}
		return s.resolve(existing, requestHash)
	}
	// Same-key same-hash creates are no-ops on the store: a racing twin
	// may have won. Report the stored operation, not the local copy, so
	// the caller drives the record that actually exists.
	if stored, ferr := s.repo.FindByIdempotencyKey(ctx, scope, key); ferr == nil && stored.ID != built.ID {
		return stored, OutcomeExisting, nil
	}
	return built, OutcomeCreated, nil
}

// resolve applies the §10.1 request-hash check to a found record.
func (s *Service) resolve(existing transaction.Transaction, requestHash string) (transaction.Transaction, Outcome, error) {
	if existing.RequestHash != requestHash {
		return transaction.Transaction{}, "", transaction.IdempotencyConflict(existing.IdempotencyScope, existing.IdempotencyKey)
	}
	return existing, OutcomeExisting, nil
}

// randomID mints a transaction id: random 16 bytes, hex-encoded.
func randomID() transaction.TransactionID {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("idempotency: mint transaction id: %v", err))
	}
	return transaction.TransactionID(hex.EncodeToString(b[:]))
}
