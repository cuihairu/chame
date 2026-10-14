package event_test

import (
	"context"
	"os"
	"testing"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/infrastructure/persistence/sql"
	events "github.com/cuihairu/chame/tests/conformance/event"
)

// TestSQLEventStoreConformance runs the shared event persistence suite
// against the PostgreSQL store (ADR-0002). It needs a real database and
// skips when CHAME_TEST_PG_DSN is unset: the SQL suite is an integration
// suite, never mixed into the unit-test run. CI provides the database as
// a service container.
func TestSQLEventStoreConformance(t *testing.T) {
	dsn := os.Getenv("CHAME_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set CHAME_TEST_PG_DSN to run the PostgreSQL event conformance suite")
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
	events.Run(t, func(t *testing.T) event.Repository {
		// Each subtest needs empty tables: the suite assumes fresh
		// state, exactly like memory.NewEventStore() provides.
		for _, table := range []string{`chame_events`, `chame_event_cursors`} {
			if _, err := store.DB().ExecContext(ctx, `DELETE FROM `+table); err != nil {
				t.Fatalf("reset %s: %v", table, err)
			}
		}
		return sql.NewEventStore(store.DB())
	})
}
