// Package repository holds the persistence conformance suite (ADR-0002):
// one suite that every transaction.Repository implementation must pass,
// so memory and SQL stores behave identically on idempotency uniqueness,
// conditional status updates, staleness scanning, and leases. New
// implementations are plugged in by a thin _test.go runner; semantics
// live here exactly once.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// newStoreFunc builds a fresh, empty repository for one subtest.
type newStoreFunc func() transaction.Repository

// Run executes the full repository suite against a fresh store per
// subtest. The store is expected to be empty at start; tests never share
// records.
func Run(t *testing.T, newStore newStoreFunc) {
	t.Helper()
	t.Run("IdempotencyUniqueness", func(t *testing.T) { runIdempotency(t, newStore) })
	t.Run("StatusMachine", func(t *testing.T) { runStatusMachine(t, newStore) })
	t.Run("StaleScan", func(t *testing.T) { runStaleScan(t, newStore) })
	t.Run("Lease", func(t *testing.T) { runLease(t, newStore) })
	t.Run("RecordValidation", func(t *testing.T) { runRecordValidation(t, newStore) })
	t.Run("ChainData", func(t *testing.T) { runChainData(t, newStore) })
}

// testNet is a stable network identity for suite records.
var testNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "conformance", ChainID: "1337"}

// record builds a valid created-status record with the given fields.
func record(id, key, hash string, at time.Time) transaction.Transaction {
	tx := transaction.Transaction{
		ID:          transaction.TransactionID(id),
		OperationID: transaction.OperationID("op-" + id),
		Network:     testNet,
		Status:      transaction.TxCreated,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if key != "" {
		tx.IdempotencyKey = key
		tx.IdempotencyScope = transaction.IdempotencyScope{Application: "app", OperationType: "payout"}
		tx.RequestHash = hash
	}
	return tx
}

func mustCreate(t *testing.T, repo transaction.Repository, tx transaction.Transaction) {
	t.Helper()
	if err := repo.Create(context.Background(), tx); err != nil {
		t.Fatalf("create %s: unexpected error: %v", tx.ID, err)
	}
}

// runIdempotency pins §10.1: one operation per (scope, key), replays with
// the same request reattach, different requests conflict — under races
// too.
func runIdempotency(t *testing.T, newStore newStoreFunc) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("finds record by key", func(t *testing.T) {
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "key-1", "hash-1", at))
		got, err := repo.FindByIdempotencyKey(ctx,
			transaction.IdempotencyScope{Application: "app", OperationType: "payout"}, "key-1")
		if err != nil {
			t.Fatalf("find by key: %v", err)
		}
		if got.ID != "tx-1" || got.RequestHash != "hash-1" {
			t.Fatalf("find by key returned %+v", got)
		}
	})

	t.Run("missing key is not found", func(t *testing.T) {
		repo := newStore()
		_, err := repo.FindByIdempotencyKey(ctx,
			transaction.IdempotencyScope{Application: "app", OperationType: "payout"}, "nope")
		if !errors.Is(err, transaction.ErrRecordNotFound) {
			t.Fatalf("want ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("same key same hash keeps the first record", func(t *testing.T) {
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "key-1", "hash-1", at))
		// A retry builds a different record identity but the same request.
		retry := record("tx-2", "key-1", "hash-1", at.Add(time.Minute))
		if err := repo.Create(ctx, retry); err != nil {
			t.Fatalf("replay create: %v", err)
		}
		got, err := repo.FindByIdempotencyKey(ctx,
			transaction.IdempotencyScope{Application: "app", OperationType: "payout"}, "key-1")
		if err != nil {
			t.Fatalf("find by key: %v", err)
		}
		if got.ID != "tx-1" || !got.UpdatedAt.Equal(at) {
			t.Fatalf("replay must keep the first record untouched, got %+v", got)
		}
		if _, err := repo.Get(ctx, "tx-2"); !errors.Is(err, transaction.ErrRecordNotFound) {
			t.Fatalf("replay must not store the second id, got %v", err)
		}
	})

	t.Run("same key different hash conflicts", func(t *testing.T) {
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "key-1", "hash-1", at))
		err := repo.Create(ctx, record("tx-2", "key-1", "hash-DIFFERENT", at))
		if !errors.Is(err, cherrs.ErrIdempotencyConflict) {
			t.Fatalf("want ErrIdempotencyConflict, got %v", err)
		}
		// The stored record wins; the loser never lands.
		got, err := repo.FindByIdempotencyKey(ctx,
			transaction.IdempotencyScope{Application: "app", OperationType: "payout"}, "key-1")
		if err != nil || got.ID != "tx-1" {
			t.Fatalf("conflicting create must not replace the record: %+v %v", got, err)
		}
	})

	t.Run("keys do not leak across scopes", func(t *testing.T) {
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "key-1", "hash-1", at))
		// Same key string, different application.
		other := record("tx-2", "key-1", "hash-1", at)
		other.IdempotencyScope = transaction.IdempotencyScope{Application: "other-app", OperationType: "payout"}
		mustCreate(t, repo, other)
		// Same application, different operation type.
		otherOp := record("tx-3", "key-1", "hash-1", at)
		otherOp.IdempotencyScope = transaction.IdempotencyScope{Application: "app", OperationType: "deposit"}
		mustCreate(t, repo, otherOp)

		scope := transaction.IdempotencyScope{Application: "app", OperationType: "payout"}
		got, err := repo.FindByIdempotencyKey(ctx, scope, "key-1")
		if err != nil || got.ID != "tx-1" {
			t.Fatalf("scope isolation broken: got %+v %v", got, err)
		}
	})

	t.Run("concurrent replays create exactly one operation", func(t *testing.T) {
		repo := newStore()
		const n = 16
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = repo.Create(ctx, record(fmt.Sprintf("tx-%d", i), "key-1", "hash-1", at))
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("replay %d: same request must never conflict: %v", i, err)
			}
		}
		found := 0
		for i := 0; i < n; i++ {
			if _, err := repo.Get(ctx, transaction.TransactionID(fmt.Sprintf("tx-%d", i))); err == nil {
				found++
			}
		}
		if found != 1 {
			t.Fatalf("want exactly one stored record, got %d", found)
		}
	})

	t.Run("concurrent different requests elect one winner", func(t *testing.T) {
		repo := newStore()
		const n = 16
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = repo.Create(ctx, record(fmt.Sprintf("tx-%d", i), "key-1", fmt.Sprintf("hash-%d", i), at))
			}(i)
		}
		wg.Wait()
		winners, conflicts := 0, 0
		for i, err := range errs {
			switch {
			case err == nil:
				winners++
			case errors.Is(err, cherrs.ErrIdempotencyConflict):
				conflicts++
			default:
				t.Fatalf("replay %d: unexpected error %v", i, err)
			}
		}
		if winners != 1 || conflicts != n-1 {
			t.Fatalf("want 1 winner and %d conflicts, got %d and %d", n-1, winners, conflicts)
		}
	})
}

// runStatusMachine pins §10.2: the matrix applies atomically through the
// repository, with compare-and-swap semantics on the expectation.
func runStatusMachine(t *testing.T, newStore newStoreFunc) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	newCreated := func(t *testing.T) transaction.Repository {
		t.Helper()
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "", "", at))
		return repo
	}

	t.Run("missing record is not found", func(t *testing.T) {
		repo := newStore()
		if _, err := repo.Get(ctx, "nope"); !errors.Is(err, transaction.ErrRecordNotFound) {
			t.Fatalf("want ErrRecordNotFound, got %v", err)
		}
		if err := repo.UpdateStatus(ctx, "nope", transaction.TxCreated, transaction.TxPrepared, at); !errors.Is(err, transaction.ErrRecordNotFound) {
			t.Fatalf("want ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("applies a legal transition and stamps the timestamp", func(t *testing.T) {
		repo := newCreated(t)
		next := at.Add(time.Minute)
		if err := repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxPrepared, next); err != nil {
			t.Fatalf("update status: %v", err)
		}
		got, err := repo.Get(ctx, "tx-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != transaction.TxPrepared || !got.UpdatedAt.Equal(next) {
			t.Fatalf("want prepared at %v, got %+v", next, got)
		}
		if got.CreatedAt.Equal(next) {
			t.Fatalf("created timestamp must not move: %+v", got)
		}
	})

	t.Run("stale expectation conflicts", func(t *testing.T) {
		repo := newCreated(t)
		if err := repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxPrepared, at); err != nil {
			t.Fatalf("update status: %v", err)
		}
		// A worker that still expects created lost the race.
		err := repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxFailed, at)
		if !errors.Is(err, transaction.ErrStatusConflict) {
			t.Fatalf("want ErrStatusConflict, got %v", err)
		}
	})

	t.Run("illegal transition is rejected", func(t *testing.T) {
		repo := newCreated(t)
		err := repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxConfirmed, at)
		if !errors.Is(err, transaction.ErrIllegalTransition) {
			t.Fatalf("want ErrIllegalTransition, got %v", err)
		}
		// The record is untouched by a rejected transition.
		got, err := repo.Get(ctx, "tx-1")
		if err != nil || got.Status != transaction.TxCreated {
			t.Fatalf("rejected transition must not move the record: %+v %v", got, err)
		}
	})

	t.Run("terminal status accepts no transition", func(t *testing.T) {
		repo := newCreated(t)
		if err := repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxFinalized, at); err == nil {
			t.Fatalf("created -> finalized is not a legal path")
		}
	})

	t.Run("same status is an idempotent observation", func(t *testing.T) {
		repo := newCreated(t)
		if err := repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxCreated, at); err != nil {
			t.Fatalf("same-status update: %v", err)
		}
	})

	t.Run("concurrent transitions elect one writer", func(t *testing.T) {
		repo := newCreated(t)
		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = repo.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxPrepared, at)
			}(i)
		}
		wg.Wait()
		okCount := 0
		for i, err := range errs {
			switch {
			case err == nil:
				okCount++
			case errors.Is(err, transaction.ErrStatusConflict):
			default:
				t.Fatalf("worker %d: unexpected error %v", i, err)
			}
		}
		if okCount != 1 {
			t.Fatalf("want exactly one winning transition, got %d", okCount)
		}
	})
}

// runStaleScan pins §10.4: deterministic oldest-first candidate pages.
func runStaleScan(t *testing.T, newStore newStoreFunc) {
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	seed := func(t *testing.T) transaction.Repository {
		t.Helper()
		repo := newStore()
		cases := []struct {
			id  string
			st  transaction.TransactionStatus
			age time.Duration
		}{
			{"old-unknown", transaction.TxUnknown, 2 * time.Hour},
			{"new-unknown", transaction.TxUnknown, 10 * time.Minute},
			{"old-submitted", transaction.TxSubmitted, 3 * time.Hour},
			{"old-confirmed", transaction.TxConfirmed, 9 * time.Hour},
			{"old-reorged", transaction.TxReorged, 2 * time.Hour},
		}
		for _, c := range cases {
			tx := record(c.id, "", "", base.Add(-c.age))
			tx.Status = c.st
			mustCreate(t, repo, tx)
		}
		return repo
	}

	t.Run("filters by status and age oldest first", func(t *testing.T) {
		repo := seed(t)
		got, err := repo.FindStale(ctx, transaction.StaleQuery{
			Statuses:  []transaction.TransactionStatus{transaction.TxUnknown, transaction.TxSubmitted},
			OlderThan: base.Add(-time.Hour),
		})
		if err != nil {
			t.Fatalf("find stale: %v", err)
		}
		want := []transaction.TransactionID{"old-submitted", "old-unknown"}
		if len(got) != len(want) {
			t.Fatalf("want %v, got %d records", want, len(got))
		}
		for i, id := range want {
			if got[i].ID != id {
				t.Fatalf("position %d: want %s, got %s", i, id, got[i].ID)
			}
		}
	})

	t.Run("limit bounds the page", func(t *testing.T) {
		repo := seed(t)
		got, err := repo.FindStale(ctx, transaction.StaleQuery{
			Statuses:  []transaction.TransactionStatus{transaction.TxUnknown, transaction.TxSubmitted},
			OlderThan: base.Add(-time.Hour),
			Limit:     1,
		})
		if err != nil {
			t.Fatalf("find stale: %v", err)
		}
		if len(got) != 1 || got[0].ID != "old-submitted" {
			t.Fatalf("limit must keep the oldest first, got %+v", got)
		}
	})

	t.Run("validates the query", func(t *testing.T) {
		repo := newStore()
		if _, err := repo.FindStale(ctx, transaction.StaleQuery{OlderThan: base}); err == nil {
			t.Fatalf("empty status list must be rejected")
		}
		if _, err := repo.FindStale(ctx, transaction.StaleQuery{
			Statuses:  []transaction.TransactionStatus{"bogus"},
			OlderThan: base,
		}); err == nil {
			t.Fatalf("unknown status must be rejected")
		}
	})
}

// runLease pins §10.4: one worker at a time, expiring claims, owner-only
// release.
func runLease(t *testing.T, newStore newStoreFunc) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	holderA, holderB := "worker-a", "worker-b"
	lease := func(holder string, until time.Time) transaction.Lease {
		return transaction.Lease{Holder: holder, ExpiresAt: until}
	}

	newRecord := func(t *testing.T) transaction.Repository {
		t.Helper()
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "", "", at))
		return repo
	}

	t.Run("unclaimed record claims", func(t *testing.T) {
		repo := newRecord(t)
		ok, err := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour)))
		if err != nil || !ok {
			t.Fatalf("claim: ok=%v err=%v", ok, err)
		}
	})

	t.Run("held record refuses another holder", func(t *testing.T) {
		repo := newRecord(t)
		if ok, _ := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour))); !ok {
			t.Fatalf("setup claim failed")
		}
		ok, err := repo.Claim(ctx, "tx-1", lease(holderB, at.Add(30*time.Minute)))
		if ok || err != nil {
			t.Fatalf("claim by second holder must refuse without error, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("same holder renews", func(t *testing.T) {
		repo := newRecord(t)
		if ok, _ := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour))); !ok {
			t.Fatalf("setup claim failed")
		}
		ok, err := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(2*time.Hour)))
		if err != nil || !ok {
			t.Fatalf("renewal: ok=%v err=%v", ok, err)
		}
		// The renewed lease still blocks the other holder.
		ok, err = repo.Claim(ctx, "tx-1", lease(holderB, at.Add(30*time.Minute)))
		if ok || err != nil {
			t.Fatalf("renewed lease must still block, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("expired lease is takeable", func(t *testing.T) {
		repo := newRecord(t)
		if ok, _ := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour))); !ok {
			t.Fatalf("setup claim failed")
		}
		// holderB claims at a later instant with a later expiry: by the
		// incoming lease's clock the old claim has lapsed.
		ok, err := repo.Claim(ctx, "tx-1", lease(holderB, at.Add(2*time.Hour)))
		if err != nil || !ok {
			t.Fatalf("takeover after expiry: ok=%v err=%v", ok, err)
		}
	})

	t.Run("claim and release on missing records are not found", func(t *testing.T) {
		repo := newStore()
		if _, err := repo.Claim(ctx, "nope", lease(holderA, at)); !errors.Is(err, transaction.ErrRecordNotFound) {
			t.Fatalf("want ErrRecordNotFound, got %v", err)
		}
		if err := repo.Release(ctx, "nope", holderA); !errors.Is(err, transaction.ErrRecordNotFound) {
			t.Fatalf("want ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("owner release frees the record", func(t *testing.T) {
		repo := newRecord(t)
		if ok, _ := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour))); !ok {
			t.Fatalf("setup claim failed")
		}
		if err := repo.Release(ctx, "tx-1", holderA); err != nil {
			t.Fatalf("release: %v", err)
		}
		ok, err := repo.Claim(ctx, "tx-1", lease(holderB, at.Add(30*time.Minute)))
		if err != nil || !ok {
			t.Fatalf("claim after release: ok=%v err=%v", ok, err)
		}
	})

	t.Run("release by a non-owner is a no-op", func(t *testing.T) {
		repo := newRecord(t)
		if ok, _ := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour))); !ok {
			t.Fatalf("setup claim failed")
		}
		if err := repo.Release(ctx, "tx-1", holderB); err != nil {
			t.Fatalf("foreign release must not error: %v", err)
		}
		ok, err := repo.Claim(ctx, "tx-1", lease(holderB, at.Add(30*time.Minute)))
		if ok || err != nil {
			t.Fatalf("foreign release must not drop the lease, got ok=%v err=%v", ok, err)
		}
	})

	t.Run("claim does not refresh staleness", func(t *testing.T) {
		repo := newRecord(t)
		if ok, _ := repo.Claim(ctx, "tx-1", lease(holderA, at.Add(time.Hour))); !ok {
			t.Fatalf("setup claim failed")
		}
		got, err := repo.FindStale(ctx, transaction.StaleQuery{
			Statuses:  []transaction.TransactionStatus{transaction.TxCreated},
			OlderThan: at.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("find stale: %v", err)
		}
		if len(got) != 1 || got[0].ID != "tx-1" {
			t.Fatalf("claim must not touch UpdatedAt, got %+v", got)
		}
	})

	t.Run("validates the lease", func(t *testing.T) {
		repo := newRecord(t)
		if _, err := repo.Claim(ctx, "tx-1", transaction.Lease{Holder: holderA}); err == nil {
			t.Fatalf("lease without expiry must be rejected")
		}
		if _, err := repo.Claim(ctx, "tx-1", transaction.Lease{ExpiresAt: at}); err == nil {
			t.Fatalf("lease without holder must be rejected")
		}
	})
}

// runRecordValidation pins the shared record shape: memory and SQL
// implementations reject the same invalid inputs.
func runRecordValidation(t *testing.T, newStore newStoreFunc) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("rejects an empty id", func(t *testing.T) {
		repo := newStore()
		err := repo.Create(ctx, record("", "", "", at))
		if err == nil {
			t.Fatalf("empty id must be rejected")
		}
	})

	t.Run("rejects an unknown status", func(t *testing.T) {
		repo := newStore()
		tx := record("tx-1", "", "", at)
		tx.Status = "bogus"
		if err := repo.Create(ctx, tx); err == nil {
			t.Fatalf("unknown status must be rejected")
		}
	})

	t.Run("rejects a key without request hash", func(t *testing.T) {
		repo := newStore()
		tx := record("tx-1", "key-1", "", at)
		if err := repo.Create(ctx, tx); err == nil {
			t.Fatalf("key without request hash must be rejected (§10.1)")
		}
	})

	t.Run("rejects a key without scope", func(t *testing.T) {
		repo := newStore()
		tx := record("tx-1", "key-1", "hash-1", at)
		tx.IdempotencyScope = transaction.IdempotencyScope{}
		if err := repo.Create(ctx, tx); err == nil {
			t.Fatalf("key without scope must be rejected (§10.1)")
		}
	})

	t.Run("rejects zero timestamps", func(t *testing.T) {
		repo := newStore()
		tx := record("tx-1", "", "", at)
		tx.CreatedAt = time.Time{}
		if err := repo.Create(ctx, tx); err == nil {
			t.Fatalf("zero timestamps must be rejected")
		}
	})

	t.Run("rejects a non-canonical amount", func(t *testing.T) {
		repo := newStore()
		tx := record("tx-1", "", "", at)
		tx.Amount = identifiers.Amount{Raw: "1.5", AssetID: "native"}
		if err := repo.Create(ctx, tx); err == nil {
			t.Fatalf("non-canonical amount must be rejected")
		}
	})
}

// runChainData pins what every store guarantees about the opaque payload:
// a JSON-encodable value survives the round trip with equal JSON. Stores
// may differ in the Go type they return (memory keeps the original value,
// durable stores return encoded bytes), never in the JSON content.
func runChainData(t *testing.T, newStore newStoreFunc) {
	ctx := context.Background()
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("json content round trips", func(t *testing.T) {
		repo := newStore()
		tx := record("tx-1", "", "", at)
		tx.ChainData = map[string]any{"kind": "evm", "nonce": float64(7)}
		mustCreate(t, repo, tx)

		got, err := repo.Get(ctx, "tx-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		want, err := json.Marshal(tx.ChainData)
		if err != nil {
			t.Fatalf("marshal want: %v", err)
		}
		have, err := json.Marshal(got.ChainData)
		if err != nil {
			t.Fatalf("marshal have: %v", err)
		}
		if string(want) != string(have) {
			t.Fatalf("chain data changed across the round trip: want %s, have %s", want, have)
		}
	})

	t.Run("absent chain data stays absent", func(t *testing.T) {
		repo := newStore()
		mustCreate(t, repo, record("tx-1", "", "", at))
		got, err := repo.Get(ctx, "tx-1")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.ChainData != nil {
			if raw, ok := got.ChainData.(json.RawMessage); !ok || len(raw) != 0 {
				t.Fatalf("absent chain data must stay absent, got %#v", got.ChainData)
			}
		}
	})
}
