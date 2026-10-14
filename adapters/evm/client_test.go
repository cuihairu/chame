package evm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"

	cherrs "github.com/cuihairu/chame/core/errors"
)

func TestClassifyMapsTaxonomy(t *testing.T) {
	cases := []struct {
		name string
		raw  error
		want error
	}{
		{"context deadline", context.DeadlineExceeded, cherrs.ErrRequestTimeout},
		{"os deadline", os.ErrDeadlineExceeded, cherrs.ErrRequestTimeout},
		{"net timeout", netErr{msg: "i/o timeout", timeout: true}, cherrs.ErrRequestTimeout},
		{"net refused", netErr{msg: "connection refused"}, cherrs.ErrNetworkUnavailable},
		{"syscall refused", syscall.ECONNREFUSED, cherrs.ErrNetworkUnavailable},
		{"rate limited", &rpc.HTTPError{StatusCode: 429, Status: "429"}, cherrs.ErrRateLimited},
		{"bad auth", &rpc.HTTPError{StatusCode: 401, Status: "401"}, cherrs.ErrAuthenticationFailed},
		{"forbidden", &rpc.HTTPError{StatusCode: 403, Status: "403"}, cherrs.ErrPermissionDenied},
		{"server down", &rpc.HTTPError{StatusCode: 502, Status: "502"}, cherrs.ErrNetworkUnavailable},
		{"reverted call", dataErr{data: "0x"}, cherrs.ErrTransactionExecutionFailed},
		{"plain", fmt.Errorf("weird"), cherrs.ErrInternalError},
	}
	for _, c := range cases {
		err := classify("evm.balance_at", "evm/mainnet/1", c.raw)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: classify(%v) = %v, want %v", c.name, c.raw, err, c.want)
		}
	}
	// Caller cancellation is not a chain error and stays untouched.
	got := classify("op", "net", context.Canceled)
	if !errors.Is(got, context.Canceled) {
		t.Errorf("canceled should pass through, got %v", got)
	}
	if _, classified := got.(*cherrs.Error); classified {
		t.Error("canceled must not be classified")
	}
}

func TestClassifyCarriesContextAndCause(t *testing.T) {
	cause := fmt.Errorf("dial failed")
	err := classify("evm.balance_at", "evm/base/8453", cause)
	msg := err.Error()
	for _, want := range []string{"evm.balance_at", "evm/base/8453"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
	if !errors.Is(err, cause) {
		t.Error("cause must stay reachable")
	}
}

func TestFailoverEligible(t *testing.T) {
	transient := []error{
		classify("op", "n", context.DeadlineExceeded),
		classify("op", "n", netErr{msg: "refused"}),
		classify("op", "n", &rpc.HTTPError{StatusCode: 429}),
	}
	for _, err := range transient {
		if !failoverEligible(err) {
			t.Errorf("%v should be failover-eligible", err)
		}
	}
	deterministic := []error{
		classify("op", "n", dataErr{data: "0x"}),
		classify("op", "n", fmt.Errorf("weird")),
		nil,
		fmt.Errorf("raw"),
	}
	for _, err := range deterministic {
		if failoverEligible(err) {
			t.Errorf("%v must not be failover-eligible", err)
		}
	}
}
