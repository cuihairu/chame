// Package transaction implements the application-level transaction
// coordination (spec §9): driving the shared state machine across
// preparation, signing, submission, and recovery of unknown outcomes. The
// recovery worker (spec §10.4) scans for stuck transactions, leases them
// so multiple workers do not overlap, and advances each through a
// pluggable Reconciler under a bounded task timeout.
package transaction
