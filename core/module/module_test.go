package module

import (
	"context"
	"strings"
	"testing"

	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/health"
)

func TestInfoValidate(t *testing.T) {
	valid := Info{Dependencies: []Dependency{{Name: "db"}}}
	if err := valid.Validate("app"); err != nil {
		t.Fatalf("valid info rejected: %v", err)
	}

	cases := []struct {
		name string
		info Info
		want string
	}{
		{"empty dependency name", Info{Dependencies: []Dependency{{Name: ""}}}, "must not be empty"},
		{"self dependency", Info{Dependencies: []Dependency{{Name: "app"}}}, "depends on itself"},
		{"duplicate dependency", Info{Dependencies: []Dependency{{Name: "db"}, {Name: "db"}}}, "duplicate dependency"},
		{"bad required capability", Info{Requires: []capability.Name{"BAD_CAP"}}, "invalid required capability"},
		{"bad provided capability", Info{Provides: []capability.Name{""}}, "invalid provided capability"},
	}
	for _, tc := range cases {
		err := tc.info.Validate("app")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestStorePutCapability(t *testing.T) {
	s := NewStore()
	if err := s.Put("m1", capability.Network, "net-impl"); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Second provider rejected.
	err := s.Put("m2", capability.Network, "other")
	if err == nil || !strings.Contains(err.Error(), "already provided") {
		t.Errorf("duplicate provider: got %v", err)
	}
	// Invalid name rejected.
	if err := s.Put("m1", capability.Name("BAD"), "x"); err == nil {
		t.Error("invalid capability name should be rejected")
	}
	// Nil value rejected.
	if err := s.Put("m1", capability.Asset, nil); err == nil {
		t.Error("nil capability should be rejected")
	}
	// Read back with the type assertion owned by the caller.
	v, ok := s.Capability(capability.Network)
	if !ok {
		t.Fatal("capability not found")
	}
	if str, isStr := v.(string); !isStr || str != "net-impl" {
		t.Errorf("capability value = %v", v)
	}
	if _, ok := s.Capability(capability.Asset); ok {
		t.Error("missing capability should not be found")
	}
}

func TestRegistryViews(t *testing.T) {
	s := NewStore()
	r1 := NewRegistry(s, "m1")
	r2 := NewRegistry(s, "m2")

	if err := r1.PutCapability(capability.Signer, "signer-impl"); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Cross-module visibility and error attribution.
	if err := r2.PutCapability(capability.Signer, "x"); err == nil || !strings.Contains(err.Error(), "module \"m2\"") {
		t.Errorf("duplicate claim should blame m2, got %v", err)
	}
	if v, ok := r2.Capability(capability.Signer); !ok || v != "signer-impl" {
		t.Errorf("cross-module read failed: %v %v", v, ok)
	}
}

func TestRegistryAddHealthCheck(t *testing.T) {
	s := NewStore()
	r := NewRegistry(s, "m")
	ok := health.CheckFunc(func(ctx context.Context) (health.Status, error) { return health.Healthy, nil })
	if err := r.AddHealthCheck("m-check", ok); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.AddHealthCheck("m-check", ok); err == nil {
		t.Error("duplicate check should be rejected")
	}
	if err := r.AddHealthCheck("", ok); err == nil {
		t.Error("empty check name should be rejected")
	}
	if err := r.AddHealthCheck("nil-check", nil); err == nil {
		t.Error("nil checker should be rejected")
	}
}

func TestRegistryAddTask(t *testing.T) {
	s := NewStore()
	r := NewRegistry(s, "m1")
	if err := r.AddTask("", func(ctx context.Context) error { return nil }); err == nil {
		t.Error("empty task name should be rejected")
	}
	if err := r.AddTask("worker", nil); err == nil {
		t.Error("nil task should be rejected")
	}
	if err := r.AddTask("worker", func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.AddTask("worker", func(ctx context.Context) error { return nil }); err == nil {
		t.Error("duplicate task in one module should be rejected")
	}
	// Another module may reuse the name: ownership is per module.
	r2 := NewRegistry(s, "m2")
	if err := r2.AddTask("worker", func(ctx context.Context) error { return nil }); err != nil {
		t.Fatalf("add: %v", err)
	}

	tasks := s.Tasks()
	if len(tasks) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(tasks))
	}
	if tasks[0].Owner != "m1" || tasks[1].Owner != "m2" {
		t.Fatalf("tasks should sort by owner: %+v", tasks)
	}
}

func TestRegistryAddEventHandler(t *testing.T) {
	s := NewStore()
	r1 := NewRegistry(s, "m1")
	r2 := NewRegistry(s, "m2")

	handler := func(ctx context.Context, ev any) error { return nil }
	if err := r1.AddEventHandler("deposit.detected", handler); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r1.AddEventHandler("deposit.detected", handler); err == nil {
		t.Error("duplicate event type should be rejected")
	}
	if err := r2.AddEventHandler("deposit.detected", handler); err == nil {
		t.Error("a type claimed by another module should be rejected")
	}
	if err := r1.AddEventHandler("", handler); err == nil {
		t.Error("empty event type should be rejected")
	}
	if err := r1.AddEventHandler("withdrawn.detected", nil); err == nil {
		t.Error("nil handler should be rejected")
	}
	if err := r2.AddEventHandler("withdrawn.detected", handler); err != nil {
		t.Fatalf("add: %v", err)
	}

	types := s.HandlerTypes()
	if len(types) != 2 || types[0] != "deposit.detected" || types[1] != "withdrawn.detected" {
		t.Fatalf("handler types should be sorted: %v", types)
	}
	v, ok := r2.EventHandler("deposit.detected")
	if !ok || v == nil {
		t.Fatal("cross-module handler read failed")
	}
	if _, ok := s.EventHandler("missing"); ok {
		t.Error("missing event type should not be found")
	}
}
