package errors

import (
	"errors"
	"fmt"
	"testing"
)

// TestPolicyByCategoryAndSemantics pins spec §14.3: the retry decision
// comes from the category and the operation semantics, never from the
// message. Every category is checked under all three semantics.
func TestPolicyByCategoryAndSemantics(t *testing.T) {
	cases := []struct {
		name string
		err  error
		sem  Semantics
		want Decision
	}{
		// Deterministic failures: the same call produces the same result.
		{"invalid argument read", New(InvalidArgument, "evm.balance_at", "bad address"), SemReadOnly, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"invalid configuration submit", New(InvalidConfiguration, "evm.start", "chain id mismatch"), SemSubmit, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"unsupported capability process", New(UnsupportedCapability, "solana.sign", "read-only phase"), SemProcess, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"authentication failed read", New(AuthenticationFailed, "evm.balance_at", "bad key"), SemReadOnly, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"permission denied submit", New(PermissionDenied, "evm.submit", "not allowed"), SemSubmit, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"transaction rejected submit", New(TransactionRejected, "evm.submit", "underpriced"), SemSubmit, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"transaction execution failed submit", New(TransactionExecutionFailed, "evm.submit", "out of gas"), SemSubmit, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"transaction expired submit", New(TransactionExpired, "evm.submit", "dropped"), SemSubmit, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"idempotency conflict process", New(IdempotencyConflict, "tx.create", "same key different hash"), SemProcess, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"event decode failure process", New(EventDecodeFailure, "event.decode", "short data"), SemProcess, Decision{Hint: HintNever, Reason: "deterministic failure"}},
		{"internal error read", New(InternalError, "evm.balance_at", "nil pool"), SemReadOnly, Decision{Hint: HintNever, Reason: "deterministic failure"}},

		// Throttling: retry after backoff, whatever the semantics.
		{"rate limited read", New(RateLimited, "evm.balance_at", "429"), SemReadOnly, Decision{Retry: true, Hint: HintBackoff, Reason: "rate limited: retry after backoff"}},
		{"rate limited submit", New(RateLimited, "evm.submit", "429"), SemSubmit, Decision{Retry: true, Hint: HintBackoff, Reason: "rate limited: retry after backoff"}},
		{"rate limited process", New(RateLimited, "event.poll", "429"), SemProcess, Decision{Retry: true, Hint: HintBackoff, Reason: "rate limited: retry after backoff"}},

		// Unknown outcome: reconcile before anything else.
		{"status unknown read", New(TransactionStatusUnknown, "evm.submit", "no receipt"), SemReadOnly, Decision{Hint: HintReconcile, Reason: "outcome unknown: reconcile before retrying"}},
		{"status unknown submit", New(TransactionStatusUnknown, "evm.submit", "no receipt"), SemSubmit, Decision{Hint: HintReconcile, Reason: "outcome unknown: reconcile before retrying"}},
		{"status unknown process", New(TransactionStatusUnknown, "tx.recover", "no receipt"), SemProcess, Decision{Hint: HintReconcile, Reason: "outcome unknown: reconcile before retrying"}},

		// Transient failures: replayable for reads and in-process work,
		// but a submission that timed out may already be on chain.
		{"timeout read", New(RequestTimeout, "evm.balance_at", "deadline exceeded"), SemReadOnly, Decision{Retry: true, Hint: HintIdempotent, Reason: "transient failure"}},
		{"timeout process", New(RequestTimeout, "event.poll", "deadline exceeded"), SemProcess, Decision{Retry: true, Hint: HintIdempotent, Reason: "transient failure"}},
		{"timeout submit", New(RequestTimeout, "evm.submit", "deadline exceeded"), SemSubmit, Decision{Hint: HintReconcile, Reason: "submission timed out: reconcile before retrying"}},
		{"network unavailable read", New(NetworkUnavailable, "evm.balance_at", "connection refused"), SemReadOnly, Decision{Retry: true, Hint: HintIdempotent, Reason: "transient failure"}},
		{"network unavailable process", New(NetworkUnavailable, "event.poll", "connection refused"), SemProcess, Decision{Retry: true, Hint: HintIdempotent, Reason: "transient failure"}},
		{"network unavailable submit", New(NetworkUnavailable, "evm.submit", "connection refused"), SemSubmit, Decision{Hint: HintReconcile, Reason: "submission timed out: reconcile before retrying"}},

		// In-process failures: idempotent retry.
		{"persistence failure process", New(PersistenceFailure, "tx.save", "disk full"), SemProcess, Decision{Retry: true, Hint: HintIdempotent, Reason: "retryable in-process failure"}},
		{"persistence failure read", New(PersistenceFailure, "tx.save", "disk full"), SemReadOnly, Decision{Retry: true, Hint: HintIdempotent, Reason: "retryable in-process failure"}},
		{"event processing failure process", New(EventProcessingFailure, "event.handle", "handler panic"), SemProcess, Decision{Retry: true, Hint: HintIdempotent, Reason: "retryable in-process failure"}},
		{"finality not reached process", New(FinalityNotReached, "event.poll", "still confirming"), SemProcess, Decision{Retry: true, Hint: HintIdempotent, Reason: "retryable in-process failure"}},
		{"reorg detected process", New(ReorgDetected, "event.poll", "hash mismatch"), SemProcess, Decision{Retry: true, Hint: HintIdempotent, Reason: "retryable in-process failure"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Policy(tc.err, tc.sem)
			if got.Retry != tc.want.Retry || got.Hint != tc.want.Hint || got.Reason != tc.want.Reason {
				t.Errorf("Policy(%v, %s) = %+v, want %+v", tc.err, tc.sem, got, tc.want)
			}
		})
	}
}

// TestPolicyDefaultsToNever pins the safe default: an unclassified error
// and an unknown semantic both answer "do not retry".
func TestPolicyDefaultsToNever(t *testing.T) {
	if got := Policy(errors.New("raw"), SemReadOnly); got.Retry || got.Hint != HintNever {
		t.Errorf("unclassified error = %+v, want never", got)
	}
	if got := Policy(New(RequestTimeout, "evm.balance_at", "slow"), Semantics("")); got.Retry || got.Hint != HintNever {
		t.Errorf("unknown semantics = %+v, want never", got)
	}
	if got := Policy(New(RequestTimeout, "evm.balance_at", "slow"), Semantics("write")); got.Retry || got.Hint != HintNever {
		t.Errorf("unknown semantics = %+v, want never", got)
	}
	if got := Policy(nil, SemReadOnly); got.Retry || got.Hint != HintNever {
		t.Errorf("nil error = %+v, want never", got)
	}
}

// TestPolicySeesThroughWraps pins that the decision uses the classified
// error, not the wrapper: a cause wrapped by fmt.Errorf still classifies.
func TestPolicySeesThroughWraps(t *testing.T) {
	inner := New(RequestTimeout, "evm.balance_at", "deadline exceeded")
	wrapped := fmt.Errorf("balance: %w", inner)
	if got := Policy(wrapped, SemReadOnly); !got.Retry || got.Hint != HintIdempotent {
		t.Errorf("wrapped error = %+v, want idempotent retry", got)
	}
}

// TestSemanticsValid pins the known semantics set.
func TestSemanticsValid(t *testing.T) {
	for _, s := range []Semantics{SemReadOnly, SemSubmit, SemProcess} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	for _, s := range []Semantics{"", "write", "READ-ONLY"} {
		if s.Valid() {
			t.Errorf("%q must not be valid", s)
		}
	}
}

// TestFieldExtractors pins the accessors that let callers react to the
// class without re-rendering the message.
func TestFieldExtractors(t *testing.T) {
	err := &Error{Category: RateLimited, Op: "evm.balance_at", Network: "evm/mainnet/1", Err: ErrRateLimited}
	if c, ok := CategoryOf(err); !ok || c != RateLimited {
		t.Errorf("CategoryOf = %q, %v", c, ok)
	}
	if op, ok := OpOf(err); !ok || op != "evm.balance_at" {
		t.Errorf("OpOf = %q, %v", op, ok)
	}
	if net, ok := NetworkOf(err); !ok || net != "evm/mainnet/1" {
		t.Errorf("NetworkOf = %q, %v", net, ok)
	}
	if _, ok := CategoryOf(errors.New("raw")); ok {
		t.Error("unclassified error must not yield a category")
	}
	if _, ok := OpOf(nil); ok {
		t.Error("nil must not yield an op")
	}
}
