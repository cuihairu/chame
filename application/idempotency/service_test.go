package idempotency_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/chame/application/idempotency"
	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

var testNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "sepolia", ChainID: "11155111"}

var testScope = transaction.IdempotencyScope{Application: "game", OperationType: "payout"}

// fixedClock returns one frozen instant.
type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// builder fills the business fields of the seeded record.
func builder(tx transaction.Transaction) (transaction.Transaction, error) {
	tx.OperationID = "op-1"
	tx.Network = testNet
	return tx, nil
}

// countingRepo wraps a repository and can inject one synthetic idempotency
// conflict on the next keyed create, simulating a lost race.
type countingRepo struct {
	transaction.Repository
	creates       int
	conflictArmed bool
}

func (r *countingRepo) Create(ctx context.Context, tx transaction.Transaction) error {
	r.creates++
	if r.conflictArmed && tx.IdempotencyKey != "" {
		r.conflictArmed = false
		return transaction.IdempotencyConflict(tx.IdempotencyScope, tx.IdempotencyKey)
	}
	return r.Repository.Create(ctx, tx)
}

func newService(t *testing.T, at time.Time) (*idempotency.Service, *countingRepo) {
	t.Helper()
	base := memory.New()
	repo := &countingRepo{Repository: base}
	svc := idempotency.New(repo,
		idempotency.WithClock(fixedClock(at)),
		idempotency.WithNewID(func() transaction.TransactionID { return transaction.TransactionID("tx-seeded") }),
	)
	return svc, repo
}

func TestBeginCreatesOnMiss(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, at)

	tx, outcome, err := svc.Begin(context.Background(), testScope, "key-1", "hash-1", builder)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if outcome != idempotency.OutcomeCreated {
		t.Fatalf("want created, got %q", outcome)
	}
	if tx.ID != "tx-seeded" || tx.Status != transaction.TxCreated || !tx.CreatedAt.Equal(at) {
		t.Fatalf("seed not applied: %+v", tx)
	}
	if tx.Network.Family != identifiers.FamilyEVM || tx.OperationID != "op-1" {
		t.Fatalf("builder fields missing: %+v", tx)
	}
	if repo.creates != 1 {
		t.Fatalf("want one create, got %d", repo.creates)
	}
}

func TestBeginReusesSameRequest(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, at)
	ctx := context.Background()

	first, out1, err := svc.Begin(ctx, testScope, "key-1", "hash-1", builder)
	if err != nil || out1 != idempotency.OutcomeCreated {
		t.Fatalf("first begin: %v %q", err, out1)
	}

	replay, out2, err := svc.Begin(ctx, testScope, "key-1", "hash-1", builder)
	if err != nil {
		t.Fatalf("replay begin: %v", err)
	}
	if out2 != idempotency.OutcomeExisting {
		t.Fatalf("want existing, got %q", out2)
	}
	if replay.ID != first.ID {
		t.Fatalf("replay must return the first operation: %s vs %s", replay.ID, first.ID)
	}
	if repo.creates != 1 {
		t.Fatalf("replay must not create, got %d creates", repo.creates)
	}
}

func TestBeginConflictsOnDifferentRequest(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, at)
	ctx := context.Background()

	if _, _, err := svc.Begin(ctx, testScope, "key-1", "hash-1", builder); err != nil {
		t.Fatalf("first begin: %v", err)
	}
	_, _, err := svc.Begin(ctx, testScope, "key-1", "hash-DIFFERENT", builder)
	if !errors.Is(err, cherrs.ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
}

func TestBeginValidatesArguments(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, at)
	ctx := context.Background()

	cases := []struct {
		name  string
		scope transaction.IdempotencyScope
		key   string
		hash  string
	}{
		{"empty scope", transaction.IdempotencyScope{}, "key", "hash"},
		{"empty key", testScope, "", "hash"},
		{"empty hash", testScope, "key", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := svc.Begin(ctx, tc.scope, tc.key, tc.hash, builder)
			if !errors.Is(err, cherrs.ErrInvalidArgument) {
				t.Fatalf("want ErrInvalidArgument, got %v", err)
			}
		})
	}
}

func TestBeginRejectsBuilderMutation(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, at)

	steal := func(tx transaction.Transaction) (transaction.Transaction, error) {
		tx.IdempotencyKey = "hijacked"
		return tx, nil
	}
	_, _, err := svc.Begin(context.Background(), testScope, "key-1", "hash-1", steal)
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("want ErrInvalidArgument, got %v", err)
	}
}

func TestBeginPropagatesBuilderError(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, at)

	boom := errors.New("boom")
	fail := func(tx transaction.Transaction) (transaction.Transaction, error) {
		return transaction.Transaction{}, boom
	}
	_, _, err := svc.Begin(context.Background(), testScope, "key-1", "hash-1", fail)
	if !errors.Is(err, boom) {
		t.Fatalf("want builder error, got %v", err)
	}
}

func TestBeginResolvesLostRace(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, at)
	ctx := context.Background()

	// First operation lands.
	if _, _, err := svc.Begin(ctx, testScope, "key-1", "hash-1", builder); err != nil {
		t.Fatalf("first begin: %v", err)
	}
	// Now simulate the interleaving: find misses, then the create hits a
	// conflict because another caller won the key in between.
	repo.conflictArmed = true
	got, outcome, err := svc.Begin(ctx, testScope, "key-1", "hash-1", builder)
	if err != nil {
		t.Fatalf("raced begin: %v", err)
	}
	if outcome != idempotency.OutcomeExisting {
		t.Fatalf("want existing after race, got %q", outcome)
	}
	if got.IdempotencyKey != "key-1" {
		t.Fatalf("must return the stored record, got %+v", got)
	}
}

func TestBeginSurfacesConflictAfterLostRaceWithDifferentRequest(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, repo := newService(t, at)
	ctx := context.Background()

	if _, _, err := svc.Begin(ctx, testScope, "key-1", "hash-1", builder); err != nil {
		t.Fatalf("first begin: %v", err)
	}
	// The store reports a conflict (find missed) but the stored request
	// differs: the resolution must surface the conflict, not reuse.
	repo.conflictArmed = true
	_, _, err := svc.Begin(ctx, testScope, "key-1", "hash-DIFFERENT", builder)
	if !errors.Is(err, cherrs.ErrIdempotencyConflict) {
		t.Fatalf("want ErrIdempotencyConflict, got %v", err)
	}
}

func TestBeginConcurrentReplaysConvergeOnOneOperation(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	svc, _ := newService(t, at)
	ctx := context.Background()

	const n = 16
	results := make([]transaction.Transaction, n)
	outcomes := make([]idempotency.Outcome, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], outcomes[i], errs[i] = svc.Begin(ctx, testScope, "key-1", "hash-1", builder)
		}(i)
	}
	wg.Wait()

	created := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("begin %d: %v", i, errs[i])
		}
		if results[i].ID != results[0].ID {
			t.Fatalf("begin %d resolved to %s, first resolved to %s", i, results[i].ID, results[0].ID)
		}
		if outcomes[i] == idempotency.OutcomeCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("want exactly one created outcome, got %d", created)
	}
}
