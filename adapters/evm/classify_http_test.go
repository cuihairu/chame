package evm

import (
	"errors"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"

	cherrs "github.com/cuihairu/chame/core/errors"
)

func httpError(status int) error { return rpc.HTTPError{StatusCode: status, Status: "test"} }

// TestClassifyHTTPErrors pins the failover contract for HTTP-level
// endpoint failures (spec §7.4): a non-2xx answer is the classic
// endpoint-outage signal and must classify as NetworkUnavailable, so
// the pool fails over. go-ethereum serves these as value-typed
// rpc.HTTPError; matching only the pointer form never sees them.
func TestClassifyHTTPErrors(t *testing.T) {
	err := classify("op", "net", httpError(503))
	var ce *cherrs.Error
	if !errors.As(err, &ce) {
		t.Fatalf("classify returned %T, want a classified error", err)
	}
	if ce.Category != cherrs.NetworkUnavailable {
		t.Errorf("category = %v, want network_unavailable", ce.Category)
	}
	if !failoverEligible(err) {
		t.Error("HTTP 503 is not failover-eligible")
	}

	// Rate limiting is transient too.
	if !failoverEligible(classify("op", "net", httpError(429))) {
		t.Error("HTTP 429 is not failover-eligible")
	}
	// Authentication failures are deterministic, not an outage.
	if failoverEligible(classify("op", "net", httpError(401))) {
		t.Error("HTTP 401 must not fail over")
	}
}
