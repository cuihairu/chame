package sql_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/infrastructure/persistence/sql"
)

// testDSN reads the PostgreSQL DSN for integration runs (ADR-0002). The
// SQL suite skips when unset: it needs a real PostgreSQL and is never
// mixed into the unit-test run.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("CHAME_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAME_TEST_PG_DSN to run the PostgreSQL suite")
	}
	return dsn
}

// isolatedDSN creates a fresh throwaway schema on the test database and
// returns a DSN pointed at it, so a destructive setup never touches
// tables another suite shares. The caller drops the schema when the test
// ends.
func isolatedDSN(t *testing.T, base string, ctx context.Context) (string, string) {
	t.Helper()
	schema := fmt.Sprintf("chame_sql_test_%d", time.Now().UnixNano())
	// Create the schema through the base DSN first: a search_path that
	// names a schema which does not exist yet is rejected by PostgreSQL.
	bootstrap, err := sql.Open(ctx, base)
	if err != nil {
		t.Fatalf("open bootstrap: %v", err)
	}
	defer func() { _ = bootstrap.Close() }()
	if _, err := bootstrap.DB().ExecContext(ctx,
		`CREATE SCHEMA `+schema+` AUTHORIZATION CURRENT_USER`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	options := url.QueryEscape("-c search_path=" + schema)
	options = strings.ReplaceAll(options, "+", "%20")
	return base + "&options=" + options, schema
}

// reset removes every record so runs stay repeatable against a shared
// database. Migrations persist by design.
func reset(t *testing.T, store *sql.Store) {
	t.Helper()
	if _, err := store.DB().ExecContext(context.Background(), `DELETE FROM chame_transactions`); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func TestStoreMigrateIsIdempotent(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	store, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	rows, err := store.DB().QueryContext(ctx,
		`SELECT version, COUNT(*) FROM chame_schema_migrations GROUP BY version`)
	if err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var version string
		var n int
		if err := rows.Scan(&version, &n); err != nil {
			t.Fatalf("scan migration: %v", err)
		}
		counts[version] = n
	}
	if rows.Err() != nil {
		t.Fatalf("rows: %v", rows.Err())
	}
	if counts["0001_transactions"] != 1 {
		t.Fatalf("0001 must be applied exactly once, got %+v", counts)
	}
	if counts["0002_events"] != 1 {
		t.Fatalf("0002 must be applied exactly once, got %+v", counts)
	}
}

// TestStoreMigrateIsConcurrencySafe pins the migration advisory lock: two
// instances migrating at once converge on one application instead of
// racing on the version primary key. The shared test database already has
// the schema applied, so this exercises the lock path both instances take
// (the first-apply race is closed by the same lock, asserted against the
// version table by TestStoreMigrateIsIdempotent).
func TestStoreMigrateIsConcurrencySafe(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	a, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = a.Migrate(ctx) }()
	go func() { defer wg.Done(); errs[1] = b.Migrate(ctx) }()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate %d: %v", i, err)
		}
	}
}

// TestStoreMigrateIsConcurrencySafeOnFirstStart pins the migration
// advisory lock against the path the existing concurrency test cannot
// reach: two instances starting on a virgin database (no schema at all).
// Concurrent CREATE TABLE IF NOT EXISTS is not atomic in PostgreSQL, so
// without the lock one instance fails on a duplicate catalog entry; with
// it both converge on one application. The lock also serializes the
// version check-and-record that TestStoreMigrateIsIdempotent pins.
func TestStoreMigrateIsConcurrencySafeOnFirstStart(t *testing.T) {
	base := testDSN(t)
	ctx := context.Background()
	dsn, schema := isolatedDSN(t, base, ctx)

	// The throwaway schema starts empty: both instances observe a virgin
	// database, exactly the first-start state the lock protects. Other
	// suites sharing the test database are never touched.
	setup, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open setup: %v", err)
	}
	t.Cleanup(func() {
		_, _ = setup.DB().ExecContext(ctx, `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
		_ = setup.Close()
	})

	a, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open a: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	b, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open b: %v", err)
	}
	t.Cleanup(func() { _ = b.Close() })

	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); errs[0] = a.Migrate(ctx) }()
	go func() { defer wg.Done(); errs[1] = b.Migrate(ctx) }()
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent first-start migrate %d: %v", i, err)
		}
	}

	// Both instances must observe the schema applied exactly once.
	var n int
	if err := setup.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chame_schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	want := sql.MigrationCount()
	if n != want {
		t.Fatalf("applied migrations = %d, want %d", n, want)
	}
}

func TestStoreOpenRejectsAnUnreachableDSN(t *testing.T) {
	ctx := context.Background()
	_, err := sql.Open(ctx, "postgres://nobody:nope@127.0.0.1:9/chame?sslmode=disable&connect_timeout=2")
	if err == nil {
		t.Fatalf("an unreachable DSN must fail")
	}
}

func TestStoreRoundTripsAFullRecord(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	store, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	reset(t, store)

	at := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC).UTC()
	sender, err := identifiers.NewEVMAddress("0x4a04e43f378f803b42f3d6b28f016a5b372075d1")
	if err != nil {
		t.Fatalf("sender: %v", err)
	}
	amount, err := identifiers.NewAmount("123456789", 6, "ETH")
	if err != nil {
		t.Fatalf("amount: %v", err)
	}
	tx := transaction.Transaction{
		ID:          "tx-roundtrip",
		OperationID: "op-roundtrip",
		Network: identifiers.NetworkID{
			Family:  identifiers.FamilyEVM,
			Name:    "sepolia",
			ChainID: "11155111",
		},
		Status:         transaction.TxPrepared,
		IdempotencyKey: "key-roundtrip",
		IdempotencyScope: transaction.IdempotencyScope{
			Application:   "game",
			OperationType: "payout",
		},
		RequestHash: "hash-roundtrip",
		Sender:      sender,
		Recipient:   sender,
		Amount:      amount,
		ChainHash:   "0xdeadbeef",
		ChainData:   map[string]any{"kind": "evm", "gas": float64(21000)},
		CreatedAt:   at,
		UpdatedAt:   at.Add(time.Second),
	}
	if _, err := store.Create(ctx, tx); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Get(ctx, "tx-roundtrip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.ID != tx.ID || got.OperationID != tx.OperationID || got.Status != tx.Status {
		t.Fatalf("identity mismatch: %+v", got)
	}
	if got.Network != tx.Network {
		t.Fatalf("network: %+v vs %+v", got.Network, tx.Network)
	}
	if got.IdempotencyScope != tx.IdempotencyScope || got.IdempotencyKey != tx.IdempotencyKey || got.RequestHash != tx.RequestHash {
		t.Fatalf("idempotency fields: %+v", got)
	}
	if got.Sender != tx.Sender || got.Recipient != tx.Recipient {
		t.Fatalf("addresses: %+v vs %+v", got.Sender, tx.Sender)
	}
	if got.Amount != tx.Amount {
		t.Fatalf("amount: %+v vs %+v", got.Amount, tx.Amount)
	}
	if got.ChainHash != tx.ChainHash {
		t.Fatalf("chain hash: %q vs %q", got.ChainHash, tx.ChainHash)
	}
	if !got.CreatedAt.Equal(tx.CreatedAt) || !got.UpdatedAt.Equal(tx.UpdatedAt) {
		t.Fatalf("timestamps: %+v vs %+v", got.CreatedAt, tx.CreatedAt)
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
		t.Fatalf("chain data changed: want %s, have %s", want, have)
	}
}

func TestStoreRejectsNonEncodableChainData(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	store, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	reset(t, store)

	tx := transaction.Transaction{
		ID:        "tx-odd-data",
		Network:   identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "sepolia", ChainID: "11155111"},
		Status:    transaction.TxCreated,
		ChainData: make(chan int),
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	_, err = store.Create(ctx, tx)
	if !errors.Is(err, cherrs.ErrPersistenceFailure) {
		t.Fatalf("want ErrPersistenceFailure, got %v", err)
	}
}

func TestStoreRejectsADuplicateKeylessId(t *testing.T) {
	dsn := testDSN(t)
	ctx := context.Background()
	store, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	reset(t, store)

	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mk := func() transaction.Transaction {
		return transaction.Transaction{
			ID:        "tx-same-id",
			Network:   identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "sepolia", ChainID: "11155111"},
			Status:    transaction.TxCreated,
			CreatedAt: at,
			UpdatedAt: at,
		}
	}
	if _, err := store.Create(ctx, mk()); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := store.Create(ctx, mk()); !errors.Is(err, cherrs.ErrPersistenceFailure) {
		t.Fatalf("want ErrPersistenceFailure for a duplicate keyless id, got %v", err)
	}
}
