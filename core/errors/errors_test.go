package errors

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestSentinelMatching(t *testing.T) {
	cause := fmt.Errorf("dial tcp: connection refused")
	e := &Error{Category: NetworkUnavailable, Op: "evm.balance_at", Network: "evm/mainnet/1", Err: cause}
	if !errors.Is(e, ErrNetworkUnavailable) {
		t.Error("errors.Is must match the category sentinel")
	}
	if errors.Is(e, ErrRequestTimeout) {
		t.Error("errors.Is must not match a different category")
	}
	if !errors.Is(e, cause) {
		t.Error("errors.Is must reach the cause")
	}
	if got := errors.Unwrap(e); got != cause {
		t.Errorf("Unwrap = %v, want cause", got)
	}
}

func TestMessageCarriesPhaseNotSecrets(t *testing.T) {
	e := &Error{Category: RequestTimeout, Op: "evm.header", Network: "evm/mainnet/1", Err: fmt.Errorf("context deadline exceeded")}
	msg := e.Error()
	for _, want := range []string{"evm.header", "evm/mainnet/1", "request_timeout"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "http") || strings.Contains(msg, "token=") {
		t.Errorf("message must not leak endpoint material: %q", msg)
	}
}

func TestWrapNilSafe(t *testing.T) {
	if Wrap(nil, InternalError, "op") != nil {
		t.Error("Wrap(nil) must return nil")
	}
}

func TestRetryHintMapping(t *testing.T) {
	cases := []struct {
		cat  Category
		want RetryHint
	}{
		{InvalidArgument, HintNever},
		{InvalidConfiguration, HintNever},
		{UnsupportedCapability, HintNever},
		{AuthenticationFailed, HintNever},
		{PermissionDenied, HintNever},
		{TransactionRejected, HintNever},
		{TransactionExecutionFailed, HintNever},
		{TransactionExpired, HintNever},
		{IdempotencyConflict, HintNever},
		{EventDecodeFailure, HintNever},
		{InternalError, HintNever},
		{RateLimited, HintBackoff},
		{RequestTimeout, HintIdempotent},
		{NetworkUnavailable, HintIdempotent},
		{TransactionStatusUnknown, HintReconcile},
		{PersistenceFailure, HintIdempotent},
		{EventProcessingFailure, HintIdempotent},
		{FinalityNotReached, HintIdempotent},
		{ReorgDetected, HintIdempotent},
	}
	for _, c := range cases {
		if got := c.cat.RetryHint(); got != c.want {
			t.Errorf("%s hint = %s, want %s", c.cat, got, c.want)
		}
	}
}

func TestNewFormatsMessage(t *testing.T) {
	e := New(InvalidArgument, "evm.call", "bad calldata: %d", 7)
	if !errors.Is(e, ErrInvalidArgument) {
		t.Error("New must classify")
	}
	if !strings.Contains(e.Error(), "bad calldata: 7") {
		t.Errorf("message = %q", e.Error())
	}
}
