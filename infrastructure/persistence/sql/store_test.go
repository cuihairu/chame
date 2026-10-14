package sql_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
	if err := store.Create(ctx, tx); err != nil {
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
	err = store.Create(ctx, tx)
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
	if err := store.Create(ctx, mk()); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := store.Create(ctx, mk()); !errors.Is(err, cherrs.ErrPersistenceFailure) {
		t.Fatalf("want ErrPersistenceFailure for a duplicate keyless id, got %v", err)
	}
}
