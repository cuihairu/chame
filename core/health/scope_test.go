package health

import (
	"context"
	"errors"
	"testing"
)

// staticCheck returns a Checker with a fixed outcome.
func staticCheck(status Status, err error) Checker {
	return CheckFunc(func(context.Context) (Status, error) { return status, err })
}

func TestScopeValid(t *testing.T) {
	for _, s := range []Scope{Liveness, Readiness, Dependency} {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
	}
	for _, s := range []Scope{"", "liveness ", "LIVENESS"} {
		if s.Valid() {
			t.Errorf("%q must not be valid", s)
		}
	}
}

func TestAddScopedRejectsUnknownScope(t *testing.T) {
	r := NewRegistry()
	if err := r.AddScoped("x", Scope("bogus"), staticCheck(Healthy, nil)); err == nil {
		t.Fatal("unknown scope must be rejected")
	}
}

func TestNamesInScope(t *testing.T) {
	r := NewRegistry()
	if err := r.Add("evm.mainnet", staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddScoped("process", Liveness, staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddScoped("api", Readiness, staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	if got := r.NamesInScope(Dependency); len(got) != 1 || got[0] != "evm.mainnet" {
		t.Errorf("dependency checks = %v", got)
	}
	if got := r.NamesInScope(Liveness); len(got) != 1 || got[0] != "process" {
		t.Errorf("liveness checks = %v", got)
	}
	if got := r.NamesInScope(Readiness); len(got) != 1 || got[0] != "api" {
		t.Errorf("readiness checks = %v", got)
	}
}

func TestDependsOn(t *testing.T) {
	r := NewRegistry()
	if err := r.AddDependency("solana.mainnet", []string{"solana"}, staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	deps, ok := r.DependsOn("solana.mainnet")
	if !ok || len(deps) != 1 || deps[0] != "solana" {
		t.Errorf("DependsOn = %v, %v", deps, ok)
	}
	if _, ok := r.DependsOn("missing"); ok {
		t.Error("unknown check must not resolve")
	}
}

// TestRunReadinessExcludesUnusedDependencies pins spec §15.3: an
// unavailable optional Solana module must not make a service that never
// uses Solana unhealthy.
func TestRunReadinessExcludesUnusedDependencies(t *testing.T) {
	r := NewRegistry()
	if err := r.AddScoped("process", Liveness, staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddScoped("api", Readiness, staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDependency("evm.mainnet", []string{"evm"}, staticCheck(Healthy, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.AddDependency("solana.mainnet", []string{"solana"}, staticCheck(Unhealthy, errors.New("cluster down"))); err != nil {
		t.Fatal(err)
	}

	// The operator view sees everything: the optional module is down.
	if got := r.Run(context.Background()); got.Status != Unhealthy {
		t.Errorf("overall = %s, want unhealthy", got.Status)
	}

	// A service that uses only EVM is ready: the Solana check is out of
	// its scope.
	got := r.RunReadiness(context.Background(), []string{"evm"})
	if got.Status != Healthy {
		t.Errorf("readiness = %s, want healthy: %+v", got.Status, got.Results)
	}
	for _, res := range got.Results {
		if res.Name == "solana.mainnet" {
			t.Error("unused dependency must not appear in the readiness report")
		}
	}

	// A service that does use Solana is not ready.
	got = r.RunReadiness(context.Background(), []string{"evm", "solana"})
	if got.Status != Unhealthy {
		t.Errorf("readiness with solana = %s, want unhealthy", got.Status)
	}
}

// TestRunReadinessCountsUnconditionalDependencies pins that a check with
// no declared dependencies is always counted: it is not optional.
func TestRunReadinessCountsUnconditionalDependencies(t *testing.T) {
	r := NewRegistry()
	if err := r.Add("postgres", staticCheck(Unhealthy, errors.New("disk full"))); err != nil {
		t.Fatal(err)
	}
	if got := r.RunReadiness(context.Background(), nil); got.Status != Unhealthy {
		t.Errorf("readiness = %s, want unhealthy", got.Status)
	}
}

func TestRunScope(t *testing.T) {
	r := NewRegistry()
	if err := r.AddScoped("process", Liveness, staticCheck(Degraded, nil)); err != nil {
		t.Fatal(err)
	}
	if err := r.Add("evm.mainnet", staticCheck(Unhealthy, errors.New("down"))); err != nil {
		t.Fatal(err)
	}
	if got := r.RunScope(context.Background(), Liveness); got.Status != Degraded {
		t.Errorf("liveness = %s, want degraded", got.Status)
	}
	if got := r.RunScope(context.Background(), Dependency); got.Status != Unhealthy {
		t.Errorf("dependency = %s, want unhealthy", got.Status)
	}
	if got := r.RunScope(context.Background(), Readiness); got.Status != Healthy || len(got.Results) != 0 {
		t.Errorf("readiness = %+v, want empty healthy", got)
	}
	if got := r.RunScope(context.Background(), Scope("bogus")); got.Status != Unhealthy {
		t.Errorf("unknown scope = %s, want unhealthy", got.Status)
	}
}
