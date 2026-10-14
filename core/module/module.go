// Package module defines the Module interface, registration metadata, and
// the module-scoped Registry (spec §4.3, §4.4). Dependencies are wired
// explicitly by the composition root; there is no injection container.
package module

import (
	"context"
	"fmt"
	"time"

	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/health"
)

// Module is a business or application capability unit (spec §2.3). A
// module registers what it provides in Register, starts after its
// dependencies in Start, and releases resources in Stop.
type Module interface {
	// Name returns the stable, unique module identifier.
	Name() string
	// Register declares capabilities, services, and handlers on the
	// registry. It must not start long-running background tasks.
	Register(Registry) error
	// Start brings the module online. It runs after every dependency has
	// completed Start.
	Start(context.Context) error
	// Stop releases resources and must honor the context deadline.
	Stop(context.Context) error
}

// Dependency names another module this one depends on.
type Dependency struct {
	// Name is the dependency's module name.
	Name string
	// Optional marks the dependency as best-effort: startup order still
	// follows it when present, but its absence is not an error.
	Optional bool
}

// Info is the registration metadata a module carries beyond its interface
// (spec §4.4). The zero value is valid: no dependencies, no capability
// declarations.
type Info struct {
	// Dependencies lists required or optional module dependencies.
	Dependencies []Dependency
	// Requires lists capability names that must be provided by some
	// module in the same runtime.
	Requires []capability.Name
	// Provides lists capability names this module makes available. Each
	// capability name may be provided by at most one module in the first
	// release (spec §4.4 step 3).
	Provides []capability.Name
	// StopTimeout bounds this module's Stop call individually. Zero uses
	// the runtime shutdown timeout.
	StopTimeout time.Duration
}

// Validate checks the metadata itself: dependency names non-empty and
// not self-referencing, capability names well-formed.
func (i Info) Validate(name string) error {
	seen := make(map[string]bool, len(i.Dependencies))
	for _, d := range i.Dependencies {
		if d.Name == "" {
			return fmt.Errorf("module %q: dependency name must not be empty", name)
		}
		if d.Name == name {
			return fmt.Errorf("module %q: depends on itself", name)
		}
		if seen[d.Name] {
			return fmt.Errorf("module %q: duplicate dependency %q", name, d.Name)
		}
		seen[d.Name] = true
	}
	for _, c := range i.Requires {
		if !c.Valid() {
			return fmt.Errorf("module %q: invalid required capability %q", name, c)
		}
	}
	for _, c := range i.Provides {
		if !c.Valid() {
			return fmt.Errorf("module %q: invalid provided capability %q", name, c)
		}
	}
	return nil
}

// Store is the runtime-wide registration state: named capabilities,
// health checks, and the module that published each entry. One Store
// belongs to one Runtime; Registry values are per-module views over it.
type Store struct {
	capabilities map[capability.Name]entry
	healthChecks *health.Registry
}

type entry struct {
	value any
	owner string
}

// NewStore creates the shared store for one runtime.
func NewStore() *Store {
	return &Store{
		capabilities: make(map[capability.Name]entry),
		healthChecks: health.NewRegistry(),
	}
}

// HealthChecks exposes the aggregated health registry.
func (s *Store) HealthChecks() *health.Registry { return s.healthChecks }

// Put publishes v under the capability name on behalf of owner. The name
// may be claimed once across the runtime; later claims are rejected
// (spec §4.4 step 3, uniqueness policy of the first release).
func (s *Store) Put(owner string, name capability.Name, v any) error {
	if !name.Valid() {
		return fmt.Errorf("module %q: invalid capability name %q", owner, name)
	}
	if v == nil {
		return fmt.Errorf("module %q: capability %q must not be nil", owner, name)
	}
	if prev, dup := s.capabilities[name]; dup {
		return fmt.Errorf("module %q: capability %q already provided by module %q", owner, name, prev.owner)
	}
	s.capabilities[name] = entry{value: v, owner: owner}
	return nil
}

// Capability returns the value registered under the capability name.
// Callers own the type assertion; nothing hides the concrete type.
func (s *Store) Capability(name capability.Name) (any, bool) {
	e, ok := s.capabilities[name]
	if !ok {
		return nil, false
	}
	return e.value, true
}

// Registry is the controlled registration surface handed to a module
// (spec §4.3): named capabilities, health checks. It is deliberately not
// a container for reaching into other modules' internals.
type Registry struct {
	store *Store
	owner string
}

// NewRegistry returns the registry view for one module.
func NewRegistry(s *Store, owner string) Registry {
	return Registry{store: s, owner: owner}
}

// PutCapability publishes v under the capability name. The name may be
// claimed once across the runtime.
func (r Registry) PutCapability(name capability.Name, v any) error {
	return r.store.Put(r.owner, name, v)
}

// Capability reads a value published by any module. During Register only
// earlier-initialized modules are visible; by Start all are.
func (r Registry) Capability(name capability.Name) (any, bool) {
	return r.store.Capability(name)
}

// AddHealthCheck registers a named health check owned by the module.
func (r Registry) AddHealthCheck(name string, c health.Checker) error {
	if err := r.store.healthChecks.Add(name, c); err != nil {
		return fmt.Errorf("module %q: %w", r.owner, err)
	}
	return nil
}
