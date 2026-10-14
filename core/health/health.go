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

// Registry collects named checkers. Registration is not concurrency-safe;
// the runtime registers checks during initialization only.
type Registry struct {
	byName map[string]Checker
	order  []string
	clock  func() time.Time
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Checker), clock: time.Now}
}

// WithClock overrides the timestamp source (used by tests).
func (r *Registry) WithClock(now func() time.Time) *Registry {
	r.clock = now
	return r
}

// Add registers a checker under name. Duplicate names are rejected.
func (r *Registry) Add(name string, c Checker) error {
	if name == "" {
		return fmt.Errorf("health: check name must not be empty")
	}
	if c == nil {
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

// Run executes every registered check with the given context and
// aggregates the results: any Unhealthy makes the report Unhealthy,
// otherwise any Degraded makes it Degraded, otherwise Healthy. A check
// whose context expires is recorded as Unhealthy with the context error.
func (r *Registry) Run(ctx context.Context) Report {
	names := make([]string, len(r.order))
	copy(names, r.order)
	sort.Strings(names) // stable, deterministic reports

	report := Report{Status: Healthy}
	for _, name := range names {
		res := Result{Name: name, Status: Healthy, Timestamp: r.clock()}
		status, err := r.byName[name].CheckHealth(ctx)
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
