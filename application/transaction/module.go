package transaction

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/clock"
	"github.com/cuihairu/chame/core/module"
)

// Module composes the transaction pipeline into one runtime module
// (spec §4.4): it publishes the flow as the runtime's transaction
// capability and runs the recovery worker as a background task, so a
// composition root only wires the repository, the observation seam, and
// — when the service writes transactions — the chain's transactor.
// Explicit assembly, no reflective magic (spec §4.4).
type Module struct {
	repo       txmodel.Repository
	transactor txmodel.Transactor
	signers    []signer.Signer
	cfg        Config
	logger     *slog.Logger
	metrics    *Metrics
	now        clock.Clock

	worker *Worker
	flow   *Flow
}

// ModuleOption customizes the pipeline assembly.
type ModuleOption func(*Module)

// WithTransactor wires the chain-specific half and the signers the flow
// may use. A module without one still recovers stuck records — the
// recovery loop only needs the repository and the observation seam — but
// publishes no transaction capability and cannot open new operations.
func WithTransactor(t txmodel.Transactor, signers ...signer.Signer) ModuleOption {
	return func(m *Module) {
		m.transactor = t
		m.signers = signers
	}
}

// WithWorkerConfig tunes the recovery loop. Zero fields take the
// worker's defaults.
func WithWorkerConfig(cfg Config) ModuleOption {
	return func(m *Module) { m.cfg = cfg }
}

// WithWorkerMetrics wires the recovery loop's observability hooks.
func WithWorkerMetrics(metrics Metrics) ModuleOption {
	return func(m *Module) { m.metrics = &metrics }
}

// WithModuleLogger injects the diagnostic logger for the flow and the
// recovery loop. Default: slog.Default.
func WithModuleLogger(l *slog.Logger) ModuleOption {
	return func(m *Module) { m.logger = l }
}

// WithModuleClock injects the time source for the flow and the recovery
// loop. Default: clock.System.
func WithModuleClock(c clock.Clock) ModuleOption {
	return func(m *Module) { m.now = c }
}

// NewModule assembles the pipeline. The repository and the observation
// seam are required: without the seam the recovery loop would scan
// forever and never resolve anything.
func NewModule(repo txmodel.Repository, query StatusQuery, opts ...ModuleOption) (*Module, error) {
	rec, err := NewStatusReconciler(query)
	if err != nil {
		return nil, err
	}
	m := &Module{repo: repo}
	for _, opt := range opts {
		opt(m)
	}
	var wopts []Option
	if m.logger != nil {
		wopts = append(wopts, WithLogger(m.logger))
	}
	if m.metrics != nil {
		wopts = append(wopts, WithMetrics(*m.metrics))
	}
	if m.now != nil {
		wopts = append(wopts, WithClock(m.now))
	}
	w, err := NewWorker(repo, rec, m.cfg, wopts...)
	if err != nil {
		return nil, err
	}
	m.worker = w
	if m.transactor != nil {
		var fopts []FlowOption
		if len(m.signers) > 0 {
			fopts = append(fopts, WithFlowSigners(m.signers...))
		}
		if m.logger != nil {
			fopts = append(fopts, WithFlowLogger(m.logger))
		}
		if m.now != nil {
			fopts = append(fopts, WithFlowClock(m.now))
		}
		f, err := NewFlow(m.repo, m.transactor, fopts...)
		if err != nil {
			return nil, err
		}
		m.flow = f
	}
	return m, nil
}

// Name is the module identity in the dependency graph.
func (m *Module) Name() string { return "transaction" }

// Info describes what the assembly publishes: the transaction
// capability only when a transactor was wired. The composition root
// hands it to Runtime.Register alongside the module.
func (m *Module) Info() module.Info {
	info := module.Info{}
	if m.flow != nil {
		info.Provides = []capability.Name{capability.Transaction}
	}
	return info
}

// Register declares the pipeline on the module registry: the flow as the
// transaction capability, and the recovery loop as a background task the
// runtime starts and stops with the service (§10.4).
func (m *Module) Register(reg module.Registry) error {
	if m.flow != nil {
		if err := reg.PutCapability(capability.Transaction, m.flow); err != nil {
			return fmt.Errorf("module %q: %w", m.Name(), err)
		}
	}
	return reg.AddTask("recovery", m.worker.Run)
}

// Start and Stop hold no resources of their own: the runtime owns the
// recovery task's lifecycle, from start to the shutdown budget.
func (m *Module) Start(context.Context) error { return nil }
func (m *Module) Stop(context.Context) error  { return nil }

// ReconcileOne is the manual reconciliation entry the recovery contract
// names (§10.4): an operator can resolve one stuck record without
// waiting for the next scan.
func (m *Module) ReconcileOne(ctx context.Context, id txmodel.TransactionID) error {
	return m.worker.ReconcileOne(ctx, id)
}

// Flow returns the pipeline's write side, or nil when no transactor was
// wired. The capability store serves the same value to runtime peers,
// who own the type assertion.
func (m *Module) Flow() *Flow { return m.flow }
