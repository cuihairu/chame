// Package health defines the health-check model (spec §15.3): per-module
// checks and an aggregated report used by the runtime to describe
// readiness.
package health

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// Status is the outcome of a single health check.
type Status string

const (
	// Healthy means the component serves normally.
	Healthy Status = "healthy"
	// Degraded means the component serves with reduced capability.
	Degraded Status = "degraded"
	// Unhealthy means the component is not serving.
	Unhealthy Status = "unhealthy"
)

// Checker reports the health of one component.
type Checker interface {
	// CheckHealth inspects the component. It must respect the context
	// deadline and must not block indefinitely.
	CheckHealth(ctx context.Context) (Status, error)
}

// CheckFunc adapts a function to Checker.
type CheckFunc func(ctx context.Context) (Status, error)

// CheckHealth implements Checker.
func (f CheckFunc) CheckHealth(ctx context.Context) (Status, error) { return f(ctx) }

// Result is the outcome of one named check.
type Result struct {
	Name      string    `json:"name"`
	Status    Status    `json:"status"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Report aggregates all registered checks at one point in time.
type Report struct {
	Status  Status   `json:"status"`
	Results []Result `json:"results"`
}

// Scope is the health-check scope (spec §15.3).
type Scope string

const (
	// Liveness reports whether the process is still running normally.
	Liveness Scope = "liveness"
	// Readiness reports whether the service can serve its current
	// capabilities.
	Readiness Scope = "readiness"
	// Dependency reports whether a specific dependency is available. A
	// dependency check names the dependencies it needs, so a service can
	// exclude the ones it does not use (spec §15.3: an unavailable optional
	// Solana module must not make a service that never uses Solana
	// unhealthy).
	Dependency Scope = "dependency"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool {
	switch s {
	case Liveness, Readiness, Dependency:
		return true
	default:
		return false
	}
}

// check is one registered health check.
type check struct {
	name      string
	scope     Scope
	dependsOn []string
	checker   Checker
}

// Registry collects named checkers. Registration is not concurrency-safe;
// the runtime registers checks during initialization only.
type Registry struct {
	byName map[string]check
	order  []string
	clock  func() time.Time
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]check), clock: time.Now}
}

// WithClock overrides the timestamp source (used by tests).
func (r *Registry) WithClock(now func() time.Time) *Registry {
	r.clock = now
	return r
}

// Add registers an unconditional dependency checker under name: it is
// always counted in every report. Prefer AddDependency when the check is
// for an optional module (spec §15.3). Duplicate names are rejected.
func (r *Registry) Add(name string, c Checker) error {
	return r.AddDependency(name, nil, c)
}

// AddScoped registers a checker under name in the given scope. Duplicate
// names are rejected.
func (r *Registry) AddScoped(name string, scope Scope, c Checker) error {
	if !scope.Valid() {
		return fmt.Errorf("health: unknown scope %q", scope)
	}
	return r.add(name, check{name: name, scope: scope, checker: c})
}

// AddDependency registers a dependency checker under name. deps names the
// dependencies the check needs, such as the module that provides them; a
// service that uses none of them excludes the check from its readiness
// report. Duplicate names are rejected.
func (r *Registry) AddDependency(name string, deps []string, c Checker) error {
	return r.add(name, check{name: name, scope: Dependency, dependsOn: deps, checker: c})
}

func (r *Registry) add(name string, c check) error {
	if name == "" {
		return fmt.Errorf("health: check name must not be empty")
	}
	if c.checker == nil {
		return fmt.Errorf("health: check %q must not be nil", name)
	}
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("health: check %q already registered", name)
	}
	r.byName[name] = c
	r.order = append(r.order, name)
	return nil
}

// Names lists registered check names in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// NamesInScope lists the check names registered in one scope.
func (r *Registry) NamesInScope(scope Scope) []string {
	var out []string
	for _, name := range r.order {
		if r.byName[name].scope == scope {
			out = append(out, name)
		}
	}
	return out
}

// DependsOn returns the dependencies a check was registered with.
func (r *Registry) DependsOn(name string) ([]string, bool) {
	c, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	return c.dependsOn, true
}

// Run executes every registered check with the given context and
// aggregates the results: any Unhealthy makes the report Unhealthy,
// otherwise any Degraded makes it Degraded, otherwise Healthy. A check
// whose context expires is recorded as Unhealthy with the context error.
func (r *Registry) Run(ctx context.Context) Report {
	return r.run(ctx, nil)
}

// RunScope executes the checks registered in one scope and aggregates them
// with the same rules as Run.
func (r *Registry) RunScope(ctx context.Context, scope Scope) Report {
	if !scope.Valid() {
		return Report{Status: Unhealthy}
	}
	return r.run(ctx, &scope)
}

// RunReadiness executes the liveness and readiness checks plus every
// dependency check whose dependencies intersect used, and aggregates the
// results. used is the set of dependencies the caller actually relies on —
// for an optional module, the module name; for storage, the store name.
// A check with no dependencies is always counted: it is unconditional.
func (r *Registry) RunReadiness(ctx context.Context, used []string) Report {
	deps := make(map[string]bool, len(used))
	for _, d := range used {
		deps[d] = true
	}
	keep := func(c check) bool {
		if c.scope != Dependency {
			return true
		}
		if len(c.dependsOn) == 0 {
			return true
		}
		for _, d := range c.dependsOn {
			if deps[d] {
				return true
			}
		}
		return false
	}
	return r.runFiltered(ctx, keep)
}

// run executes every registered check selected by scope, or every check
// when scope is nil.
func (r *Registry) run(ctx context.Context, scope *Scope) Report {
	keep := func(c check) bool {
		if scope == nil {
			return true
		}
		return c.scope == *scope
	}
	return r.runFiltered(ctx, keep)
}

func (r *Registry) runFiltered(ctx context.Context, keep func(check) bool) Report {
	names := make([]string, 0, len(r.order))
	for _, name := range r.order {
		if keep(r.byName[name]) {
			names = append(names, name)
		}
	}
	sort.Strings(names) // stable, deterministic reports

	report := Report{Status: Healthy}
	for _, name := range names {
		res := Result{Name: name, Status: Healthy, Timestamp: r.clock()}
		status, err := r.byName[name].checker.CheckHealth(ctx)
		switch {
		case err != nil:
			res.Status = Unhealthy
			res.Error = err.Error()
		default:
			res.Status = status
		}
		if res.Status == Unhealthy {
			report.Status = Unhealthy
		} else if res.Status == Degraded && report.Status == Healthy {
			report.Status = Degraded
		}
		report.Results = append(report.Results, res)
	}
	return report
}
