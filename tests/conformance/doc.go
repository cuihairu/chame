// Package conformance hosts the shared conformance suites: the adapter
// suite (spec §16.2) that every chain adapter must pass, and the
// persistence suites (ADR-0002) that every transaction.Repository and
// event.Repository implementation must pass, so memory and SQL stores
// behave identically.
package conformance
