// Package sql provides the SQL persistence implementation (spec §10,
// §11, ADR-0002): PostgreSQL via database/sql with explicit schema
// migrations, covering transaction records, idempotency keys, event
// records, and event cursors.
//
// The suites in tests/conformance/repository and tests/conformance/event
// must pass against this store before it ships; they run against a real
// PostgreSQL, gated by CHAME_TEST_PG_DSN.
package sql
