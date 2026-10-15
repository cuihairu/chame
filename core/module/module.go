// Package module defines the Module interface, registration metadata, and
// the module-scoped Registry (spec §4.3, §4.4). Dependencies are wired
// explicitly by the composition root; there is no injection container.
package module

import (
	"context"
	"fmt"
	"sort"
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
// health checks, background tasks, event handlers, and the module that
// published each entry. One Store belongs to one Runtime; Registry values
// are per-module views over it.
type Store struct {
	capabilities map[capability.Name]entry
	healthChecks *health.Registry
	tasks        map[string]TaskRegistration
	handlers     map[string]entry
}

type entry struct {
	value any
	owner string
}

// Task is a long-running background worker (spec §4.3): the runtime
// starts every registered task after all modules have started and
// cancels it before modules stop. A task must return when its context is
// done; it owns its own retry policy inside the loop.
type Task func(ctx context.Context) error

// TaskRegistration is one background task as the runtime sees it.
type TaskRegistration struct {
	// Name is the task's name, unique within its owning module.
	Name string
	// Owner is the module that registered the task.
	Owner string
	// Task is the worker body.
	Task Task
}

// NewStore creates the shared store for one runtime.
func NewStore() *Store {
	return &Store{
		capabilities: make(map[capability.Name]entry),
		healthChecks: health.NewRegistry(),
		tasks:        make(map[string]TaskRegistration),
		handlers:     make(map[string]entry),
	}
}

// HealthChecks exposes the aggregated health registry.
func (s *Store) HealthChecks() *health.Registry { return s.healthChecks }

// Tasks lists every registered background task in registration order,
// which is module start order.
func (s *Store) Tasks() []TaskRegistration {
	out := make([]TaskRegistration, 0, len(s.tasks))
	for _, t := range s.tasks {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Owner < out[j].Owner || (out[i].Owner == out[j].Owner && out[i].Name < out[j].Name)
	})
	return out
}

// EventHandler returns the handler registered for one normalized event
// type. The value is opaque here by the same policy as the capability
// store: the typed contract — a Handler in the eventing application — is
// asserted by the consumer.
func (s *Store) EventHandler(eventType string) (any, bool) {
	e, ok := s.handlers[eventType]
	if !ok {
		return nil, false
	}
	return e.value, true
}

// HandlerTypes lists every event type that has a handler, sorted.
func (s *Store) HandlerTypes() []string {
	out := make([]string, 0, len(s.handlers))
	for t := range s.handlers {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

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

// AddHealthCheck registers an unconditional dependency check owned by the
// module: it counts in every report. Use AddDependencyHealthCheck for an
// optional module, so a service that does not use it stays ready (spec
// §15.3).
func (r Registry) AddHealthCheck(name string, c health.Checker) error {
	if err := r.store.healthChecks.Add(name, c); err != nil {
		return fmt.Errorf("module %q: %w", r.owner, err)
	}
	return nil
}

// AddDependencyHealthCheck registers a dependency check that only counts
// for callers using one of the named dependencies.
func (r Registry) AddDependencyHealthCheck(name string, deps []string, c health.Checker) error {
	if err := r.store.healthChecks.AddDependency(name, deps, c); err != nil {
		return fmt.Errorf("module %q: %w", r.owner, err)
	}
	return nil
}

// AddScopedHealthCheck registers a check owned by the module in the given
// scope (spec §15.3).
func (r Registry) AddScopedHealthCheck(name string, scope health.Scope, c health.Checker) error {
	if err := r.store.healthChecks.AddScoped(name, scope, c); err != nil {
		return fmt.Errorf("module %q: %w", r.owner, err)
	}
	return nil
}

// AddTask registers a long-running background task owned by the module
// (spec §4.3). The runtime starts it after all modules have started and
// cancels it before modules stop; it must honor the context. A duplicate
// (module, task name) pair is a registration error.
func (r Registry) AddTask(name string, t Task) error {
	if name == "" {
		return fmt.Errorf("module %q: task name must not be empty", r.owner)
	}
	if t == nil {
		return fmt.Errorf("module %q: task %q must not be nil", r.owner, name)
	}
	key := r.owner + "\x00" + name
	if _, dup := r.store.tasks[key]; dup {
		return fmt.Errorf("module %q: task %q is already registered", r.owner, name)
	}
	r.store.tasks[key] = TaskRegistration{Name: name, Owner: r.owner, Task: t}
	return nil
}

// AddEventHandler registers the handler for one normalized event type
// (spec §4.3, §11.4). The handler value is opaque here by the same
// policy as the capability store: the typed contract belongs to the
// eventing application, which binds the registrations before the event
// sources start. One handler per event type: fan-out is the handler's
// job, and a duplicate registration is a configuration error.
func (r Registry) AddEventHandler(eventType string, h any) error {
	if eventType == "" {
		return fmt.Errorf("module %q: event type must not be empty", r.owner)
	}
	if h == nil {
		return fmt.Errorf("module %q: handler for event type %q must not be nil", r.owner, eventType)
	}
	if prev, dup := r.store.handlers[eventType]; dup {
		return fmt.Errorf("module %q: event type %q already has a handler from module %q",
			r.owner, eventType, prev.owner)
	}
	r.store.handlers[eventType] = entry{value: h, owner: r.owner}
	return nil
}

// EventHandler returns the handler registered for one event type, typed
// by the caller.
func (r Registry) EventHandler(eventType string) (any, bool) {
	return r.store.EventHandler(eventType)
}

// HandlerTypes lists every event type that has a handler, sorted. During
// Register only earlier-initialized modules are visible; by Start all
// are.
func (r Registry) HandlerTypes() []string {
	return r.store.HandlerTypes()
}
