package solana

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	cherrs "github.com/cuihairu/chame/core/errors"
)

func TestClassifyContextCanceledPassesThrough(t *testing.T) {
	err := classify("op", "net", context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must pass through, got %v", err)
	}
	var ce *cherrs.Error
	if errors.As(err, &ce) {
		t.Fatalf("cancellation must not be classified, got %v", err)
	}
}

func TestClassifyDeadlineAndNetErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want cherrs.Category
	}{
		{"context deadline", context.DeadlineExceeded, cherrs.RequestTimeout},
		{"os deadline", os.ErrDeadlineExceeded, cherrs.RequestTimeout},
		{"net timeout", netErr{msg: "i/o timeout", timeout: true}, cherrs.RequestTimeout},
		{"net refused", netErr{msg: "connection reset"}, cherrs.NetworkUnavailable},
		{"conn refused", syscall.ECONNREFUSED, cherrs.NetworkUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classify("op", "net", tc.err)
			var ce *cherrs.Error
			if !errors.As(got, &ce) {
				t.Fatalf("want classified error, got %T", got)
			}
			if ce.Category != tc.want {
				t.Fatalf("want %s, got %s", tc.want, ce.Category)
			}
			if !errors.Is(got, tc.err) {
				t.Fatalf("cause must stay in the chain: %v", got)
			}
		})
	}
}

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		code int
		want cherrs.Category
	}{
		{429, cherrs.RateLimited},
		{401, cherrs.AuthenticationFailed},
		{403, cherrs.PermissionDenied},
		{502, cherrs.NetworkUnavailable},
		{503, cherrs.NetworkUnavailable},
		{400, cherrs.InternalError},
	}
	for _, tc := range cases {
		raw := jsonrpc.NewHTTPError(tc.code, fmt.Errorf("status %d", tc.code))
		got := classify("op", "net", raw)
		var ce *cherrs.Error
		if !errors.As(got, &ce) {
			t.Fatalf("code %d: want classified error, got %T", tc.code, got)
		}
		if ce.Category != tc.want {
			t.Fatalf("code %d: want %s, got %s", tc.code, tc.want, ce.Category)
		}
	}
}

func TestClassifyRPCErrorIsNotTransient(t *testing.T) {
	raw := &jsonrpc.RPCError{Code: -32602, Message: "Invalid paramaters"}
	got := classify("op", "net", raw)
	var ce *cherrs.Error
	if !errors.As(got, &ce) {
		t.Fatalf("want classified error, got %T", got)
	}
	if ce.Category != cherrs.InternalError {
		t.Fatalf("want internal error, got %s", ce.Category)
	}
	if failoverEligible(got) {
		t.Fatal("a rejected request must not justify a failover")
	}
	if !errors.Is(got, raw) {
		t.Fatal("the rpc error must stay in the chain")
	}
}

func TestFailoverEligible(t *testing.T) {
	eligible := []cherrs.Category{cherrs.NetworkUnavailable, cherrs.RequestTimeout, cherrs.RateLimited}
	for _, cat := range eligible {
		if !failoverEligible(of(cat, "op", "net", fmt.Errorf("x"))) {
			t.Fatalf("%s must be failover eligible", cat)
		}
	}
	notEligible := []cherrs.Category{
		cherrs.InvalidArgument, cherrs.InvalidConfiguration, cherrs.AuthenticationFailed,
		cherrs.PermissionDenied, cherrs.InternalError, cherrs.UnsupportedCapability,
	}
	for _, cat := range notEligible {
		if failoverEligible(of(cat, "op", "net", fmt.Errorf("x"))) {
			t.Fatalf("%s must not be failover eligible", cat)
		}
	}
	if failoverEligible(errors.New("plain")) {
		t.Fatal("an unclassified error must not justify a failover")
	}
}

func TestCommitmentValidation(t *testing.T) {
	for _, c := range []Commitment{CommitmentProcessed, CommitmentConfirmed, CommitmentFinalized} {
		if !c.Valid() {
			t.Fatalf("%s must be valid", c)
		}
	}
	for _, c := range []Commitment{"", "final", "Finalized", "processed "} {
		if c.Valid() {
			t.Fatalf("%q must be invalid", c)
		}
	}
	if string(CommitmentFinalized.rpc()) != string(rpcCommitmentType(CommitmentFinalized)) {
		t.Fatal("conversion must preserve the value")
	}
}
