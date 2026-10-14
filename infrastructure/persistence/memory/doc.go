// Package memory provides the in-memory persistence implementations
// (spec §10.2 and §11). They exist for unit tests and examples only;
// production transaction and event flows must not depend on them. The SQL
// implementation (§10.5, ADR-0002) is the durable path and must pass the
// same conformance suites.
package memory
