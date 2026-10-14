// Package conformance hosts the shared conformance suites: the adapter
// suite (spec §16.2) that every chain adapter must pass, and the
// persistence suite (ADR-0002) that every transaction.Repository
// implementation must pass, so memory and SQL stores behave identically.
//
// Status: skeleton. Cases are added with the phased plan in todo.md.
package conformance
