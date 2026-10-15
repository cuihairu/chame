package evm

import (
	"context"
	"errors"
	"testing"

	"github.com/cuihairu/chame/core/identifiers"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// TestNetworkRejectsRequestsAfterClose pins spec §16.2: a closed adapter
// answers new requests with a classified error instead of panicking on a
// nil pool.
func TestNetworkRejectsRequestsAfterClose(t *testing.T) {
	n := testNetwork(t, newFakeClient(1))
	n.close()

	addr := evmAddr(t, weiHolder)
	if err := n.Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Healthy = %v, want network unavailable", err)
	}
	if _, err := n.BalanceAt(context.Background(), addr); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("BalanceAt = %v, want network unavailable", err)
	}
	if _, err := n.ChainID(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("ChainID = %v, want network unavailable", err)
	}
	if _, err := n.AccountProvider().Account(context.Background(), addr); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Account = %v, want network unavailable", err)
	}
	if _, err := n.AccountProvider().Balance(context.Background(), addr); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Balance = %v, want network unavailable", err)
	}
	// Native-asset metadata is served from the adapter's own knowledge
	// of the family and needs no endpoint; a token query does.
	if _, err := n.AssetProvider().Asset(context.Background(), identifiers.AssetID{Network: n.ID(), Kind: identifiers.KindNative}); err != nil {
		t.Errorf("native Asset after close = %v, want served without the endpoint", err)
	}
	token := identifiers.AssetID{Network: n.ID(), Kind: identifiers.KindEVMToken, Address: weiHolder}
	if _, err := n.AssetProvider().Asset(context.Background(), token); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Asset = %v, want network unavailable", err)
	}
	if _, err := n.AssetProvider().Balance(context.Background(), token, addr); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("asset Balance = %v, want network unavailable", err)
	}
}
