// Package errors defines the Chame error taxonomy (spec §14): classified
// errors carrying retry hints and chain context, never collapsing an
// unknown outcome into a plain failure.
//
// Every category from spec §14.1 has a sentinel (ErrRequestTimeout, ...)
// so callers identify the class with errors.Is while the underlying cause
// stays reachable through errors.Unwrap. Retry policy is decided by
// operation semantics, never by error string matching (spec §14.2, §14.3).
package errors
