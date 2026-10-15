package errors

import "errors"

// Semantics says what the operation that failed does (spec §14.3). The
// same error warrants different responses: a read-only timeout may be
// replayed, a submission timeout may not — the transaction may already be
// on chain, so reconciliation comes first. Retry decisions therefore need
// both the category and the semantics.
type Semantics string

const (
	// SemReadOnly marks an operation that produces no on-chain effect.
	SemReadOnly Semantics = "read-only"
	// SemSubmit marks an operation that submits a transaction. Its
	// outcome may already be on chain.
	SemSubmit Semantics = "submit"
	// SemProcess marks in-process work over already-observed data:
	// event handling, persistence, recovery.
	SemProcess Semantics = "process"
)

// Valid reports whether s is a known operation semantic.
func (s Semantics) Valid() bool {
	switch s {
	case SemReadOnly, SemSubmit, SemProcess:
		return true
	default:
		return false
	}
}

// Decision is the retry decision for one failed operation (spec §14.3).
type Decision struct {
	// Retry reports whether repeating the operation is safe.
	Retry bool
	// Hint is the category's default response, kept so a caller can
	// apply its own backoff schedule.
	Hint RetryHint
	// Reason is a short explanation for logs. Callers must not branch on
	// it: branch on Retry and Hint.
	Reason string
}

// Policy decides whether a failed operation may be repeated, given the
// operation's semantics (spec §14.3). The decision comes from the error
// category and the semantics, never from the error message: retry policy
// is an operation property, not a string property.
//
// An unclassified error, and an unknown semantic, both answer "do not
// retry" — the safe default when the framework cannot speak for the
// operation.
func Policy(err error, sem Semantics) Decision {
	if !sem.Valid() {
		return Decision{Hint: HintNever, Reason: "unknown operation semantics"}
	}
	var e *Error
	if !errors.As(err, &e) {
		return Decision{Hint: HintNever, Reason: "unclassified error"}
	}
	switch e.Category {
	case InvalidArgument, InvalidConfiguration, UnsupportedCapability,
		AuthenticationFailed, PermissionDenied,
		TransactionRejected, TransactionExecutionFailed, TransactionExpired,
		IdempotencyConflict, EventDecodeFailure, InternalError:
		// Deterministic failures and business outcomes: the same call
		// produces the same result, and business outcomes are for the
		// application to compensate, not for the framework to replay.
		return Decision{Hint: HintNever, Reason: "deterministic failure"}
	case RateLimited:
		return Decision{Retry: true, Hint: HintBackoff, Reason: "rate limited: retry after backoff"}
	case TransactionStatusUnknown:
		return Decision{Hint: HintReconcile, Reason: "outcome unknown: reconcile before retrying"}
	case RequestTimeout, NetworkUnavailable:
		if sem == SemSubmit {
			// A submission that timed out may already be on chain. Never
			// assume it was not sent: reconcile first (spec §14.3).
			return Decision{Hint: HintReconcile, Reason: "submission timed out: reconcile before retrying"}
		}
		return Decision{Retry: true, Hint: HintIdempotent, Reason: "transient failure"}
	case PersistenceFailure, EventProcessingFailure, FinalityNotReached, ReorgDetected:
		return Decision{Retry: true, Hint: HintIdempotent, Reason: "retryable in-process failure"}
	default:
		return Decision{Hint: HintNever, Reason: "unclassified category"}
	}
}

// CategoryOf returns the taxonomy category of err, or "" when err is not a
// classified error. It exists so callers can react to the class without
// re-rendering the message.
func CategoryOf(err error) (Category, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return "", false
	}
	return e.Category, true
}

// OpOf returns the operation phase of a classified error.
func OpOf(err error) (string, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return "", false
	}
	return e.Op, true
}

// NetworkOf returns the logical network of a classified error.
func NetworkOf(err error) (string, bool) {
	var e *Error
	if !errors.As(err, &e) {
		return "", false
	}
	return e.Network, true
}
