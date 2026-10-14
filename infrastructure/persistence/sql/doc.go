// Package sql provides the SQL persistence implementation (spec §10,
// ADR-0002): PostgreSQL via database/sql with explicit schema migrations,
// covering transaction records, idempotency keys, and event cursors.
//
// Status: skeleton. Implementation lands with the phased plan in todo.md.
package sql
