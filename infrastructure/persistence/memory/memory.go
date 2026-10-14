// Package memory provides an in-process transaction.Repository (spec
// §10.2). It is for tests and examples only: records live in one
// process's memory, survive nothing, and coordinate nothing across
// instances. Production deployments use a durable implementation (spec
// §10.5, ADR-0002).
package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// Store is an in-memory transaction.Repository. The zero value is not
// usable; construct with New. Safe for concurrent use. ChainData is kept
// by reference — treat stored payloads as immutable once handed over.
type Store struct {
	mu      sync.Mutex
	records map[transaction.TransactionID]transaction.Transaction
	keys    map[string]transaction.TransactionID
	leases  map[transaction.TransactionID]transaction.Lease
}

// New constructs an empty store.
func New() *Store {
	return &Store{
		records: map[transaction.TransactionID]transaction.Transaction{},
		keys:    map[string]transaction.TransactionID{},
		leases:  map[transaction.TransactionID]transaction.Lease{},
	}
}

// keyID builds the (scope, key) index entry. A key is only indexed when
// set; the scope is validated at Create.
func keyID(scope transaction.IdempotencyScope, key string) string {
	return scope.Application + "\x00" + scope.OperationType + "\x00" + key
}

// Create implements transaction.Repository. Same key and same request
// hash returns nil and keeps the first record (§10.1: a retried request
// never creates a second operation, even under a race); same key with a
// different hash conflicts.
func (s *Store) Create(_ context.Context, tx transaction.Transaction) error {
	if err := transaction.ValidateRecord(tx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if tx.IdempotencyKey != "" {
		k := keyID(tx.IdempotencyScope, tx.IdempotencyKey)
		if existing, ok := s.records[s.keys[k]]; ok {
			if existing.RequestHash != tx.RequestHash {
				return conflict(tx.IdempotencyScope, tx.IdempotencyKey)
			}
			return nil // same request: the stored record is the operation
		}
	} else if _, ok := s.records[tx.ID]; ok {
		return cherrs.New(cherrs.PersistenceFailure, "transaction.repository",
			"transaction %s already exists", tx.ID)
	}
	s.records[tx.ID] = tx
	if tx.IdempotencyKey != "" {
		s.keys[keyID(tx.IdempotencyScope, tx.IdempotencyKey)] = tx.ID
	}
	return nil
}

// Get implements transaction.Repository.
func (s *Store) Get(_ context.Context, id transaction.TransactionID) (transaction.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.records[id]
	if !ok {
		return transaction.Transaction{}, fmt.Errorf("%w: %s", transaction.ErrRecordNotFound, id)
	}
	return tx, nil
}

// FindByIdempotencyKey implements transaction.Repository.
func (s *Store) FindByIdempotencyKey(_ context.Context, scope transaction.IdempotencyScope, key string) (transaction.Transaction, error) {
	if err := scope.Validate(); err != nil {
		return transaction.Transaction{}, err
	}
	if key == "" {
		return transaction.Transaction{}, fmt.Errorf("idempotency key must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.records[s.keys[keyID(scope, key)]]
	if !ok {
		return transaction.Transaction{}, fmt.Errorf("%w: key %q in scope %s",
			transaction.ErrRecordNotFound, key, scope)
	}
	return tx, nil
}

// UpdateStatus implements transaction.Repository: compare-and-swap plus
// the state-machine matrix, atomically (§10.2).
func (s *Store) UpdateStatus(_ context.Context, id transaction.TransactionID, expected, next transaction.TransactionStatus, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, ok := s.records[id]
	if !ok {
		return fmt.Errorf("%w: %s", transaction.ErrRecordNotFound, id)
	}
	if tx.Status != expected {
		return fmt.Errorf("%w: transaction %s is %s, expected %s",
			transaction.ErrStatusConflict, id, tx.Status, expected)
	}
	if !transaction.CanTransition(expected, next) {
		return fmt.Errorf("%w: %s -> %s on transaction %s",
			transaction.ErrIllegalTransition, expected, next, id)
	}
	tx.Status = next
	tx.UpdatedAt = at
	s.records[id] = tx
	return nil
}

// FindStale implements transaction.Repository: matching records,
// oldest-first, deterministically tie-broken by ID.
func (s *Store) FindStale(_ context.Context, q transaction.StaleQuery) ([]transaction.Transaction, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	statuses := map[transaction.TransactionStatus]bool{}
	for _, st := range q.Statuses {
		statuses[st] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []transaction.Transaction
	for _, tx := range s.records {
		if !statuses[tx.Status] || !tx.UpdatedAt.Before(q.OlderThan) {
			continue
		}
		out = append(out, tx)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if q.Limit > 0 && len(out) > q.Limit {
		out = out[:q.Limit]
	}
	return out, nil
}

// Claim implements transaction.Repository. Expiry is judged against the
// claim instant the caller supplies, which keeps the store clock-free and
// tests deterministic: a still-valid lease blocks, whatever its owner.
// Claims never touch UpdatedAt: a claimed-but-stuck record must still
// reappear in later scans once its lease lapses (§10.4).
func (s *Store) Claim(_ context.Context, id transaction.TransactionID, lease transaction.Lease) (bool, error) {
	if err := lease.Validate(); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return false, fmt.Errorf("%w: %s", transaction.ErrRecordNotFound, id)
	}
	held, ok := s.leases[id]
	if ok && held.Holder != lease.Holder && held.ExpiresAt.After(lease.At) {
		return false, nil // still held by someone else
	}
	s.leases[id] = lease
	return true, nil
}

// Release implements transaction.Repository: owner-only; a stale worker
// releasing after a takeover is a no-op, not an error.
func (s *Store) Release(_ context.Context, id transaction.TransactionID, holder string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return fmt.Errorf("%w: %s", transaction.ErrRecordNotFound, id)
	}
	if held, ok := s.leases[id]; ok && held.Holder == holder {
		delete(s.leases, id)
	}
	return nil
}

// conflict builds the §10.1 key-reuse error.
func conflict(scope transaction.IdempotencyScope, key string) error {
	return transaction.IdempotencyConflict(scope, key)
}

var _ transaction.Repository = (*Store)(nil)
