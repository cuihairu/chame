package errors

import (
	"errors"
	"fmt"
)

// Category classifies an error into the stable taxonomy (spec §14.1).
// Categories are stable identifiers; messages are not.
type Category string

// The categories of spec §14.1.
const (
	InvalidArgument            Category = "invalid_argument"
	InvalidConfiguration       Category = "invalid_configuration"
	UnsupportedCapability      Category = "unsupported_capability"
	NetworkUnavailable         Category = "network_unavailable"
	RequestTimeout             Category = "request_timeout"
	RateLimited                Category = "rate_limited"
	AuthenticationFailed       Category = "authentication_failed"
	PermissionDenied           Category = "permission_denied"
	TransactionRejected        Category = "transaction_rejected"
	TransactionExecutionFailed Category = "transaction_execution_failed"
	TransactionExpired         Category = "transaction_expired"
	TransactionStatusUnknown   Category = "transaction_status_unknown"
	IdempotencyConflict        Category = "idempotency_conflict"
	PersistenceFailure         Category = "persistence_failure"
	EventDecodeFailure         Category = "event_decode_failure"
	EventProcessingFailure     Category = "event_processing_failure"
	FinalityNotReached         Category = "finality_not_reached"
	ReorgDetected              Category = "reorg_detected"
	InternalError              Category = "internal_error"
)

// Sentinels, one per category. errors.Is(err, errors.ErrRequestTimeout)
// reports the class of any classified error, whatever its cause.
var (
	ErrInvalidArgument            = errors.New("invalid argument")
	ErrInvalidConfiguration       = errors.New("invalid configuration")
	ErrUnsupportedCapability      = errors.New("unsupported capability")
	ErrNetworkUnavailable         = errors.New("network unavailable")
	ErrRequestTimeout             = errors.New("request timeout")
	ErrRateLimited                = errors.New("rate limited")
	ErrAuthenticationFailed       = errors.New("authentication failed")
	ErrPermissionDenied           = errors.New("permission denied")
	ErrTransactionRejected        = errors.New("transaction rejected")
	ErrTransactionExecutionFailed = errors.New("transaction execution failed")
	ErrTransactionExpired         = errors.New("transaction expired")
	ErrTransactionStatusUnknown   = errors.New("transaction status unknown")
	ErrIdempotencyConflict        = errors.New("idempotency conflict")
	ErrPersistenceFailure         = errors.New("persistence failure")
	ErrEventDecodeFailure         = errors.New("event decode failure")
	ErrEventProcessingFailure     = errors.New("event processing failure")
	ErrFinalityNotReached         = errors.New("finality not reached")
	ErrReorgDetected              = errors.New("reorg detected")
	ErrInternalError              = errors.New("internal error")
)

var sentinelByCategory = map[Category]error{
	InvalidArgument:            ErrInvalidArgument,
	InvalidConfiguration:       ErrInvalidConfiguration,
	UnsupportedCapability:      ErrUnsupportedCapability,
	NetworkUnavailable:         ErrNetworkUnavailable,
	RequestTimeout:             ErrRequestTimeout,
	RateLimited:                ErrRateLimited,
	AuthenticationFailed:       ErrAuthenticationFailed,
	PermissionDenied:           ErrPermissionDenied,
	TransactionRejected:        ErrTransactionRejected,
	TransactionExecutionFailed: ErrTransactionExecutionFailed,
	TransactionExpired:         ErrTransactionExpired,
	TransactionStatusUnknown:   ErrTransactionStatusUnknown,
	IdempotencyConflict:        ErrIdempotencyConflict,
	PersistenceFailure:         ErrPersistenceFailure,
	EventDecodeFailure:         ErrEventDecodeFailure,
	EventProcessingFailure:     ErrEventProcessingFailure,
	FinalityNotReached:         ErrFinalityNotReached,
	ReorgDetected:              ErrReorgDetected,
	InternalError:              ErrInternalError,
}

// RetryHint states how a caller may respond to an error (spec §14.3).
// The hint is a default for the category; the caller who knows the
// operation semantics makes the final call.
type RetryHint string

const (
	// HintNever marks deterministic errors: argument, configuration,
	// permission, and rejection classes. Retrying cannot change the
	// outcome.
	HintNever RetryHint = "never"
	// HintBackoff marks throttling: retry after honoring Retry-After or
	// a backoff schedule.
	HintBackoff RetryHint = "backoff"
	// HintIdempotent marks errors where an idempotent retry is safe:
	// read-only timeouts, transient unavailability. A transaction
	// submission that times out must NOT use this path — it classifies
	// as TransactionStatusUnknown and reconciles instead (spec §14.3).
	HintIdempotent RetryHint = "idempotent"
	// HintReconcile marks unknown outcomes: determine what happened on
	// chain before doing anything else.
	HintReconcile RetryHint = "reconcile"
)

// RetryHint returns the default response for the category (spec §14.3).
func (c Category) RetryHint() RetryHint {
	switch c {
	case RateLimited:
		return HintBackoff
	case RequestTimeout, NetworkUnavailable:
		return HintIdempotent // only under read semantics; see HintIdempotent
	case TransactionStatusUnknown:
		return HintReconcile
	case PersistenceFailure, EventProcessingFailure, FinalityNotReached, ReorgDetected:
		return HintIdempotent
	default:
		return HintNever
	}
}

// Error is a classified error. It preserves the cause for errors.Unwrap
// and matches its category sentinel for errors.Is. Messages carry the
// operation phase and logical network, never endpoint URLs or other
// secret-bearing material (spec §5.4, §14.2).
type Error struct {
	// Category is the classification.
	Category Category
	// Op names the operation phase, e.g. "evm.balance_at".
	Op string
	// Network is the logical network identifier, when known.
	Network string
	// Err is the underlying cause. Never nil.
	Err error
}

// Error renders "op [on network]: category: cause".
func (e *Error) Error() string {
	var where string
	if e.Network != "" {
		where = " on " + e.Network
	}
	if e.Op != "" {
		return fmt.Sprintf("%s%s: %s: %v", e.Op, where, e.Category, e.Err)
	}
	return fmt.Sprintf("%s%s: %v", e.Category, where, e.Err)
}

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.Err }

// Is reports whether target is the sentinel of this error's category.
func (e *Error) Is(target error) bool {
	s, ok := sentinelByCategory[e.Category]
	return ok && s == target
}

// RetryHint returns the default retry response for this error's category.
func (e *Error) RetryHint() RetryHint { return e.Category.RetryHint() }

// Wrap classifies err for operation op. It returns nil when err is nil,
// so it composes with defers and returns.
func Wrap(err error, category Category, op string) *Error {
	if err == nil {
		return nil
	}
	return &Error{Category: category, Op: op, Err: err}
}

// New builds a classified error with a formatted message.
func New(category Category, op string, format string, args ...any) *Error {
	return &Error{Category: category, Op: op, Err: fmt.Errorf(format, args...)}
}
