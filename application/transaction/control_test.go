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

// An amount string too large for int64 must be refused outright: the old
// accumulation wrapped silently, and 2^64+1 collapsed to 1 — a payout far
// beyond the budget passing as pocket change.
func TestLimitRejectsOverflowingAmount(t *testing.T) {
	l := &txapp.Limit{Single: 5, Daily: 10}
	for _, amount := range []string{
		"18446744073709551617", // 2^64+1 wrapped to 1
		"9223372036854775808",  // max int64 + 1 wrapped negative
		"99999999999999999999",
		"12a",
		"-5",
		"",
	} {
		r := txapp.ControlRequest{Action: "payout", Amount: amount, At: time.Now()}
		if _, err := l.Check(r); !errors.Is(err, txapp.ErrControlDenied) {
			t.Fatalf("amount %q must be refused, got %v", amount, err)
		}
	}
	// The decodable maximum is still decidable on its own merits: parsing
	// must not be the refusal reason.
	if _, err := (&txapp.Limit{}).Check(txapp.ControlRequest{Action: "payout", Amount: "9223372036854775807", At: time.Now()}); err != nil {
		t.Fatalf("max int64 must still parse: %v", err)
	}
	t.Log("TestLimitRejectsOverflowingAmount: PASS")
}

func TestLimitReleaseReturnsBudget(t *testing.T) {
	l := &txapp.Limit{Single: 6, Daily: 10}
	day := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	r := txapp.ControlRequest{Action: "payout", Amount: "6", At: day}
	if charged, err := l.Check(r); err != nil || !charged {
		t.Fatalf("first check must charge: charged=%v err=%v", charged, err)
	}
	if _, err := l.Check(r); !errors.Is(err, txapp.ErrControlDenied) {
		t.Fatalf("the day budget must be exhausted, got %v", err)
	}
	l.Release(r)
	if charged, err := l.Check(r); err != nil || !charged {
		t.Fatalf("released budget must be usable again: charged=%v err=%v", charged, err)
	}
	t.Log("TestLimitReleaseReturnsBudget: PASS")
}

// A record re-driven while its reservation is live passes the gate free:
// a transient stop must not stack a second charge on the day budget, and
// only the drive that charged may hand the reservation back.
func TestLimitRecheckDoesNotStackCharge(t *testing.T) {
	l := &txapp.Limit{Single: 9, Daily: 9}
	at := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	r := txapp.ControlRequest{Action: "payout", IdempotencyKey: "tx-1", Amount: "9", At: at}
	if charged, err := l.Check(r); err != nil || !charged {
		t.Fatalf("first check must charge: charged=%v err=%v", charged, err)
	}
	if charged, err := l.Check(r); err != nil || charged {
		t.Fatalf("the live reservation must pass free: charged=%v err=%v", charged, err)
	}
	// The budget is charged once: a different record of the same size is
	// denied while the first holds it.
	if _, err := l.Check(txapp.ControlRequest{Action: "payout", IdempotencyKey: "tx-2", Amount: "9", At: at}); !errors.Is(err, txapp.ErrControlDenied) {
		t.Fatalf("the day budget must be spent once, got %v", err)
	}
	// Handing the reservation back frees exactly one charge.
	l.Release(r)
	if _, err := l.Check(txapp.ControlRequest{Action: "payout", IdempotencyKey: "tx-2", Amount: "9", At: at}); err != nil {
		t.Fatalf("released budget must be usable again: %v", err)
	}
	// A repeat release releases nothing: the reservation is not live, and
	// the day budget must not go negative.
	l.Release(r)
	if _, err := l.Check(txapp.ControlRequest{Action: "payout", IdempotencyKey: "tx-3", Amount: "9", At: at}); !errors.Is(err, txapp.ErrControlDenied) {
		t.Fatalf("a spent budget must not come back on a repeat release, got %v", err)
	}
	t.Log("TestLimitRecheckDoesNotStackCharge: PASS")
}

func TestLimitDayRolloverResetsBudget(t *testing.T) {
	l := &txapp.Limit{Daily: 10}
	day1 := time.Date(2026, 11, 12, 23, 0, 0, 0, time.UTC)
	day2 := day1.Add(2 * time.Hour)
	if _, err := l.Check(txapp.ControlRequest{Action: "payout", Amount: "10", At: day1}); err != nil {
		t.Fatalf("day one check: %v", err)
	}
	if _, err := l.Check(txapp.ControlRequest{Action: "payout", Amount: "1", At: day1}); !errors.Is(err, txapp.ErrControlDenied) {
		t.Fatalf("day one budget must be exhausted, got %v", err)
	}
	if _, err := l.Check(txapp.ControlRequest{Action: "payout", Amount: "10", At: day2}); err != nil {
		t.Fatalf("a new UTC day must start from a fresh budget: %v", err)
	}
	t.Log("TestLimitDayRolloverResetsBudget: PASS")
}

// The approver's refusal must not consume the day's budget: after a
// refused record, another record of the same size still passes the gate.
func TestControlApprovalRefusalReleasesBudget(t *testing.T) {
	tr := &fakeTransactor{}
	audit := &auditMemory{}
	approved := map[string]bool{}
	f, _ := txapp.NewFlow(memory.New(), tr, txapp.WithInternalControls(
		approvalFunc(func(_ context.Context, r txapp.ControlRequest) (bool, error) {
			return approved[r.IdempotencyKey], nil
		}),
		&txapp.Limit{Single: 9, Daily: 9}, audit, controlRequest))
	refused := controlSeed(t, f, "refused")
	got, err := f.Drive(context.Background(), refused.ID)
	if !errors.Is(err, txapp.ErrControlDenied) || got.Status != txmodel.TxFailed {
		t.Fatalf("the refusal must fail the record: got=%s err=%v", got.Status, err)
	}
	approved["released"] = true
	next := controlSeed(t, f, "released")
	got, err = f.Drive(context.Background(), next.ID)
	if err != nil || got.Status != txmodel.TxSubmitted {
		t.Fatalf("the refused budget must not stay consumed: got=%s err=%v", got.Status, err)
	}
	t.Log("TestControlApprovalRefusalReleasesBudget: PASS")
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
