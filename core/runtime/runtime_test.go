package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/lifecycle"
	"github.com/cuihairu/chame/core/module"
)

// recModule is a test module recording its lifecycle calls.
type recModule struct {
	name     string
	provides []capability.Name
	requires []capability.Name

	startErr error
	stopErr  error

	mu     sync.Mutex
	events *[]string
}

func (m *recModule) Name() string { return m.name }

func (m *recModule) Register(r module.Registry) error {
	for _, c := range m.provides {
		if err := r.PutCapability(c, m.name+"-impl"); err != nil {
			return err
		}
	}
	return nil
}

func (m *recModule) Start(ctx context.Context) error {
	m.mu.Lock()
	*m.events = append(*m.events, "start:"+m.name)
	m.mu.Unlock()
	return m.startErr
}

func (m *recModule) Stop(ctx context.Context) error {
	m.mu.Lock()
	*m.events = append(*m.events, "stop:"+m.name)
	m.mu.Unlock()
	return m.stopErr
}

func newRec(name string, events *[]string) *recModule {
	return &recModule{name: name, events: events}
}

func drain(events *[]string) []string {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	out := make([]string, len(*events))
	copy(out, *events)
	*events = nil
	return out
}

var eventsMu sync.Mutex

// --- spec §19 Phase 1 acceptance ---

func TestMinimalRuntimeWithoutModulesRunsAndStops(t *testing.T) {
	rt := New()
	if rt.State() != lifecycle.StateNew {
		t.Fatalf("initial state = %s", rt.State())
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if rt.State() != lifecycle.StateRunning {
		t.Fatalf("state after start = %s", rt.State())
	}
	rep := rt.Health(context.Background())
	if rep.Status != health.Healthy || len(rep.Results) != 0 {
		t.Errorf("empty health report = %+v", rep)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if rt.State() != lifecycle.StateStopped {
		t.Fatalf("state after stop = %s", rt.State())
	}
}

func TestModuleRegisterStartStop(t *testing.T) {
	var events []string
	m := newRec("app", &events)
	rt := New()
	if err := rt.Register(m, module.Info{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rt.Run(ctx) }()
	// Wait until running, then cancel.
	waitState(t, rt, lifecycle.StateRunning, 2*time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
	got := drain(&events)
	want := []string{"start:app", "stop:app"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestMissingRequiredDependencyFailsBeforeStart(t *testing.T) {
	var events []string
	rt := New()
	_ = rt.Register(newRec("app", &events), module.Info{
		Dependencies: []module.Dependency{{Name: "database"}},
	})
	err := rt.Start(context.Background())
	if err == nil {
		t.Fatal("start should fail")
	}
	var pe *PhaseError
	if !errors.As(err, &pe) || pe.Phase != lifecycle.StateConfiguring {
		t.Fatalf("error = %v, want PhaseError in Configuring", err)
	}
	if !strings.Contains(err.Error(), `"database"`) {
		t.Errorf("error should name the missing dependency: %v", err)
	}
	// Nothing started, runtime is spent.
	if rt.State() != lifecycle.StateStopped {
		t.Errorf("state = %s, want Stopped", rt.State())
	}
	if got := drain(&events); len(got) != 0 {
		t.Errorf("no module call expected, got %v", got)
	}
}

func TestOptionalMissingDependencyIsFine(t *testing.T) {
	var events []string
	rt := New()
	_ = rt.Register(newRec("app", &events), module.Info{
		Dependencies: []module.Dependency{{Name: "cache", Optional: true}},
	})
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = rt.Stop(context.Background())
}

func TestDependencyCycleFailsWithReadableLoop(t *testing.T) {
	var events []string
	rt := New()
	a := newRec("a", &events)
	b := newRec("b", &events)
	c := newRec("c", &events)
	_ = rt.Register(a, module.Info{Dependencies: []module.Dependency{{Name: "b"}}})
	_ = rt.Register(b, module.Info{Dependencies: []module.Dependency{{Name: "c"}}})
	_ = rt.Register(c, module.Info{Dependencies: []module.Dependency{{Name: "a"}}})
	err := rt.Start(context.Background())
	if err == nil {
		t.Fatal("start should fail on cycle")
	}
	for _, name := range []string{"a", "b", "c"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("cycle error should spell out the loop, got: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "->") {
		t.Errorf("cycle error should use -> notation, got: %v", err)
	}
	if got := drain(&events); len(got) != 0 {
		t.Errorf("no module call expected, got %v", got)
	}
}

func TestStartOrderFollowsDependencies(t *testing.T) {
	var events []string
	rt := New()
	// Register in reverse dependency order to prove ordering comes from
	// the graph, not registration order.
	_ = rt.Register(newRec("app", &events), module.Info{
		Dependencies: []module.Dependency{{Name: "db"}},
	})
	_ = rt.Register(newRec("db", &events), module.Info{})
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := drain(&events)
	want := []string{"start:db", "start:app", "stop:app", "stop:db"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}

func TestStartupFailureStopsStartedModulesInReverse(t *testing.T) {
	var events []string
	ok1 := newRec("ok1", &events)
	ok2 := newRec("ok2", &events)
	bad := newRec("bad", &events)
	bad.startErr = errors.New("boom")

	rt := New()
	_ = rt.Register(ok1, module.Info{})
	_ = rt.Register(ok2, module.Info{Dependencies: []module.Dependency{{Name: "ok1"}}})
	_ = rt.Register(bad, module.Info{Dependencies: []module.Dependency{{Name: "ok2"}}})

	err := rt.Start(context.Background())
	if err == nil {
		t.Fatal("start should fail")
	}
	var pe *PhaseError
	if !errors.As(err, &pe) || pe.Phase != lifecycle.StateStarting || pe.Module != "bad" {
		t.Fatalf("error = %v, want Starting PhaseError naming module bad", err)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should carry the cause: %v", err)
	}
	got := drain(&events)
	want := []string{"start:ok1", "start:ok2", "start:bad", "stop:ok2", "stop:ok1"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("events = %v, want %v", got, want)
	}
	// The failed runtime cannot be reused.
	if rt.State() != lifecycle.StateStopped {
		t.Errorf("state = %s, want Stopped", rt.State())
	}
	if err := rt.Start(context.Background()); err == nil {
		t.Error("restarting a stopped runtime must fail")
	}
}

func TestDuplicateRegistrationRejected(t *testing.T) {
	var events []string
	rt := New()
	if err := rt.Register(newRec("dup", &events), module.Info{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	err := rt.Register(newRec("dup", &events), module.Info{})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate register: got %v", err)
	}
}

func TestRegisterAndStartGuards(t *testing.T) {
	var events []string
	rt := New()
	if err := rt.Register(nil, module.Info{}); err == nil {
		t.Error("nil module must be rejected")
	}
	empty := newRec("", &events)
	if err := rt.Register(empty, module.Info{}); err == nil {
		t.Error("empty module name must be rejected")
	}
	m := newRec("m", &events)
	if err := rt.Register(m, module.Info{Dependencies: []module.Dependency{{Name: "m"}}}); err == nil {
		t.Error("self dependency must be rejected")
	}
	if err := rt.Register(m, module.Info{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Registration closed after New.
	if err := rt.Register(newRec("late", &events), module.Info{}); err == nil {
		t.Error("register after start must fail")
	}
	// Double start and stop while running.
	if err := rt.Start(context.Background()); err == nil {
		t.Error("double start must fail")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := rt.Stop(context.Background()); err == nil {
		t.Error("double stop must fail")
	}
	if err := rt.Run(context.Background()); err == nil {
		t.Error("Run on a spent runtime must fail")
	}
}

func TestCapabilityDeclarationAndLookup(t *testing.T) {
	rt := New()
	provider := newRec("netmod", &[]string{})
	provider.provides = []capability.Name{capability.Network}
	consumer := newRec("app", &[]string{})
	consumer.requires = []capability.Name{capability.Network}
	_ = rt.Register(provider, module.Info{Provides: provider.provides})
	_ = rt.Register(consumer, module.Info{Requires: consumer.requires})
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	v, ok := rt.Capability(capability.Network)
	if !ok || v != "netmod-impl" {
		t.Errorf("capability = %v, %v", v, ok)
	}
	_ = rt.Stop(context.Background())
}

func TestMissingCapabilityFailsConfigure(t *testing.T) {
	var events []string
	rt := New()
	m := newRec("app", &events)
	m.requires = []capability.Name{capability.Signer}
	_ = rt.Register(m, module.Info{Requires: m.requires})
	err := rt.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "signer") {
		t.Fatalf("start = %v, want missing capability error", err)
	}
}

func TestDuplicateCapabilityProviderFailsConfigure(t *testing.T) {
	rt := New()
	a := newRec("a", &[]string{})
	b := newRec("b", &[]string{})
	_ = rt.Register(a, module.Info{Provides: []capability.Name{capability.Event}})
	_ = rt.Register(b, module.Info{Provides: []capability.Name{capability.Event}})
	if err := rt.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("start = %v, want duplicate provider error", err)
	}
}

func TestHealthChecksAggregate(t *testing.T) {
	rt := New()
	h := newHealthModule("hm")
	_ = rt.Register(h, module.Info{})
	_ = rt.Start(context.Background())
	rep := rt.Health(context.Background())
	if rep.Status != health.Unhealthy {
		t.Errorf("report = %+v, want Unhealthy", rep)
	}
	if len(rep.Results) != 1 || rep.Results[0].Name != "hm-check" || rep.Results[0].Status != health.Unhealthy {
		t.Errorf("results = %+v", rep.Results)
	}
	_ = rt.Stop(context.Background())
}

func TestStopErrorsAreAggregated(t *testing.T) {
	var events []string
	a := newRec("a", &events)
	b := newRec("b", &events)
	a.stopErr = errors.New("cannot close")
	rt := New()
	_ = rt.Register(a, module.Info{})
	_ = rt.Register(b, module.Info{})
	_ = rt.Start(context.Background())
	err := rt.Stop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot close") {
		t.Fatalf("stop = %v, want aggregated module error", err)
	}
	// Both modules still stopped despite the failure.
	got := drain(&events)
	if len(got) != 4 {
		t.Errorf("events = %v", got)
	}
	if rt.State() != lifecycle.StateStopped {
		t.Errorf("state = %s, want Stopped", rt.State())
	}
}

func TestPerModuleStopTimeoutHonored(t *testing.T) {
	var events []string
	slow := &blockingModule{name: "slow", events: &events, hold: 300 * time.Millisecond}
	rt := New()
	_ = rt.Register(slow, module.Info{StopTimeout: 50 * time.Millisecond})
	_ = rt.Start(context.Background())
	begin := time.Now()
	err := rt.Stop(context.Background())
	// The timeout is surfaced, never swallowed (spec §4.5: 未完成任务不能
	// 被标记为成功).
	if err == nil || !strings.Contains(err.Error(), "slow") {
		t.Fatalf("stop = %v, want error naming the timed-out module", err)
	}
	if elapsed := time.Since(begin); elapsed > 250*time.Millisecond {
		t.Errorf("stop took %s, per-module timeout was not honored", elapsed)
	}
	if rt.State() != lifecycle.StateStopped {
		t.Errorf("state = %s, want Stopped", rt.State())
	}
}

// blockingModule honors its stop context: a module that respects the
// deadline cannot block the shutdown sequence.
type blockingModule struct {
	name   string
	events *[]string
	hold   time.Duration
}

func (m *blockingModule) Name() string                     { return m.name }
func (m *blockingModule) Register(r module.Registry) error { return nil }
func (m *blockingModule) Start(ctx context.Context) error  { return nil }
func (m *blockingModule) Stop(ctx context.Context) error {
	select {
	case <-time.After(m.hold):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitState(t *testing.T, rt *Runtime, want lifecycle.State, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if rt.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state never reached %s (now %s)", want, rt.State())
}

func newHealthModule(name string) *healthModule {
	return &healthModule{name: name}
}

type healthModule struct {
	name string
}

func (m *healthModule) Name() string { return m.name }
func (m *healthModule) Register(r module.Registry) error {
	return r.AddHealthCheck("hm-check", health.CheckFunc(func(ctx context.Context) (health.Status, error) {
		return health.Unhealthy, errors.New("down")
	}))
}
func (m *healthModule) Start(ctx context.Context) error { return nil }
func (m *healthModule) Stop(ctx context.Context) error  { return nil }

// taskModule registers one background task through the registry (spec
// §4.3) and records what the task observed. The recorder is locked: the
// task runs on its own goroutine.
type taskModule struct {
	name string
	body func(ctx context.Context) error

	mu     sync.Mutex
	events []string
}

func (m *taskModule) Name() string { return m.name }
func (m *taskModule) Register(r module.Registry) error {
	return r.AddTask("worker", func(ctx context.Context) error {
		m.append("task:start:" + m.name)
		defer m.append("task:done:" + m.name) // survives a panicking body
		return m.body(ctx)
	})
}
func (m *taskModule) Start(ctx context.Context) error { return nil }
func (m *taskModule) Stop(ctx context.Context) error  { return nil }

func (m *taskModule) append(e string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}

func (m *taskModule) drain() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.events...)
}

func waitForTaskEvent(t *testing.T, m *taskModule, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		for _, e := range m.drain() {
			if e == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event %q never happened, got %v", want, m.drain())
}

func TestTaskLifecycleWithRuntime(t *testing.T) {
	m := &taskModule{name: "eventing", body: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	r := New(WithShutdownTimeout(2 * time.Second))
	if err := r.Register(m, module.Info{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTaskEvent(t, m, "task:start:eventing", 2*time.Second)

	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	got := m.drain()
	// The task starts after the modules and stops, cancelled, before the
	// first module stop (spec §4.5 order).
	if len(got) != 2 || got[0] != "task:start:eventing" || got[1] != "task:done:eventing" {
		t.Fatalf("want the task started and cancelled, got %v", got)
	}
	if r.State() != lifecycle.StateStopped {
		t.Fatalf("state: %s", r.State())
	}
}

func TestTaskPanicDoesNotTakeTheRuntimeDown(t *testing.T) {
	m := &taskModule{name: "chaos", body: func(ctx context.Context) error {
		panic("worker exploded")
	}}
	r := New(WithShutdownTimeout(2 * time.Second))
	if err := r.Register(m, module.Info{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTaskEvent(t, m, "task:done:chaos", 2*time.Second)
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("a panicking task must not break the stop sequence: %v", err)
	}
}

func TestTaskEarlyExitLeavesRuntimeUsable(t *testing.T) {
	m := &taskModule{name: "broken", body: func(ctx context.Context) error {
		return errors.New("cannot connect")
	}}
	r := New(WithShutdownTimeout(2 * time.Second))
	if err := r.Register(m, module.Info{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitForTaskEvent(t, m, "task:done:broken", 2*time.Second)
	// The first release does not restart a dead task, but the runtime
	// itself stays healthy and stops cleanly.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

func TestTaskIgnoringDeadlineCannotBlockStop(t *testing.T) {
	hang := &taskModule{name: "hang", body: func(ctx context.Context) error {
		<-make(chan struct{}) // ignores the context on purpose
		return nil
	}}
	r := New(WithShutdownTimeout(50 * time.Millisecond))
	if err := r.Register(hang, module.Info{}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	started := time.Now()
	_ = r.Stop(context.Background())
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("a hanging task must not block the stop sequence, took %s", elapsed)
	}
}
