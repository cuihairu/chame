package transaction_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	txapp "github.com/cuihairu/chame/application/transaction"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/runtime"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

// fakeQuery answers the observation seam from a script: one status for
// every record, plus the list of records it was asked about.
type fakeQuery struct {
	mu     sync.Mutex
	status txmodel.TransactionStatus
	err    error
	seen   []txmodel.TransactionID
}

func (q *fakeQuery) StatusOf(_ context.Context, tx txmodel.Transaction) (txmodel.TransactionStatus, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seen = append(q.seen, tx.ID)
	if q.err != nil {
		return "", q.err
	}
	if q.status == "" {
		return tx.Status, nil
	}
	return q.status, nil
}

// stuckRecord builds one record the recovery loop would scan: submitted
// outcomes past their fresh window, unknowns, reorgs.
func stuckRecord(id, chainHash string, status txmodel.TransactionStatus) txmodel.Transaction {
	minuteAgo := time.Now().Add(-time.Minute)
	return txmodel.Transaction{
		ID:        txmodel.TransactionID(id),
		Network:   testNet,
		Status:    status,
		ChainHash: chainHash,
		CreatedAt: minuteAgo,
		UpdatedAt: minuteAgo,
	}
}

// testModule assembles the read-only pipeline over its own store and
// hands both back.
func testModule(t *testing.T, q txapp.StatusQuery, opts ...txapp.ModuleOption) (*txapp.Module, txmodel.Repository) {
	t.Helper()
	repo := memory.New()
	m, err := txapp.NewModule(repo, q, opts...)
	if err != nil {
		t.Fatalf("new module: %v", err)
	}
	return m, repo
}

// runtimeWithModule registers one module on a fresh runtime and starts
// it, returning the stop so a test shuts the runtime down first.
func runtimeWithModule(t *testing.T, m *txapp.Module) *runtime.Runtime {
	t.Helper()
	rt := runtime.New()
	if err := rt.Register(m, m.Info()); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Stop(context.Background()) })
	return rt
}

// TestModuleRecoversStuckRecordsUnderTheRuntime runs the assembled
// pipeline in a real runtime: the scan loop finds an unknown record and
// the observation seam's answer is applied to it.
func TestModuleRecoversStuckRecordsUnderTheRuntime(t *testing.T) {
	q := &fakeQuery{status: txmodel.TxIncluded}
	m, repo := testModule(t, q, txapp.WithWorkerConfig(txapp.Config{
		ScanInterval: 10 * time.Millisecond,
		MinStaleAge:  0,
		LeaseTTL:     time.Second,
		TaskTimeout:  time.Second,
	}))
	if err := repo.Create(context.Background(), stuckRecord("tx-module", "0xabc", txmodel.TxUnknown)); err != nil {
		t.Fatalf("create: %v", err)
	}

	runtimeWithModule(t, m)

	deadline := time.Now().Add(2 * time.Second)
	for {
		got, err := repo.Get(context.Background(), "tx-module")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status == txmodel.TxIncluded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("record stayed %s, want included", got.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestModulePublishesTheFlowWhenATransactorIsWired pins the assembly
// rule: the transaction capability exists only when the chain's write
// half was wired, and the capability store serves the flow the module
// holds.
func TestModulePublishesTheFlowWhenATransactorIsWired(t *testing.T) {
	q := &fakeQuery{}
	full, _ := testModule(t, q, txapp.WithTransactor(&fakeTransactor{}))
	if full.Flow() == nil {
		t.Fatal("a module with a transactor must hold a flow")
	}
	rt := runtimeWithModule(t, full)
	if _, ok := rt.Capability(capability.Transaction); !ok {
		t.Error("Capability(transaction) = not found, want the flow")
	}

	readOnly, _ := testModule(t, q)
	if readOnly.Flow() != nil {
		t.Error("a module without a transactor must hold no flow")
	}
	rt2 := runtimeWithModule(t, readOnly)
	if _, ok := rt2.Capability(capability.Transaction); ok {
		t.Error("Capability(transaction) = found, want nothing published")
	}
}

// TestModuleReconcilesOneManually drives the manual reconciliation
// entry (§10.4): one record, no scan loop needed.
func TestModuleReconcilesOneManually(t *testing.T) {
	q := &fakeQuery{status: txmodel.TxConfirmed}
	m, repo := testModule(t, q)
	if err := repo.Create(context.Background(), stuckRecord("tx-manual", "0xbb", txmodel.TxUnknown)); err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := m.ReconcileOne(context.Background(), "tx-manual"); err != nil {
		t.Fatalf("reconcile one: %v", err)
	}
	got, err := repo.Get(context.Background(), "tx-manual")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != txmodel.TxConfirmed {
		t.Errorf("status = %s, want confirmed", got.Status)
	}

	if err := m.ReconcileOne(context.Background(), "tx-missing"); !errors.Is(err, txmodel.ErrRecordNotFound) {
		t.Errorf("reconcile of a missing record = %v, want record not found", err)
	}
}

// TestModuleRequiresRepositoryAndSeam refuses assemblies that would
// scan forever: the worker and the reconciler both check their
// dependencies at construction.
func TestModuleRequiresRepositoryAndSeam(t *testing.T) {
	if _, err := txapp.NewModule(nil, &fakeQuery{}); err == nil {
		t.Fatal("module without a repository: err = nil, want error")
	}
	if _, err := txapp.NewModule(memory.New(), nil); err == nil {
		t.Fatal("module without an observation seam: err = nil, want error")
	}
}
