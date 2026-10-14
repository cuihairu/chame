// Package runtime implements the Chame application lifecycle container
// (spec §4): module registration, dependency validation, ordered start,
// reverse-order graceful stop, startup-failure cleanup, and health
// aggregation.
//
// There are no package-level globals: every Runtime owns its registry,
// logger, and clock, injected through New.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/clock"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/lifecycle"
	"github.com/cuihairu/chame/core/logging"
	"github.com/cuihairu/chame/core/module"
)

// DefaultShutdownTimeout bounds the whole Stop sequence when the caller
// context carries no deadline (spec §5.3 example: 20s).
const DefaultShutdownTimeout = 20 * time.Second

// PhaseError marks the lifecycle phase (and module, when known) of a
// startup or shutdown failure, as required by spec §4.2.
type PhaseError struct {
	Phase  lifecycle.State
	Module string // empty when the failure is not tied to one module
	Err    error
}

func (e *PhaseError) Error() string {
	if e.Module == "" {
		return fmt.Sprintf("runtime: %s: %v", e.Phase, e.Err)
	}
	return fmt.Sprintf("runtime: %s module %q: %v", e.Phase, e.Module, e.Err)
}

func (e *PhaseError) Unwrap() error { return e.Err }

// Runtime is the lifecycle container (spec §4.1). Create with New,
// register modules, then Start or Run. A Runtime instance is single-use:
// once Stopped it cannot be restarted (spec §4.2).
type Runtime struct {
	mu              sync.Mutex
	state           lifecycle.State
	graph           *graph
	store           *module.Store
	logger          *slog.Logger
	clock           clock.Clock
	shutdownTimeout time.Duration
	started         []*node // modules started so far, in start order
}

// Option configures a Runtime.
type Option func(*Runtime)

// WithLogger injects the application logger. Default: a JSON slog logger
// at info level (see core/logging).
func WithLogger(l *slog.Logger) Option {
	return func(r *Runtime) { r.logger = l }
}

// WithClock injects the time source. Default: clock.System.
func WithClock(c clock.Clock) Option {
	return func(r *Runtime) { r.clock = c }
}

// WithShutdownTimeout bounds the graceful-stop sequence. Default:
// DefaultShutdownTimeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(r *Runtime) { r.shutdownTimeout = d }
}

// New returns a Runtime in StateNew accepting module registrations.
func New(opts ...Option) *Runtime {
	r := &Runtime{
		state:           lifecycle.StateNew,
		graph:           newGraph(),
		clock:           clock.System{},
		shutdownTimeout: DefaultShutdownTimeout,
	}
	for _, o := range opts {
		o(r)
	}
	if r.logger == nil {
		l, err := logging.New(logging.DefaultOptions())
		if err != nil { // cannot happen with validated defaults
			l = slog.New(slog.NewJSONHandler(io.Discard, nil))
		}
		r.logger = l
	}
	return r
}

// State reports the current lifecycle state.
func (r *Runtime) State() lifecycle.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

func (r *Runtime) setState(next lifecycle.State) error {
	if !lifecycle.CanTransition(r.state, next) {
		return fmt.Errorf("%w: %s -> %s", lifecycle.ErrInvalidState, r.state, next)
	}
	r.state = next
	return nil
}

// Register adds a module with its metadata (spec §4.4). Only allowed
// while the runtime is in StateNew; duplicate names are rejected here and
// the whole graph is validated again at start.
func (r *Runtime) Register(m module.Module, info module.Info) error {
	if m == nil {
		return errors.New("runtime: module must not be nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != lifecycle.StateNew {
		return &PhaseError{
			Phase: r.state,
			Err:   fmt.Errorf("register module %q: modules can only be registered before start", m.Name()),
		}
	}
	return r.graph.add(m, info, len(r.graph.order))
}

// Start runs the startup path (spec §4.2): configure (validate the
// graph), initialize (let modules declare capabilities and checks), then
// start modules in dependency order. On any failure the already-started
// modules are stopped in reverse order, the runtime ends in StateStopped,
// and the returned error names the phase and module.
func (r *Runtime) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.state != lifecycle.StateNew {
		err := &PhaseError{Phase: r.state, Err: fmt.Errorf("start: runtime is %s and single-use", r.state)}
		r.mu.Unlock()
		return err
	}
	_ = r.setState(lifecycle.StateConfiguring)
	order, err := r.graph.resolve()
	if err != nil {
		r.finishToStoppedLocked()
		r.mu.Unlock()
		return &PhaseError{Phase: lifecycle.StateConfiguring, Err: err}
	}
	r.store = module.NewStore()
	r.store.HealthChecks().WithClock(func() time.Time { return r.clock.Now() })
	_ = r.setState(lifecycle.StateInitializing)
	r.mu.Unlock()

	// Initializing: modules declare capabilities, checks, handlers
	// (spec §4.2). Register must not start long-running work.
	for _, n := range order {
		if err := ctx.Err(); err != nil {
			r.failStartup(order, nil)
			return &PhaseError{Phase: lifecycle.StateInitializing, Module: n.mod.Name(), Err: err}
		}
		if err := n.mod.Register(module.NewRegistry(r.store, n.mod.Name())); err != nil {
			r.failStartup(order, nil)
			return &PhaseError{Phase: lifecycle.StateInitializing, Module: n.mod.Name(), Err: err}
		}
	}

	// Starting: dependency order.
	r.mu.Lock()
	_ = r.setState(lifecycle.StateStarting)
	r.mu.Unlock()
	for _, n := range order {
		if err := ctx.Err(); err != nil {
			r.failStartup(order, nil)
			return &PhaseError{Phase: lifecycle.StateStarting, Module: n.mod.Name(), Err: err}
		}
		mlog := logging.WithModule(r.logger, n.mod.Name())
		mlog.Info("starting module")
		if err := n.mod.Start(ctx); err != nil {
			r.failStartup(order, nil)
			return &PhaseError{Phase: lifecycle.StateStarting, Module: n.mod.Name(), Err: err}
		}
		r.mu.Lock()
		r.started = append(r.started, n)
		r.mu.Unlock()
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.setState(lifecycle.StateRunning); err != nil {
		return &PhaseError{Phase: lifecycle.StateRunning, Err: err}
	}
	r.logger.Info("runtime started", slog.Int("modules", len(order)))
	return nil
}

// finishToStoppedLocked walks Stopping -> Stopped; caller holds the lock.
func (r *Runtime) finishToStoppedLocked() {
	_ = r.setState(lifecycle.StateStopping)
	_ = r.setState(lifecycle.StateStopped)
}

// failStartup stops every module started so far in reverse order (spec
// §4.1) and moves the runtime to StateStopped. The cleanup context is
// fresh, not the failed start context, and is bounded by the shutdown
// timeout.
func (r *Runtime) failStartup(_ []*node, cause error) {
	r.mu.Lock()
	_ = r.setState(lifecycle.StateStopping)
	r.mu.Unlock()
	r.stopStarted("startup cleanup")
	r.mu.Lock()
	_ = r.setState(lifecycle.StateStopped)
	r.mu.Unlock()
	if cause != nil {
		r.logger.Error("runtime start failed", slog.String("cause", cause.Error()))
	}
}

// Stop shuts the runtime down gracefully (spec §4.5): stop modules in
// reverse start order honoring per-module stop timeouts, aggregate the
// errors, and land in StateStopped.
func (r *Runtime) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.state != lifecycle.StateRunning {
		err := &PhaseError{Phase: r.state, Err: fmt.Errorf("stop: runtime is %s, only Running can stop", r.state)}
		r.mu.Unlock()
		return err
	}
	if err := r.setState(lifecycle.StateStopping); err != nil {
		r.mu.Unlock()
		return &PhaseError{Phase: r.state, Err: err}
	}
	r.mu.Unlock()

	r.logger.Info("runtime stopping")
	errs := r.stopStarted("stop")

	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.setState(lifecycle.StateStopped)
	r.logger.Info("runtime stopped")
	if err := errors.Join(errs...); err != nil {
		return &PhaseError{Phase: lifecycle.StateStopping, Err: err}
	}
	return nil
}

// Run starts the runtime and blocks until ctx is cancelled, then stops
// gracefully with the shutdown timeout. It returns the startup error if
// starting failed, otherwise the stop error if stopping failed.
func (r *Runtime) Run(ctx context.Context) error {
	if err := r.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	// The start context is done; stopping gets its own budget.
	return r.Stop(context.Background())
}

// Capability returns the value published under a capability name. The
// caller owns the type assertion.
func (r *Runtime) Capability(name capability.Name) (any, bool) {
	r.mu.Lock()
	store := r.store
	r.mu.Unlock()
	if store == nil {
		return nil, false
	}
	return store.Capability(name)
}

// Health runs every registered health check and returns the aggregated
// report (spec §15.3). Before startup there is nothing to check and the
// report is empty and healthy.
func (r *Runtime) Health(ctx context.Context) health.Report {
	r.mu.Lock()
	store := r.store
	r.mu.Unlock()
	if store == nil {
		return health.Report{Status: health.Healthy}
	}
	return store.HealthChecks().Run(ctx)
}

// stopStarted runs Stop on started modules in reverse order, each with a
// per-module timeout capped by the overall shutdown timeout. A module
// that ignores its deadline cannot block the sequence indefinitely
// (spec §4.5); its context expires and the sequence moves on. Stop errors
// are collected and returned.
func (r *Runtime) stopStarted(phase string) []error {
	r.mu.Lock()
	started := r.started
	r.started = nil
	budget := r.shutdownTimeout
	r.mu.Unlock()

	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		n := started[i]
		timeout := n.info.StopTimeout
		if timeout <= 0 || timeout > budget {
			timeout = budget
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		mlog := logging.WithModule(r.logger, n.mod.Name())
		mlog.Info("stopping module")
		if err := n.mod.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("module %q: %w", n.mod.Name(), err))
		}
		cancel()
	}
	return errs
}
