// Package event holds the event persistence conformance suite: one suite
// that every event.Repository implementation must pass, so memory and SQL
// stores behave identically on identity uniqueness, conditional delivery
// updates, reorg invalidation, and cursor monotonicity. New
// implementations are plugged in by a thin _test.go runner; semantics
// live here exactly once.
package event
