package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

var (
	evmNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}
	scope  = transaction.IdempotencyScope{Application: "game", OperationType: "payout"}
	t0     = time.Unix(1700000000, 0).UTC()
	t1     = t0.Add(time.Minute)
	t2     = t0.Add(2 * time.Minute)
	t3     = t0.Add(3 * time.Minute)
)

func validTx(id string) transaction.Transaction {
	return transaction.Transaction{
		ID:          transaction.TransactionID(id),
		OperationID: transaction.OperationID("op-" + id),
		Network:     evmNet,
		Status:      transaction.TxCreated,
		CreatedAt:   t0,
		UpdatedAt:   t0,
	}
}

func mustCreate(t *testing.T, s *Store, tx transaction.Transaction) transaction.CreateResult {
	t.Helper()
	res, err := s.Create(context.Background(), tx)
	if err != nil {
		t.Fatalf("Create(%s): %v", tx.ID, err)
	}
	if res != transaction.CreateInserted {
		t.Fatalf("Create(%s): result=%v, want CreateInserted", tx.ID, res)
	}
	return res
}

// TestStoreCreateValidation checks that Create rejects malformed records
// with the shared ValidateRecord rules, so memory and SQL stores reject
// the same inputs.
func TestStoreCreateValidation(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*transaction.Transaction)
	}{
		{"empty id", func(tx *transaction.Transaction) { tx.ID = "" }},
		{"bad network", func(tx *transaction.Transaction) {
			tx.Network = identifiers.NetworkID{Family: "cosmos", Name: "x", ChainID: "1"}
		}},
		{"unknown status", func(tx *transaction.Transaction) { tx.Status = "flying" }},
		{"zero timestamps", func(tx *transaction.Transaction) { tx.CreatedAt = time.Time{} }},
		{"key without request hash", func(tx *transaction.Transaction) { tx.IdempotencyKey = "k1" }},
		{"key without scope application", func(tx *transaction.Transaction) {
			tx.IdempotencyKey = "k1"
			tx.RequestHash = "h1"
			tx.IdempotencyScope = transaction.IdempotencyScope{OperationType: "payout"}
		}},
		{"key without scope operation", func(tx *transaction.Transaction) {
			tx.IdempotencyKey = "k1"
			tx.RequestHash = "h1"
			tx.IdempotencyScope = transaction.IdempotencyScope{Application: "game"}
		}},
	}
	for _, tc := range cases {
		tx := validTx("tx-1")
		tc.mut(&tx)
		if _, err := New().Create(context.Background(), tx); err == nil {
			t.Errorf("%s: accepted invalid record", tc.name)
		}
	}
}

// TestStoreCreateIdempotency pins §10.1: one record per (scope, key).
// A retried request never creates a second operation; a key reused by a
// different request conflicts; different scopes never collide.
func TestStoreCreateIdempotency(t *testing.T) {
	ctx := context.Background()
	s := New()

	first := validTx("tx-1")
	first.IdempotencyKey = "k1"
	first.IdempotencyScope = scope
	first.RequestHash = "h1"
	mustCreate(t, s, first)

	retry := first
	retry.ID = "tx-2"
	retry.CreatedAt = t1
	retry.UpdatedAt = t1
	res, err := s.Create(ctx, retry)
	if err != nil {
		t.Fatalf("same key and hash rejected: %v", err)
	}
	if res != transaction.CreateSameKeyExisting {
		t.Fatalf("result=%v, want CreateSameKeyExisting", res)
	}
	kept, err := s.Get(ctx, "tx-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "tx-2"); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("retry stored a second record: %v", err)
	}
	if kept.ID != "tx-1" {
		t.Fatalf("stored record mutated to %+v", kept)
	}

	other := first
	other.RequestHash = "h2"
	if _, err := s.Create(ctx, other); !errors.Is(err, cherrs.ErrIdempotencyConflict) {
		t.Fatalf("same key different hash: err=%v, want ErrIdempotencyConflict", err)
	}

	elsewhere := first
	elsewhere.IdempotencyScope = transaction.IdempotencyScope{Application: "wallet", OperationType: "payout"}
	if res, err := s.Create(ctx, elsewhere); err != nil || res != transaction.CreateInserted {
		t.Fatalf("same key in another scope rejected: res=%v err=%v", res, err)
	}

	unkeyed := validTx("tx-1")
	if _, err := s.Create(ctx, unkeyed); !errors.Is(err, cherrs.ErrPersistenceFailure) {
		t.Fatalf("duplicate unkeyed id: err=%v, want ErrPersistenceFailure", err)
	}
}

// TestStoreGetAndFind covers the keyed and unkeyed reads, including the
// query-shape errors FindByIdempotencyKey must reject.
func TestStoreGetAndFind(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustCreate(t, s, validTx("tx-1"))

	got, err := s.Get(ctx, "tx-1")
	if err != nil || got.ID != "tx-1" {
		t.Fatalf("Get=%+v err=%v", got, err)
	}
	if _, err := s.Get(ctx, "tx-9"); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("Get missing: err=%v, want ErrRecordNotFound", err)
	}

	if _, err := s.FindByIdempotencyKey(ctx, transaction.IdempotencyScope{}, "k"); err == nil {
		t.Error("empty scope accepted")
	}
	if _, err := s.FindByIdempotencyKey(ctx, scope, ""); err == nil {
		t.Error("empty key accepted")
	}
	if _, err := s.FindByIdempotencyKey(ctx, scope, "k1"); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("unknown key: err=%v, want ErrRecordNotFound", err)
	}

	keyed := validTx("tx-2")
	keyed.IdempotencyKey = "k1"
	keyed.IdempotencyScope = scope
	keyed.RequestHash = "h1"
	mustCreate(t, s, keyed)

	found, err := s.FindByIdempotencyKey(ctx, scope, "k1")
	if err != nil || found.ID != "tx-2" {
		t.Fatalf("FindByIdempotencyKey=%+v err=%v", found, err)
	}
	if _, err := s.FindByIdempotencyKey(ctx, transaction.IdempotencyScope{Application: "game", OperationType: "deposit"}, "k1"); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("key leaked across operation types: err=%v", err)
	}
}

// TestStoreUpdateStatus covers the conditional update: not-found, stale
// expectation, matrix violation, success stamping UpdatedAt, and the
// idempotent same-status observation.
func TestStoreUpdateStatus(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		expected transaction.TransactionStatus
		next     transaction.TransactionStatus
		wantErr  error
	}{
		{"missing record", transaction.TxCreated, transaction.TxPrepared, transaction.ErrRecordNotFound},
		{"stale expectation", transaction.TxPrepared, transaction.TxSigned, transaction.ErrStatusConflict},
		{"illegal transition", transaction.TxCreated, transaction.TxFinalized, transaction.ErrIllegalTransition},
		{"terminal frozen", transaction.TxFailed, transaction.TxCreated, transaction.ErrIllegalTransition},
	}
	for _, tc := range cases {
		s := New()
		tx := validTx("tx-1")
		if tc.name == "terminal frozen" {
			tx.Status = transaction.TxFailed
		}
		mustCreate(t, s, tx)
		if _, ok := s.records[tx.ID]; !ok {
			t.Fatal("seed record missing")
		}
		id := tx.ID
		if tc.name == "missing record" {
			id = "tx-other"
		}
		err := s.UpdateStatus(ctx, id, tc.expected, tc.next, t1)
		if tc.name == "missing record" {
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: err=%v, want %v", tc.name, err, tc.wantErr)
			}
			continue
		}
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err=%v, want %v", tc.name, err, tc.wantErr)
		}
	}

	s := New()
	mustCreate(t, s, validTx("tx-1"))
	if err := s.UpdateStatus(ctx, "tx-1", transaction.TxCreated, transaction.TxPrepared, t1); err != nil {
		t.Fatalf("legal transition rejected: %v", err)
	}
	got, _ := s.Get(ctx, "tx-1")
	if got.Status != transaction.TxPrepared || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("record after move: %+v", got)
	}
	if err := s.UpdateStatus(ctx, "tx-1", transaction.TxPrepared, transaction.TxPrepared, t2); err != nil {
		t.Fatalf("idempotent same-status observation rejected: %v", err)
	}
	if got, _ = s.Get(ctx, "tx-1"); !got.UpdatedAt.Equal(t2) {
		t.Fatalf("UpdatedAt not stamped on idempotent move: %+v", got)
	}
}

// TestStoreAttach pins §10.2: the chain reference and payload land in the
// same atomic write as the status move, and a nil payload clears.
func TestStoreAttach(t *testing.T) {
	ctx := context.Background()
	s := New()
	mustCreate(t, s, validTx("tx-1"))

	if err := s.Attach(ctx, "tx-9", transaction.TxCreated, transaction.TxPrepared, "0xh", "data", t1); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("Attach missing: err=%v", err)
	}
	if err := s.Attach(ctx, "tx-1", transaction.TxPrepared, transaction.TxSigned, "0xh", "data", t1); !errors.Is(err, transaction.ErrStatusConflict) {
		t.Fatalf("stale expectation: err=%v", err)
	}
	if err := s.Attach(ctx, "tx-1", transaction.TxCreated, transaction.TxConfirmed, "0xh", "data", t1); !errors.Is(err, transaction.ErrIllegalTransition) {
		t.Fatalf("matrix violation: err=%v", err)
	}
	if err := s.Attach(ctx, "tx-1", transaction.TxCreated, transaction.TxPrepared, "", []byte("payload"), t1); err != nil {
		t.Fatalf("Attach rejected: %v", err)
	}
	got, _ := s.Get(ctx, "tx-1")
	if got.Status != transaction.TxPrepared || got.ChainHash != "" || string(got.ChainData.([]byte)) != "payload" || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("record after attach: %+v", got)
	}
	if err := s.Attach(ctx, "tx-1", transaction.TxPrepared, transaction.TxSigned, "0xhash", nil, t2); err != nil {
		t.Fatalf("Attach with nil payload rejected: %v", err)
	}
	if got, _ = s.Get(ctx, "tx-1"); got.ChainHash != "0xhash" || got.ChainData != nil || !got.UpdatedAt.Equal(t2) {
		t.Fatalf("record after nil attach: %+v", got)
	}
}

// TestStoreFindStale covers the §10.4 scan: query validation, status
// filter, staleness bound, oldest-first order, and the deterministic
// UpdatedAt/ID tie-break.
func TestStoreFindStale(t *testing.T) {
	ctx := context.Background()
	if _, err := New().FindStale(ctx, transaction.StaleQuery{OlderThan: t3, Limit: 1}); err == nil {
		t.Error("query without statuses accepted")
	}
	if _, err := New().FindStale(ctx, transaction.StaleQuery{Statuses: []transaction.TransactionStatus{"flying"}, OlderThan: t3}); err == nil {
		t.Error("unknown status accepted")
	}
	if _, err := New().FindStale(ctx, transaction.StaleQuery{Statuses: []transaction.TransactionStatus{transaction.TxPrepared}, OlderThan: t3, Limit: -1}); err == nil {
		t.Error("negative limit accepted")
	}

	s := New()
	seed := []struct {
		id string
		st transaction.TransactionStatus
		at time.Time
	}{
		{"tx-b", transaction.TxPrepared, t1},
		{"tx-a", transaction.TxPrepared, t1}, // same instant: ID tie-break
		{"tx-c", transaction.TxIncluded, t1}, // status not selected
		{"tx-d", transaction.TxPrepared, t2}, // fresher than the bound
	}
	for _, r := range seed {
		tx := validTx(r.id)
		tx.Status = r.st
		tx.UpdatedAt = r.at
		mustCreate(t, s, tx)
	}

	q := transaction.StaleQuery{Statuses: []transaction.TransactionStatus{transaction.TxPrepared}, OlderThan: t3}
	out, err := s.FindStale(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("got %d stale records %+v, want 3", len(out), out)
	}
	for i, id := range []string{"tx-a", "tx-b", "tx-d"} {
		if out[i].ID != transaction.TransactionID(id) {
			t.Fatalf("position %d = %s, want %s (oldest-first, ID tie-break)", i, out[i].ID, id)
		}
	}

	q.Limit = 1
	out, err = s.FindStale(ctx, q)
	if err != nil || len(out) != 1 || out[0].ID != "tx-a" {
		t.Fatalf("limit: out=%+v err=%v", out, err)
	}
}

// TestStoreClaimAndRelease covers lease semantics (§10.4): validation,
// exclusivity while held, renewal by the same holder, takeover after
// expiry, owner-only release, and the claim leaving UpdatedAt untouched.
func TestStoreClaimAndRelease(t *testing.T) {
	ctx := context.Background()
	s := New()

	if _, err := s.Claim(ctx, "tx-9", transaction.Lease{Holder: "w1", At: t0, ExpiresAt: t1}); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("claim missing: err=%v", err)
	}
	if err := s.Release(ctx, "tx-9", "w1"); !errors.Is(err, transaction.ErrRecordNotFound) {
		t.Fatalf("release missing: err=%v", err)
	}

	leaseCases := []transaction.Lease{
		{Holder: "", At: t0, ExpiresAt: t1},
		{Holder: "w1", At: time.Time{}, ExpiresAt: t1},
		{Holder: "w1", At: t0, ExpiresAt: t0},
		{Holder: "w1", At: t0, ExpiresAt: time.Time{}},
	}
	for _, l := range leaseCases {
		if _, err := s.Claim(ctx, "tx-9", l); err == nil {
			t.Errorf("invalid lease %+v accepted", l)
		}
	}

	mustCreate(t, s, validTx("tx-1"))
	ok, err := s.Claim(ctx, "tx-1", transaction.Lease{Holder: "w1", At: t0, ExpiresAt: t1})
	if err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if got, _ := s.Get(ctx, "tx-1"); !got.UpdatedAt.Equal(t0) {
		t.Fatalf("claim touched UpdatedAt: %+v", got)
	}

	ok, err = s.Claim(ctx, "tx-1", transaction.Lease{Holder: "w2", At: t0.Add(time.Second), ExpiresAt: t2})
	if err != nil || ok {
		t.Fatalf("held lease stolen: ok=%v err=%v", ok, err)
	}
	ok, err = s.Claim(ctx, "tx-1", transaction.Lease{Holder: "w1", At: t0.Add(time.Second), ExpiresAt: t2})
	if err != nil || !ok {
		t.Fatalf("same-holder renewal rejected: ok=%v err=%v", ok, err)
	}
	ok, err = s.Claim(ctx, "tx-1", transaction.Lease{Holder: "w2", At: t2, ExpiresAt: t3})
	if err != nil || !ok {
		t.Fatalf("takeover after expiry rejected: ok=%v err=%v", ok, err)
	}

	if err := s.Release(ctx, "tx-1", "w1"); err != nil {
		t.Fatalf("stale worker release failed: %v", err)
	}
	ok, err = s.Claim(ctx, "tx-1", transaction.Lease{Holder: "w3", At: t2.Add(time.Second), ExpiresAt: t3})
	if err != nil || ok {
		t.Fatalf("w2 lease dropped by w1 release: ok=%v err=%v", ok, err)
	}
	if err := s.Release(ctx, "tx-1", "w2"); err != nil {
		t.Fatalf("owner release failed: %v", err)
	}
	ok, err = s.Claim(ctx, "tx-1", transaction.Lease{Holder: "w3", At: t2.Add(time.Second), ExpiresAt: t3})
	if err != nil || !ok {
		t.Fatalf("claim after owner release: ok=%v err=%v", ok, err)
	}
}
