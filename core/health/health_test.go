package health

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRegistryAdd(t *testing.T) {
	r := NewRegistry()
	ok := CheckFunc(func(ctx context.Context) (Status, error) { return Healthy, nil })
	if err := r.Add("db", ok); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.Add("db", ok); err == nil {
		t.Error("duplicate name should be rejected")
	}
	if err := r.Add("", ok); err == nil {
		t.Error("empty name should be rejected")
	}
	if err := r.Add("nil", nil); err == nil {
		t.Error("nil checker should be rejected")
	}
	if names := r.Names(); len(names) != 1 || names[0] != "db" {
		t.Errorf("names = %v", names)
	}
}

func TestAggregation(t *testing.T) {
	r := NewRegistry()
	_ = r.Add("a", CheckFunc(func(ctx context.Context) (Status, error) { return Healthy, nil }))
	_ = r.Add("b", CheckFunc(func(ctx context.Context) (Status, error) { return Degraded, nil }))
	rep := r.Run(context.Background())
	if rep.Status != Degraded {
		t.Errorf("a+b = %s, want Degraded", rep.Status)
	}

	r2 := NewRegistry()
	_ = r2.Add("a", CheckFunc(func(ctx context.Context) (Status, error) { return Healthy, nil }))
	_ = r2.Add("b", CheckFunc(func(ctx context.Context) (Status, error) { return Unhealthy, errors.New("down") }))
	rep2 := r2.Run(context.Background())
	if rep2.Status != Unhealthy {
		t.Errorf("a+b(unhealthy) = %s, want Unhealthy", rep2.Status)
	}
	if len(rep2.Results) != 2 {
		t.Fatalf("results = %+v", rep2.Results)
	}
	for _, res := range rep2.Results {
		if res.Name == "b" {
			if res.Status != Unhealthy || res.Error != "down" {
				t.Errorf("b result = %+v", res)
			}
		}
	}
}

func TestRunEmptyRegistryIsHealthy(t *testing.T) {
	if rep := NewRegistry().Run(context.Background()); rep.Status != Healthy || rep.Results != nil {
		t.Errorf("empty report = %+v", rep)
	}
}

func TestCheckErrorCountsAsUnhealthy(t *testing.T) {
	r := NewRegistry()
	_ = r.Add("err", CheckFunc(func(ctx context.Context) (Status, error) {
		return Healthy, errors.New("flaky")
	}))
	rep := r.Run(context.Background())
	if rep.Status != Unhealthy {
		t.Errorf("status = %s, want Unhealthy", rep.Status)
	}
	if rep.Results[0].Error != "flaky" {
		t.Errorf("error = %q", rep.Results[0].Error)
	}
}

func TestCheckContextExpiry(t *testing.T) {
	r := NewRegistry()
	_ = r.Add("slow", CheckFunc(func(ctx context.Context) (Status, error) {
		<-ctx.Done()
		return Healthy, ctx.Err()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	rep := r.Run(ctx)
	if rep.Status != Unhealthy {
		t.Errorf("status = %s, want Unhealthy on expiry", rep.Status)
	}
}

func TestResultsSortedByName(t *testing.T) {
	r := NewRegistry()
	_ = r.Add("zeta", CheckFunc(func(ctx context.Context) (Status, error) { return Healthy, nil }))
	_ = r.Add("alpha", CheckFunc(func(ctx context.Context) (Status, error) { return Healthy, nil }))
	rep := r.Run(context.Background())
	if rep.Results[0].Name != "alpha" || rep.Results[1].Name != "zeta" {
		t.Errorf("order = %+v", rep.Results)
	}
	if rep.Results[0].Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}
