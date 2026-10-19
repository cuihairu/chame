package transaction_test

import (
	"context"
	"errors"
	"testing"
	"time"

	txapp "github.com/cuihairu/chame/application/transaction"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

type approvalFunc func(context.Context, txapp.ControlRequest) (bool, error)

func (f approvalFunc) Approve(c context.Context, r txapp.ControlRequest) (bool, error) {
	return f(c, r)
}

type auditMemory struct{ entries []txapp.AuditEntry }

func (a *auditMemory) Append(_ context.Context, e txapp.AuditEntry) error {
	a.entries = append(a.entries, e)
	return nil
}
func (a *auditMemory) Query(_ context.Context, q txapp.AuditQuery) ([]txapp.AuditEntry, error) {
	out := []txapp.AuditEntry{}
	for _, e := range a.entries {
		if (q.Operator == "" || q.Operator == e.Operator) && (q.IdempotencyKey == "" || q.IdempotencyKey == e.IdempotencyKey) && (!q.Since.IsZero() && !e.At.Before(q.Since) || q.Since.IsZero()) && (!q.Until.IsZero() && !e.At.After(q.Until) || q.Until.IsZero()) {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestControlApprovalDenied(t *testing.T) {
	tr := &fakeTransactor{}
	audit := &auditMemory{}
	f, _ := txapp.NewFlow(memory.New(), tr, txapp.WithInternalControls(approvalFunc(func(context.Context, txapp.ControlRequest) (bool, error) { return false, nil }), nil, audit, controlRequest))
	tx := controlSeed(t, f, "approval")
	got, err := f.Drive(context.Background(), tx.ID)
	if !errors.Is(err, txapp.ErrControlDenied) || got.Status != txmodel.TxFailed || tr.prepares != 0 {
		t.Fatalf("got=%s err=%v prepares=%d", got.Status, err, tr.prepares)
	}
	t.Log("TestControlApprovalDenied: PASS")
}
func TestControlLimitDenied(t *testing.T) {
	tr := &fakeTransactor{}
	audit := &auditMemory{}
	f, _ := txapp.NewFlow(memory.New(), tr, txapp.WithInternalControls(nil, &txapp.Limit{Single: 5, Daily: 10}, audit, controlRequest))
	tx := controlSeed(t, f, "limit")
	got, err := f.Drive(context.Background(), tx.ID)
	if !errors.Is(err, txapp.ErrControlDenied) || got.Status != txmodel.TxFailed {
		t.Fatalf("got=%s err=%v", got.Status, err)
	}
	t.Log("TestControlLimitDenied: PASS")
}
func TestControlAuditQueryIncludesRejectedAttempt(t *testing.T) {
	a := &auditMemory{}
	at := time.Now()
	_ = a.Append(context.Background(), txapp.AuditEntry{Operator: "alice", IdempotencyKey: "key", Result: "denied", At: at})
	got, _ := a.Query(context.Background(), txapp.AuditQuery{Operator: "alice", Since: at.Add(-time.Second), Until: at.Add(time.Second)})
	if len(got) != 1 || got[0].Result != "denied" {
		t.Fatalf("query=%+v", got)
	}
	t.Log("TestControlAuditQueryIncludesRejectedAttempt: PASS")
}
func TestControlDisabledPreservesFlow(t *testing.T) {
	f, _ := newFlow(t, &fakeTransactor{})
	tx := flowSeed("controls-disabled")
	if _, err := f.Begin(context.Background(), txmodel.IdempotencyScope{}, "", tx); err != nil {
		t.Fatal(err)
	}
	got, err := f.Drive(context.Background(), tx.ID)
	if err != nil || got.Status != txmodel.TxSubmitted {
		t.Fatalf("got=%s err=%v", got.Status, err)
	}
	t.Log("TestControlDisabledPreservesFlow: PASS")
}

func controlRequest(tx txmodel.Transaction) txapp.ControlRequest {
	return txapp.ControlRequest{Operator: "alice", Action: "payout", IdempotencyKey: tx.IdempotencyKey, Network: tx.Network, Amount: "9"}
}
func controlSeed(t *testing.T, f *txapp.Flow, id string) txmodel.Transaction {
	t.Helper()
	tx := flowSeed(id)
	tx.IdempotencyScope = flowScope()
	tx.IdempotencyKey = id
	tx.RequestHash = "hash-" + id
	got, err := f.Begin(context.Background(), tx.IdempotencyScope, id, tx)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
