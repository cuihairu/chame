package repository_test

import (
	"context"
	"os"
	"testing"

	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/infrastructure/persistence/sql"
	repository "github.com/cuihairu/chame/tests/conformance/repository"
)

// TestSQLStoreConformance runs the shared persistence suite against the
// PostgreSQL store (ADR-0002). It needs a real database and skips when
// CHAME_TEST_PG_DSN is unset: the SQL suite is an integration suite, never
// mixed into the unit-test run. CI provides the database as a service
// container.
func TestSQLStoreConformance(t *testing.T) {
	dsn := os.Getenv("CHAME_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAME_TEST_PG_DSN to run the PostgreSQL conformance suite")
	}
	ctx := context.Background()
	store, err := sql.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	repository.Run(t, func(t *testing.T) transaction.Repository {
		// Each subtest needs an empty store: the suite assumes fresh
		// state, exactly like memory.New() provides.
		if _, err := store.DB().ExecContext(ctx, `DELETE FROM chame_transactions`); err != nil {
			t.Fatalf("reset: %v", err)
		}
		return store
	})
}
