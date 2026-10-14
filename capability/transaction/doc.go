// Package transaction defines the shared transaction model and state
// machine (spec §9): preparation, submission, and states that can express
// an unknown outcome, never a silent success or failure. The Repository
// contract (spec §10.2) pins how records persist: idempotency uniqueness,
// conditional status updates, and reconciliation leases.
package transaction
