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
	"github.com/jackc/pgx/v5"
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
	// Create the schema through the base DSN first. PostgreSQL accepts a
	// search_path that names a schema which does not exist yet (the GUC
	// is set and the connection succeeds), but an unqualified CREATE then
	// fails with "no schema has been selected to create in"; creating the
	// schema ahead of time lets the isolated connection's unqualified
	// CREATE/DELETE statements resolve into the target schema.
	bootstrap, err := sql.Open(ctx, base)
	if err != nil {
		t.Fatalf("open bootstrap: %v", err)
	}
	defer func() { _ = bootstrap.Close() }()
	if _, err := bootstrap.DB().ExecContext(ctx,
		`CREATE SCHEMA `+schema+` AUTHORIZATION CURRENT_USER`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return isolatedDSNOptions(base, schema), schema
}

// isolatedDSNOptions appends an options parameter pointing the connection
// at the throwaway schema. It parses the base DSN so it works whether or
// not the base already carries a query string (a bare postgres://host/db
// must not be concatenated with "&", which would fold the options into the
// database name).
func isolatedDSNOptions(base, schema string) string {
	options := "-c search_path=" + schema
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" {
		// net/url cannot model a pgx keyword/value conninfo (no scheme),
		// and pgx splits such DSNs on whitespace only: '?' and '&' are
		// ordinary characters, so a URL query string would fold into the
		// preceding token (the database name) and isolation would silently
		// be lost. Append an options keyword with a single-quoted value
		// instead; pgx unquotes it back into RuntimeParams["options"]. The
		// value is quoted with a single quote and embedded quotes are
		// escaped with a backslash, which is the only escape pgx's quoted
		// keyword/value parser recognizes (a doubled quote '' is an error).
		quoted := strings.ReplaceAll(options, "'", `\'`)
		return base + " options='" + quoted + "'"
	}
	escaped := url.QueryEscape(options)
	escaped = strings.ReplaceAll(escaped, "+", "%20")
	if u.RawQuery == "" {
		u.RawQuery = "options=" + escaped
	} else {
		u.RawQuery = u.RawQuery + "&options=" + escaped
	}
	return u.String()
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

// TestIsolatedDSNOptions pins the DSN building against a base that already
// carries a query string, a bare postgres:// URL, and a pgx keyword/value
// conninfo: the options must land in the query string (URL form) or as a
// quoted options keyword (conninfo form), never be folded into the database
// name, and must survive a pgx.ParseConfig round-trip.
func TestIsolatedDSNOptions(t *testing.T) {
	const schema = "chame_sql_test_probe"
	cases := []struct {
		name string
		base string
		want string
		kv   bool // keyword/value (conninfo) form, not a URL
	}{
		{
			name: "with existing query string",
			base: "postgres://user:pw@localhost:5432/db?sslmode=disable",
			want: "postgres://user:pw@localhost:5432/db?sslmode=disable&options=-c%20search_path%3Dchame_sql_test_probe",
		},
		{
			name: "bare postgres URL",
			base: "postgres://user:pw@localhost:5432/db",
			want: "postgres://user:pw@localhost:5432/db?options=-c%20search_path%3Dchame_sql_test_probe",
		},
		{
			name: "keyword value conninfo",
			base: "host=localhost user=foo dbname=bar sslmode=disable",
			want: "host=localhost user=foo dbname=bar sslmode=disable options='-c search_path=chame_sql_test_probe'",
			kv:   true,
		},
		{
			name: "keyword value conninfo with existing options",
			base: "host=localhost user=foo dbname=bar options='-c search_path=base'",
			want: "host=localhost user=foo dbname=bar options='-c search_path=base' options='-c search_path=chame_sql_test_probe'",
			kv:   true,
		},
		{
			name: "URL with existing options",
			base: "postgres://user:pw@localhost:5432/db?options=-c%20search_path%3Dbase",
			want: "postgres://user:pw@localhost:5432/db?options=-c%20search_path%3Dbase&options=-c%20search_path%3Dchame_sql_test_probe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isolatedDSNOptions(tc.base, schema)
			if got != tc.want {
				t.Fatalf("isolatedDSNOptions(%q) = %q, want %q", tc.base, got, tc.want)
			}
			// Every form is consumed by pgx: the URL branch goes through
			// pgx stdlib (sql.Open -> pgconn.ParseConfig), so assert the
			// exact RuntimeParams["options"] after a ParseConfig round-trip
			// rather than only a net/url shape check. pgx's URL query parsing
			// is last-wins for a repeated key, so the appended options (the
			// isolation search_path) must win over any base options value.
			cfg, err := pgx.ParseConfig(got)
			if err != nil {
				t.Fatalf("pgx.ParseConfig(%q): %v", got, err)
			}
			if cfg.Database != "db" && cfg.Database != "bar" {
				t.Fatalf("database = %q, want db or bar", cfg.Database)
			}
			if cfg.RuntimeParams["options"] != "-c search_path="+schema {
				t.Fatalf("options = %q, want %q", cfg.RuntimeParams["options"], "-c search_path="+schema)
			}
		})
	}
}

// TestIsolatedDSNOptionsEscapesQuote pins the keyword/value escaping
// against a schema containing a single quote: pgx's quoted-value parser
// only honors a backslash escape, so a doubled quote would make the DSN
// unparseable. This is the path the reviewer flagged as untested.
func TestIsolatedDSNOptionsEscapesQuote(t *testing.T) {
	const schema = "chame_sql_test_'probe"
	got := isolatedDSNOptions("host=localhost dbname=bar", schema)
	cfg, err := pgx.ParseConfig(got)
	if err != nil {
		t.Fatalf("pgx.ParseConfig(%q): %v", got, err)
	}
	if cfg.RuntimeParams["options"] != "-c search_path="+schema {
		t.Fatalf("options = %q, want %q", cfg.RuntimeParams["options"], "-c search_path="+schema)
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
