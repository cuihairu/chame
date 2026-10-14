package solana

import (
	"context"
	"errors"
	"testing"

	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/core/module"
)

// twoNetConfig wires two logical clusters behind one adapter, with two
// endpoints on the first (spec §7.3, Phase 4 acceptance: independent
// Solana network instances).
func twoNetConfig() Config {
	return Config{Networks: []NetworkConfig{
		{Name: "mainnet", ExpectedGenesisHash: testGenesis(0x10),
			Endpoints: []string{"https://m-a.example", "https://m-b.example"}},
		{Name: "devnet", ExpectedGenesisHash: testGenesis(0x20),
			Endpoints: []string{"https://d.example"}},
	}}
}

func clusterID(name, hash string) identifiers.NetworkID {
	return identifiers.NetworkID{Family: identifiers.FamilySolana, Name: name, ChainID: hash}
}

// startModule registers and starts the module, returning the store.
func startModule(t *testing.T, cfg Config, clients map[string]rpcClient) (*module.Store, *Module) {
	t.Helper()
	m := New(cfg, WithDialer(fakeDialer(clients, nil)))
	store := module.NewStore()
	if err := m.Register(module.NewRegistry(store, "solana")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = m.Stop(context.Background()) })
	return store, m
}

func twoNetClients() (map[string]rpcClient, *fakeClient, *fakeClient, *fakeClient) {
	mainA, mainB := newFakeClient(solanaHashFor(0x10)), newFakeClient(solanaHashFor(0x10))
	dev := newFakeClient(solanaHashFor(0x20))
	return map[string]rpcClient{
		"https://m-a.example": mainA,
		"https://m-b.example": mainB,
		"https://d.example":   dev,
	}, mainA, mainB, dev
}

func TestModuleServesMultipleClusters(t *testing.T) {
	clients, mainA, _, dev := twoNetClients()
	store, _ := startModule(t, twoNetConfig(), clients)

	raw, ok := store.Capability(capability.Network)
	if !ok {
		t.Fatal("network capability not registered")
	}
	adapter, ok := raw.(*Adapter)
	if !ok {
		t.Fatalf("capability type = %T, want *Adapter", raw)
	}
	if got := adapter.Networks(); len(got) != 2 {
		t.Fatalf("networks = %v, want 2", got)
	}
	for _, id := range []identifiers.NetworkID{
		clusterID("mainnet", testGenesis(0x10)), clusterID("devnet", testGenesis(0x20)),
	} {
		if _, ok := adapter.Network(id); !ok {
			t.Errorf("network %s missing", id)
		}
	}
	if _, ok := adapter.Network(clusterID("unknown", testGenesis(0x99))); ok {
		t.Error("unknown network must not resolve")
	}

	// Each cluster's queries reach only its own endpoints.
	devNet, _ := adapter.Network(clusterID("devnet", testGenesis(0x20)))
	if _, err := devNet.Balance(context.Background(), mustAddress(t, sysAccount), CommitmentFinalized); err != nil {
		t.Fatal(err)
	}
	if dev.callCount("balance") != 1 || mainA.callCount("balance") != 0 {
		t.Errorf("cross-cluster leak: main=%d dev=%d", mainA.callCount("balance"), dev.callCount("balance"))
	}
}

func TestModuleRegistersHealthChecks(t *testing.T) {
	clients, _, _, _ := twoNetClients()
	store, _ := startModule(t, twoNetConfig(), clients)
	names := store.HealthChecks().Names()
	want := []string{"solana.mainnet", "solana.devnet"}
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

func TestModuleHealthDetectsClusterDrift(t *testing.T) {
	clients, _, _, dev := twoNetClients()
	store, _ := startModule(t, twoNetConfig(), clients)
	dev.hash = solanaHashFor(0x99) // the endpoint now reports another cluster
	report := store.HealthChecks().Run(context.Background())
	if report.Status != health.Unhealthy {
		t.Fatalf("report = %+v, want unhealthy", report)
	}
}

func TestModuleStartFailsOnIdentityMismatch(t *testing.T) {
	clients, mainA, _, _ := twoNetClients()
	clients["https://d.example"] = newFakeClient(solanaHashFor(0x99)) // wrong cluster
	m := New(twoNetConfig(), WithDialer(fakeDialer(clients, nil)))
	store := module.NewStore()
	if err := m.Register(module.NewRegistry(store, "solana")); err != nil {
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
	m := New(twoNetConfig(), WithDialer(fakeDialer(nil, errors.New("dial: refused"))))
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("expected start failure")
	} else if !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("err = %v, want network unavailable", err)
	}
}

func TestModuleStartTwiceRejected(t *testing.T) {
	clients, _, _, _ := twoNetClients()
	_, m := startModule(t, twoNetConfig(), clients)
	if err := m.Start(context.Background()); err == nil {
		t.Fatal("second Start must fail")
	}
}

func TestModuleStopClosesEndpoints(t *testing.T) {
	clients, mainA, mainB, dev := twoNetClients()
	_, m := startModule(t, twoNetConfig(), clients)
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for _, c := range []*fakeClient{mainA, mainB, dev} {
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
	if err := m.Register(module.NewRegistry(store, "solana")); err == nil {
		t.Fatal("empty config must be rejected")
	}
}

// TestModuleReadOnlyContract pins ADR-0003: the adapter publishes the
// network family only, and every other capability returns an explicit
// unsupported error instead of a broken call.
func TestModuleReadOnlyContract(t *testing.T) {
	clients, _, _, _ := twoNetClients()
	store, m := startModule(t, twoNetConfig(), clients)

	if got := m.Info().Provides; len(got) != 1 || got[0] != capability.Network {
		t.Fatalf("provides = %v, want [network]", got)
	}
	raw, _ := store.Capability(capability.Network)
	adapter := raw.(*Adapter)
	if got := adapter.Capabilities(); len(got) != 1 || got[0] != capability.Network {
		t.Fatalf("capabilities = %v, want [network]", got)
	}
	if !adapter.Supported(capability.Network) {
		t.Fatal("network must be supported")
	}
	for _, name := range []capability.Name{
		capability.Transaction, capability.Signer, capability.Event,
		capability.Finality, capability.Account, capability.Asset, capability.Contract,
	} {
		if adapter.Supported(name) {
			t.Errorf("%s must not be provided in the read-only phase", name)
		}
		err := adapter.UnsupportedCapability(name)
		if !errors.Is(err, cherrs.ErrUnsupportedCapability) {
			t.Fatalf("%s: err = %v, want unsupported capability", name, err)
		}
	}
	if err := adapter.UnsupportedCapability(""); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("empty name: err = %v, want invalid argument", err)
	}
}
