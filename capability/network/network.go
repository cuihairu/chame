// Package network defines the network capability contract (spec §6.1):
// network identity and connection state. Adapters expose this and the
// other capability families; an adapter may support a subset and must
// declare what it supports.
package network

import (
	"context"

	"github.com/cuihairu/chame/core/identifiers"
)

// Provider reports the identity and liveness of one connected network.
// It is intentionally minimal: richer read-only queries (blocks,
// balances, contract calls) arrive with their own capability families in
// later phases instead of ballooning this one.
type Provider interface {
	// Network returns the logical network identity (spec §6.2). The
	// returned identifier must match the configured network; production
	// callers verify the protocol identity (chain ID, genesis hash)
	// through the adapter, not through this label alone.
	Network() identifiers.NetworkID
	// Healthy reports whether the network connection is usable. It must
	// respect the context deadline and return a descriptive error when
	// the network is unreachable.
	Healthy(ctx context.Context) error
}
