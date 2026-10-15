package solana

import (
	"context"
	"errors"
	"testing"

	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// TestNetworkRejectsRequestsAfterClose pins spec §16.2: a closed adapter
// answers new requests with a classified error instead of panicking on a
// nil pool.
func TestNetworkRejectsRequestsAfterClose(t *testing.T) {
	n := newTestNetwork(newFakeClient(solanaHashFor(0x10)))
	n.close()

	addr := mustAddress(t, sysAccount)
	if err := n.Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Healthy = %v, want network unavailable", err)
	}
	if _, err := n.Balance(context.Background(), addr, CommitmentFinalized); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Balance = %v, want network unavailable", err)
	}
	if _, err := n.Slot(context.Background(), CommitmentFinalized); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Slot = %v, want network unavailable", err)
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
	token := identifiers.AssetID{Network: n.ID(), Kind: identifiers.KindSPLToken, Address: voteAccount}
	if _, err := n.AssetProvider().Asset(context.Background(), token); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Asset = %v, want network unavailable", err)
	}
	if _, err := n.AssetProvider().Balance(context.Background(), token, addr); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("asset Balance = %v, want network unavailable", err)
	}
}

// TestModuleRejectedCapabilityAfterClose pins that the module's write
// capabilities stay explicit: closing an adapter never turns them into
// available capabilities.
func TestModuleRejectedCapabilityAfterClose(t *testing.T) {
	clients, _, _, _ := twoNetClients()
	store, m := startModule(t, twoNetConfig(), clients)
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	raw, ok := store.Capability(capability.Network)
	if !ok {
		t.Fatal("network capability not registered")
	}
	adapter := raw.(*Adapter)
	if adapter.Supported(capability.Transaction) {
		t.Error("write capability must not become supported after close")
	}
	if err := adapter.UnsupportedCapability(capability.Transaction); !errors.Is(err, cherrs.ErrUnsupportedCapability) {
		t.Errorf("err = %v, want unsupported capability", err)
	}
}
