package evm

import (
	"context"
	"fmt"
	"sync"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// endpointPool holds the dialed endpoints of one network and fails over
// between them for read operations (spec §7.4). Reads are idempotent,
// so moving to the next endpoint after a transient error is safe;
// switching endpoints never implies the failed request did not happen.
type endpointPool struct {
	mu      sync.Mutex
	current int
	clients []rpcClient
	// stats counts failovers; it belongs to the owning Network.
	stats *counters
}

func newEndpointPool(clients []rpcClient, stats *counters) *endpointPool {
	return &endpointPool{clients: clients, stats: stats}
}

// call runs fn against the endpoints starting from the current one. On
// a transient, failover-eligible error it advances and retries, at most
// once per endpoint. Deterministic errors return immediately. On success
// the working endpoint becomes current. fn must be a read-only
// operation.
func (p *endpointPool) call(ctx context.Context, network, op string, fn func(rpcClient) error) error {
	p.mu.Lock()
	start := p.current
	p.mu.Unlock()

	if len(p.clients) == 0 {
		return of(cherrs.NetworkUnavailable, network, op,
			fmt.Errorf("network %s: no available endpoints", network))
	}

	var lastErr error
	for i := 0; i < len(p.clients); i++ {
		idx := (start + i) % len(p.clients)
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(p.clients[idx])
		if err == nil {
			p.mu.Lock()
			p.current = idx
			p.mu.Unlock()
			return nil
		}
		err = classify(op, network, err)
		if !failoverEligible(err) {
			return err
		}
		lastErr = err
		if i+1 < len(p.clients) && p.stats != nil {
			p.stats.countFailover()
		}
	}
	return lastErr
}

// close shuts every endpoint down.
func (p *endpointPool) close() {
	for _, c := range p.clients {
		c.Close()
	}
}
