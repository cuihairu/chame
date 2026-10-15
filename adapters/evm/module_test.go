package evm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/core/module"
)

// threeNetConfig wires three logical networks behind one adapter, with
// two endpoints on the first (spec §7.3, Phase 3 acceptance: two or
// more networks).
func threeNetConfig() Config {
	return Config{Networks: []NetworkConfig{
		{Name: "mainnet", ChainID: 1, Endpoints: []string{"https://m-a.example", "https://m-b.example"}},
		{Name: "base", ChainID: 8453, Endpoints: []string{"https://base.example"}},
		{Name: "arbitrum", ChainID: 42161, Endpoints: []string{"https://arb.example"}},
	}}
}

func netID(name string, chainID uint64) identifiers.NetworkID {
	return identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: name, ChainID: fmt.Sprintf("%d", chainID)}
}

// startModule registers and starts the module against a fresh store,
// returning the store and the dialer clients.
func startModule(t *testing.T, cfg Config, clients map[string]rpcClient) (*module.Store, *Module) {
	t.Helper()
	m := New(cfg, WithDialer(fakeDialer(clients, nil)))
	store := module.NewStore()
	if err := m.Register(module.NewRegistry(store, "evm")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return store, m
}

func threeNetClients() (map[string]rpcClient, *fakeClient, *fakeClient, *fakeClient) {
	mainA, mainB := newFakeClient(1), newFakeClient(1)
	base, arb := newFakeClient(8453), newFakeClient(42161)
	return map[string]rpcClient{
		"https://m-a.example":  mainA,
		"https://m-b.example":  mainB,
		"https://base.example": base,
		"https://arb.example":  arb,
	}, mainA, base, arb
}

func TestModuleServesMultipleNetworks(t *testing.T) {
	clients, mainA, base, arb := threeNetClients()
	store, _ := startModule(t, threeNetConfig(), clients)

	raw, found := store.Capability(capability.Network)
	if !found {
		t.Fatal("network capability not registered")
	}
	adapter, ok := raw.(*Adapter)
	if !ok {
		t.Fatalf("capability type = %T, want *Adapter", raw)
	}
	// The one adapter value is published as every capability the module
	// declares, so consumers pick the entry point they need.
	for _, name := range []capability.Name{capability.Account, capability.Asset} {
		other, ok := store.Capability(name)
		if !ok {
			t.Fatalf("%s capability not registered", name)
		}
		if other != raw {
			t.Errorf("%s capability = %p, want the network adapter %p", name, other, raw)
		}
	}
	if _, ok := adapter.AssetProvider(netID("mainnet", 1)); !ok {
		t.Error("asset provider for mainnet must resolve")
	}
	if _, ok := adapter.AssetProvider(netID("unknown", 7)); ok {
		t.Error("unknown network must not resolve an asset provider")
	}
	if got := adapter.Networks(); len(got) != 3 {
		t.Fatalf("networks = %v, want 3", got)
	}
	for _, id := range []identifiers.NetworkID{netID("mainnet", 1), netID("base", 8453), netID("arbitrum", 42161)} {
		if _, ok := adapter.Network(id); !ok {
			t.Errorf("network %s missing", id)
		}
	}
	if _, ok := adapter.Network(netID("unknown", 7)); ok {
		t.Error("unknown network must not resolve")
	}

	// Each network's queries reach only its own endpoints.
	baseNet, _ := adapter.Network(netID("base", 8453))
	holder := identifiers.Address{Family: identifiers.FamilyEVM, Value: "0x0000000000000000000000000000000000000001"}
	if _, err := baseNet.BalanceAt(context.Background(), holder); err != nil {
		t.Fatal(err)
	}
	if base.callCount("balance_at") != 1 || mainA.callCount("balance_at") != 0 || arb.callCount("balance_at") != 0 {
		t.Errorf("cross-network leak: main=%d base=%d arb=%d",
			mainA.callCount("balance_at"), base.callCount("balance_at"), arb.callCount("balance_at"))
	}
}

func TestModuleInfo(t *testing.T) {
	want := []capability.Name{capability.Network, capability.Account, capability.Asset}
	got := New(threeNetConfig()).Info().Provides
	if len(got) != len(want) {
		t.Fatalf("provides = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("provides[%d] = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestModuleRegistersHealthChecks(t *testing.T) {
	clients, _, _, _ := threeNetClients()
	store, _ := startModule(t, threeNetConfig(), clients)
	names := store.HealthChecks().Names()
	want := []string{"evm.mainnet", "evm.base", "evm.arbitrum"}
	if len(names) != len(want) {
		t.Fatalf("checks = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("check[%d] = %q, want %q", i, names[i], want[i])
		}
	}
	report := store.HealthChecks().Run(context.Background())
	if report.Status != health.Healthy {
		t.Fatalf("report = %+v, want healthy", report)
	}
}

func TestModuleHealthDetectsChainDrift(t *testing.T) {
	clients, _, base, _ := threeNetClients()
	store, _ := startModule(t, threeNetConfig(), clients)
	base.chainID = big.NewInt(555) // the endpoint now reports a different chain
	report := store.HealthChecks().Run(context.Background())
	if report.Status != health.Unhealthy {
		t.Fatalf("report = %+v, want unhealthy", report)
	}
}

func TestModuleStartFailsOnIdentityMismatch(t *testing.T) {
	clients, mainA, _, _ := threeNetClients()
	clients["https://base.example"] = newFakeClient(999) // wrong chain id
	m := New(threeNetConfig(), WithDialer(fakeDialer(clients, nil)))
	store := module.NewStore()
	if err := m.Register(module.NewRegistry(store, "evm")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	err := m.Start(context.Background())
	if !errors.Is(err, cherrs.ErrInvalidConfiguration) {
		t.Fatalf("err = %v, want invalid configuration", err)
	}
	if !mainA.isClosed() {
		t.Error("already-started networks must be closed on failure")
	}
}

func TestModuleStartFailsWhenNoEndpointReachable(t *testing.T) {
	m := New(threeNetConfig(), WithDialer(fakeDialer(nil, errors.New("dial: refused"))))
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("expected start failure")
	} else if !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("err = %v, want network unavailable", err)
	}
}

func TestModuleStartTwiceRejected(t *testing.T) {
	clients, _, _, _ := threeNetClients()
	_, m := startModule(t, threeNetConfig(), clients)
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("second Start must fail")
	}
}

func TestModuleStopClosesEndpoints(t *testing.T) {
	clients, mainA, mainB, base := threeNetClients()
	arb := clients["https://arb.example"].(*fakeClient)
	_, m := startModule(t, threeNetConfig(), clients)
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, c := range []*fakeClient{mainA, mainB, base, arb} {
		if !c.isClosed() {
			t.Error("stop must close every endpoint")
		}
	}
	// Stop is idempotent.
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

func TestModuleRegisterRejectsBadConfig(t *testing.T) {
	m := New(Config{})
	store := module.NewStore()
	if err := m.Register(module.NewRegistry(store, "evm")); err == nil {
		t.Fatal("empty config must be rejected")
	}
}
