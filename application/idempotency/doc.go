// Package idempotency implements the idempotency-key semantics (spec
// §10.1): one business operation per key, with persisted state that
// survives restarts. Begin turns the repository's uniqueness rules into
// the three outcomes an application handles: create, reuse, or conflict.
//
// Unlike application/transaction, this package is deliberately not a
// runtime module: Service holds no connections, goroutines, or lifecycle,
// so there is nothing for the runtime to start or stop. A host composes
// it as a library building block — construct Service on the same
// transaction.Repository the transaction module's Flow uses, and call
// Begin before that Flow to gate chain submissions on the key. Packages
// that register capabilities or background tasks belong to the module
// convention (Name/Info/Register); packages that only offer types the
// host wires itself, like this one, do not.
package idempotency
