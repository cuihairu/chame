// Package idempotency implements the idempotency-key semantics (spec
// §10.1): one business operation per key, with persisted state that
// survives restarts. Begin turns the repository's uniqueness rules into
// the three outcomes an application handles: create, reuse, or conflict.
package idempotency
