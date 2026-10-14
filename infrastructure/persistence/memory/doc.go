// Package memory provides the in-memory persistence implementation
// (spec §10.2). It exists for unit tests and examples only; production
// transaction flows must not depend on it. The SQL implementation (§10.5,
// ADR-0002) is the durable path and must pass the same conformance suite.
package memory
