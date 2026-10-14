package solana

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/core/module"
)

// Adapter is the capability value published by the module: the
// chain-agnostic entry point into every configured cluster.
type Adapter struct {
	networks map[identifiers.NetworkID]*Network
	order    []identifiers.NetworkID
}

// Network returns the network instance for the identity.
func (a *Adapter) Network(id identifiers.NetworkID) (*Network, bool) {
	n, ok := a.networks[id]
	return n, ok
}

// Networks lists the configured network identities in config order.
func (a *Adapter) Networks() []identifiers.NetworkID {
	return append([]identifiers.NetworkID(nil), a.order...)
}

// Capabilities lists the capability families the adapter provides.
// Phase 4 is read-only (ADR-0003): transaction building, signing,
// submission and finality tracking are absent by design, not broken.
func (a *Adapter) Capabilities() []capability.Name {
	return []capability.Name{capabilityNetwork}
}

// Supported reports whether the adapter provides the capability.
func (a *Adapter) Supported(name capability.Name) bool {
	for _, n := range a.Capabilities() {
		if n == name {
			return true
		}
	}
	return false
}

// UnsupportedCapability returns the explicit error for a capability the
// adapter does not provide, so a caller gets a contract instead of a
// silent no-op (ADR-0003, spec §8 验收). It reports every name not in
// Capabilities, whatever the caller's intent.
func (a *Adapter) UnsupportedCapability(name capability.Name) error {
	if !name.Valid() {
		return cherrs.New(cherrs.InvalidArgument, "solana.capabilities",
			"capability name must be valid, got %q", name)
	}
	return cherrs.New(cherrs.UnsupportedCapability, "solana.capabilities",
		"the solana adapter does not provide capability %q in this phase", name)
}

// Module is the Solana adapter as a chame module (spec §8.1).
type Module struct {
	cfg    Config
	logger *slog.Logger
	dial   Dialer

	mu      sync.Mutex
	adapter *Adapter
	started bool
}

// Option customizes the module.
type Option func(*Module)

// WithLogger injects a logger. Diagnostics use it; nothing secret is
// logged (spec §5.4).
func WithLogger(l *slog.Logger) Option { return func(m *Module) { m.logger = l } }

// WithDialer injects the endpoint dialer. Tests use fakes; production
// uses the default Solana dialer.
func WithDialer(d Dialer) Option { return func(m *Module) { m.dial = d } }

// New builds the module from config. Validation happens at Register.
func New(cfg Config, opts ...Option) *Module {
	m := &Module{cfg: cfg, dial: defaultDialer}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Name implements module.Module.
func (m *Module) Name() string { return "solana" }

// Info declares what the module provides: the network capability family
// only (ADR-0003).
func (m *Module) Info() module.Info {
	return module.Info{Provides: []capability.Name{capabilityNetwork}}
}

// Register validates the config, publishes the adapter capability, and
// registers one health check per network.
func (m *Module) Register(reg module.Registry) error {
	if err := m.cfg.Validate(); err != nil {
		return err
	}
	a := m.buildAdapter()
	for _, id := range a.order {
		n := a.networks[id]
		if err := reg.AddHealthCheck("solana."+n.cfg.Name, n); err != nil {
			return err
		}
	}
	return reg.PutCapability(capabilityNetwork, a)
}

// Start dials every configured network and verifies cluster identity.
func (m *Module) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return fmt.Errorf("solana: module already started")
	}
	a := m.buildAdapter()
	for _, id := range a.order {
		n := a.networks[id]
		clients, err := m.dialNetwork(ctx, n.cfg)
		if err != nil {
			// Release what was already connected, reverse of start order.
			for _, otherID := range a.order {
				if otherID == id {
					break
				}
				a.networks[otherID].close()
			}
			return err
		}
		n.attach(newEndpointPool(clients, n.stats))
	}
	m.started = true
	return nil
}

// Stop closes every endpoint. It is idempotent.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.adapter != nil {
		for _, id := range m.adapter.order {
			m.adapter.networks[id].close()
		}
	}
	m.started = false
	return nil
}

// buildAdapter constructs the adapter state. Register and Start share it,
// so a module started without Register (tests, simple hosts) behaves
// identically.
func (m *Module) buildAdapter() *Adapter {
	if m.adapter != nil {
		return m.adapter
	}
	a := &Adapter{networks: map[identifiers.NetworkID]*Network{}}
	for _, cfg := range m.cfg.Networks {
		n := newNetwork(cfg, nil)
		a.networks[n.id] = n
		a.order = append(a.order, n.id)
	}
	m.adapter = a
	return a
}

// dialNetwork dials the endpoint pool, keeping endpoints that answer with
// the configured cluster identity. Failing endpoints are logged and
// skipped; if none answer the network is unavailable (spec §7.4: endpoint
// failure does not imply chain failure, but an unreachable network fails
// the call).
func (m *Module) dialNetwork(ctx context.Context, cfg NetworkConfig) ([]rpcClient, error) {
	dialCtx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	var clients []rpcClient
	var lastErr error
	// identityErr preserves a genesis-hash mismatch even when the endpoint
	// is otherwise reachable: this is misconfiguration, not an outage.
	var identityErr error
	for _, url := range cfg.Endpoints {
		c, err := m.dial(dialCtx, url)
		if err != nil {
			lastErr = classify(cfg.networkID().String(), "solana."+cfg.Name+".dial", err)
			m.warnf("network %q: endpoint unreachable: %v", cfg.Name, lastErr)
			continue
		}
		// Identity verification: every endpoint must report the configured
		// genesis hash (spec §8.2). Contradictory endpoints are a hard
		// failure, never silently resolved (spec §7.4).
		hash, err := c.GenesisHash(dialCtx)
		if err != nil {
			m.closeEndpoint(cfg.Name, c)
			lastErr = classify(cfg.networkID().String(), "solana."+cfg.Name+".genesis_hash", err)
			m.warnf("network %q: endpoint identity check failed: %v", cfg.Name, lastErr)
			continue
		}
		got := hash.String()
		if got == "" {
			m.closeEndpoint(cfg.Name, c)
			lastErr = classify(cfg.networkID().String(), "solana."+cfg.Name+".genesis_hash",
				fmt.Errorf("endpoint reported no genesis hash"))
			m.warnf("network %q: endpoint reported no genesis hash", cfg.Name)
			continue
		}
		if got != cfg.ExpectedGenesisHash {
			m.closeEndpoint(cfg.Name, c)
			identityErr = cherrs.New(cherrs.InvalidConfiguration, "solana."+cfg.Name+".genesis_hash",
				"network %q: genesis hash mismatch: want %s, got %s", cfg.Name, cfg.ExpectedGenesisHash, got)
			m.warnf("%v", identityErr)
			continue
		}
		clients = append(clients, c)
	}
	if len(clients) == 0 {
		if identityErr != nil {
			return nil, identityErr
		}
		return nil, cherrs.New(cherrs.NetworkUnavailable, "solana."+cfg.Name+".dial",
			"network %q: no endpoint reachable: %v", cfg.Name, lastErr)
	}
	return clients, nil
}

func (m *Module) warnf(format string, args ...any) {
	if m.logger != nil {
		m.logger.Warn(fmt.Sprintf(format, args...))
	}
}

// closeEndpoint shuts a rejected endpoint down. A close failure never
// changes the outcome — the endpoint was already rejected — but it is
// logged, not dropped.
func (m *Module) closeEndpoint(name string, c rpcClient) {
	if err := c.Close(); err != nil {
		m.warnf("network %q: endpoint close failed: %v", name, err)
	}
}

// attach connects a dialed pool to a network.
func (n *Network) attach(pool *endpointPool) {
	n.pool = pool
}

// close shuts the network's endpoints down and disconnects the pool.
// Safe before attach.
func (n *Network) close() {
	if n.pool == nil {
		return
	}
	n.pool.close()
	n.pool = nil
}

// CheckHealth implements health.Checker. Before Start the network is
// unhealthy, not silently healthy.
func (n *Network) CheckHealth(ctx context.Context) (health.Status, error) {
	if n.pool == nil {
		return health.Unhealthy, fmt.Errorf("network %s: not started", n.cfg.Name)
	}
	if err := n.Healthy(ctx); err != nil {
		return health.Unhealthy, err
	}
	return health.Healthy, nil
}

var (
	_ module.Module  = (*Module)(nil)
	_ health.Checker = (*Network)(nil)
)
