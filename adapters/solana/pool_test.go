package solana

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	cherrs "github.com/cuihairu/chame/core/errors"
)

func genesisCall(ctx context.Context, c rpcClient) error {
	_, err := c.GenesisHash(ctx)
	return err
}

// A hanging endpoint holds only its slice of the caller's budget: the
// failover after it still has time to answer, instead of finding the
// whole budget already consumed (spec §7.4).
func TestPoolSlowEndpointLeavesFailoverBudget(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10)).hang("genesis_hash")
	b := newFakeClient(solanaHashFor(0x10))
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := pool.call(ctx, "solana/mainnet/x", "solana.genesis_hash", genesisCall)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("call failed after %v: %v", elapsed, err)
	}
	// The hanging endpoint was cut at half the budget; the answer landed
	// well inside the whole thing.
	if elapsed >= 400*time.Millisecond {
		t.Fatalf("call took %v, want the failover inside the budget", elapsed)
	}
	if a.callCount("genesis_hash") != 1 || b.callCount("genesis_hash") != 1 {
		t.Errorf("attempts a=%d b=%d, want 1 each", a.callCount("genesis_hash"), b.callCount("genesis_hash"))
	}
}

func TestPoolFailoverOnTransientError(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	b := newFakeClient(solanaHashFor(0x10))
	stats := &counters{}
	pool := newEndpointPool([]rpcClient{a, b}, stats)

	if err := pool.call(context.Background(), "solana/mainnet/x", "solana.genesis_hash", genesisCall); err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if a.callCount("genesis_hash") != 1 || b.callCount("genesis_hash") != 1 {
		t.Errorf("attempts a=%d b=%d, want 1 each", a.callCount("genesis_hash"), b.callCount("genesis_hash"))
	}
	if stats.snapshot().Failovers != 1 {
		t.Errorf("failovers = %d, want 1", stats.snapshot().Failovers)
	}
}

func TestPoolFailoverOnTimeoutAndRateLimit(t *testing.T) {
	for name, raw := range map[string]error{
		"timeout":    context.DeadlineExceeded,
		"rate limit": jsonrpc.NewHTTPError(429, errors.New("too many")),
	} {
		t.Run(name, func(t *testing.T) {
			a := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", raw)
			b := newFakeClient(solanaHashFor(0x10))
			pool := newEndpointPool([]rpcClient{a, b}, &counters{})
			if err := pool.call(context.Background(), "solana/mainnet/x", "solana.genesis_hash", genesisCall); err != nil {
				t.Fatalf("call failed: %v", err)
			}
			if b.callCount("genesis_hash") != 1 {
				t.Errorf("b attempts = %d, want 1", b.callCount("genesis_hash"))
			}
		})
	}
}

func TestPoolStopsOnDeterministicError(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", &jsonrpc.RPCError{Code: -32602, Message: "bad"})
	b := newFakeClient(solanaHashFor(0x10))
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	err := pool.call(context.Background(), "solana/mainnet/x", "solana.genesis_hash", genesisCall)
	if !errors.Is(err, cherrs.ErrInternalError) {
		t.Fatalf("err = %v, want internal error class", err)
	}
	if b.callCount("genesis_hash") != 0 {
		t.Error("a rejected request must not fail over")
	}
}

func TestPoolAllEndpointsDown(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	b := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	err := pool.call(context.Background(), "solana/mainnet/x", "solana.genesis_hash", genesisCall)
	if !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("err = %v, want network unavailable", err)
	}
	if a.callCount("genesis_hash") != 1 || b.callCount("genesis_hash") != 1 {
		t.Errorf("attempts a=%d b=%d, want 1 each", a.callCount("genesis_hash"), b.callCount("genesis_hash"))
	}
}

func TestPoolSuccessBecomesCurrent(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	b := newFakeClient(solanaHashFor(0x10))
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	call := func() error {
		return pool.call(context.Background(), "solana/mainnet/x", "solana.genesis_hash", genesisCall)
	}
	if err := call(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := call(); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if a.callCount("genesis_hash") != 1 {
		t.Errorf("a attempts = %d, want 1 (b is current now)", a.callCount("genesis_hash"))
	}
	if b.callCount("genesis_hash") != 2 {
		t.Errorf("b attempts = %d, want 2", b.callCount("genesis_hash"))
	}
}

func TestPoolContextCancelStopsFailover(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	b := newFakeClient(solanaHashFor(0x10))
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := pool.call(ctx, "solana/mainnet/x", "solana.genesis_hash", genesisCall)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if b.callCount("genesis_hash") != 0 {
		t.Error("canceled context must not fail over")
	}
}

func TestPoolClose(t *testing.T) {
	a := newFakeClient(solanaHashFor(0x10))
	pool := newEndpointPool([]rpcClient{a}, &counters{})
	pool.close()
	if !a.isClosed() {
		t.Error("close must shut endpoints down")
	}
}
