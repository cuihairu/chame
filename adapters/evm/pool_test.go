package evm

import (
	"context"
	"errors"
	"testing"

	cherrs "github.com/cuihairu/chame/core/errors"
)

func TestPoolFailoverOnTransientError(t *testing.T) {
	a := newFakeClient(1).fail("chain_id", netErr{msg: "connection refused"})
	b := newFakeClient(1)
	stats := &counters{}
	pool := newEndpointPool([]rpcClient{a, b}, stats)

	err := pool.call(context.Background(), "evm/mainnet/1", "evm.chain_id", func(c rpcClient) error {
		_, err := c.ChainID(context.Background())
		return err
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if a.callCount("chain_id") != 1 || b.callCount("chain_id") != 1 {
		t.Errorf("attempts a=%d b=%d, want 1 each", a.callCount("chain_id"), b.callCount("chain_id"))
	}
	if stats.snapshot().Failovers != 1 {
		t.Errorf("failovers = %d, want 1", stats.snapshot().Failovers)
	}
}

func TestPoolFailoverOnTimeout(t *testing.T) {
	a := newFakeClient(1).fail("chain_id", context.DeadlineExceeded)
	b := newFakeClient(1)
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	err := pool.call(context.Background(), "evm/mainnet/1", "evm.chain_id", func(c rpcClient) error {
		_, err := c.ChainID(context.Background())
		return err
	})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if a.callCount("chain_id") != 1 || b.callCount("chain_id") != 1 {
		t.Errorf("attempts a=%d b=%d, want 1 each", a.callCount("chain_id"), b.callCount("chain_id"))
	}
}

func TestPoolStopsOnDeterministicError(t *testing.T) {
	a := newFakeClient(1).fail("chain_id", dataErr{data: "0x"})
	b := newFakeClient(1)
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	err := pool.call(context.Background(), "evm/mainnet/1", "evm.chain_id", func(c rpcClient) error {
		_, err := c.ChainID(context.Background())
		return err
	})
	if !errors.Is(err, cherrs.ErrTransactionExecutionFailed) {
		t.Fatalf("err = %v, want execution failed class", err)
	}
	if b.callCount("chain_id") != 0 {
		t.Error("deterministic error must not fail over")
	}
}

func TestPoolAllEndpointsDown(t *testing.T) {
	a := newFakeClient(1).fail("chain_id", netErr{msg: "connection refused"})
	b := newFakeClient(1).fail("chain_id", netErr{msg: "connection refused"})
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	err := pool.call(context.Background(), "evm/mainnet/1", "evm.chain_id", func(c rpcClient) error {
		_, err := c.ChainID(context.Background())
		return err
	})
	if !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("err = %v, want network unavailable", err)
	}
	if a.callCount("chain_id") != 1 || b.callCount("chain_id") != 1 {
		t.Errorf("attempts a=%d b=%d, want 1 each", a.callCount("chain_id"), b.callCount("chain_id"))
	}
}

func TestPoolSuccessBecomesCurrent(t *testing.T) {
	a := newFakeClient(1).fail("chain_id", netErr{msg: "connection refused"})
	b := newFakeClient(1)
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	call := func() error {
		return pool.call(context.Background(), "evm/mainnet/1", "evm.chain_id", func(c rpcClient) error {
			_, err := c.ChainID(context.Background())
			return err
		})
	}
	if err := call(); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := call(); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if a.callCount("chain_id") != 1 {
		t.Errorf("a attempts = %d, want 1 (b is current now)", a.callCount("chain_id"))
	}
	if b.callCount("chain_id") != 2 {
		t.Errorf("b attempts = %d, want 2", b.callCount("chain_id"))
	}
}

func TestPoolContextCancelStopsFailover(t *testing.T) {
	a := newFakeClient(1).fail("chain_id", netErr{msg: "connection refused"})
	b := newFakeClient(1)
	pool := newEndpointPool([]rpcClient{a, b}, &counters{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := pool.call(ctx, "evm/mainnet/1", "evm.chain_id", func(c rpcClient) error {
		_, err := c.ChainID(context.Background())
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
	if b.callCount("chain_id") != 0 {
		t.Error("canceled context must not fail over")
	}
}

func TestPoolClose(t *testing.T) {
	a := newFakeClient(1)
	pool := newEndpointPool([]rpcClient{a}, &counters{})
	pool.close()
	if !a.isClosed() {
		t.Error("close must shut endpoints down")
	}
}
