// Package sql provides the SQL persistence implementation (spec §10,
// ADR-0002): PostgreSQL via database/sql with explicit schema migrations,
// covering transaction records, idempotency keys, and event cursors.
//
// The suite in tests/conformance/repository must pass against this store
// before it ships; it runs against a real PostgreSQL, gated by
// CHAME_TEST_PG_DSN.
package sql
