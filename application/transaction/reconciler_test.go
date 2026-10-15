package transaction_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	txapp "github.com/cuihairu/chame/application/transaction"
	"github.com/cuihairu/chame/capability/transaction"
)

// fakeStatusQuery answers one observation per record, the way an adapter
// would.
type fakeStatusQuery struct {
	mu    sync.Mutex
	out   map[transaction.TransactionID]transaction.TransactionStatus
	err   map[transaction.TransactionID]error
	calls []transaction.Transaction
}

func newFakeStatusQuery() *fakeStatusQuery {
	return &fakeStatusQuery{
		out: map[transaction.TransactionID]transaction.TransactionStatus{},
		err: map[transaction.TransactionID]error{},
	}
}

func (f *fakeStatusQuery) StatusOf(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error) {
	f.mu.Lock()
	f.calls = append(f.calls, tx)
	out, err := f.out[tx.ID], f.err[tx.ID]
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	if out == "" {
		return tx.Status, nil
	}
	return out, nil
}

func (f *fakeStatusQuery) callCount(id transaction.TransactionID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, tx := range f.calls {
		if tx.ID == id {
			n++
		}
	}
	return n
}

func (f *fakeStatusQuery) fail(id transaction.TransactionID, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err[id] = err
}

func (f *fakeStatusQuery) observe(id transaction.TransactionID, status transaction.TransactionStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.out[id] = status
}

// submitted seeds one submitted record carrying a chain hash.
func submittedRecord(id transaction.TransactionID) transaction.Transaction {
	return transaction.Transaction{
		ID:          id,
		OperationID: transaction.OperationID("op-" + id),
		Network:     testNet,
		Status:      transaction.TxSubmitted,
		ChainHash:   "0xabc",
	}
}

func TestStatusReconcilerAppliesTheObservation(t *testing.T) {
	q := newFakeStatusQuery()
	r, err := txapp.NewStatusReconciler(q)
	if err != nil {
		t.Fatalf("new reconciler: %v", err)
	}
	tx := submittedRecord("tx-1")
	q.observe(tx.ID, transaction.TxIncluded)

	next, err := r.Reconcile(context.Background(), tx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if next != transaction.TxIncluded {
		t.Errorf("next = %s, want included", next)
	}
}

// TestStatusReconcilerWeakensToTheLegalStep walks a too-far observation
// down to the strongest status the matrix accepts from the record's own:
// finalized implies confirmed implies included, and each step is true
// when the stronger one is.
func TestStatusReconcilerWeakensToTheLegalStep(t *testing.T) {
	q := newFakeStatusQuery()
	r, _ := txapp.NewStatusReconciler(q)

	cases := []struct {
		from     transaction.TransactionStatus
		observed transaction.TransactionStatus
		want     transaction.TransactionStatus
	}{
		{transaction.TxSubmitted, transaction.TxFinalized, transaction.TxIncluded},
		{transaction.TxIncluded, transaction.TxFinalized, transaction.TxConfirmed},
		{transaction.TxConfirmed, transaction.TxFinalized, transaction.TxFinalized},
		{transaction.TxUnknown, transaction.TxFinalized, transaction.TxFinalized},
	}
	for _, tc := range cases {
		tx := submittedRecord(transaction.TransactionID("tx-" + string(tc.from)))
		tx.Status = tc.from
		q.observe(tx.ID, tc.observed)
		next, err := r.Reconcile(context.Background(), tx)
		if err != nil {
			t.Fatalf("%s + %s: Reconcile: %v", tc.from, tc.observed, err)
		}
		if next != tc.want {
			t.Errorf("%s observing %s: next = %s, want %s", tc.from, tc.observed, next, tc.want)
		}
	}
}

// TestStatusReconcilerReopensThroughUnknown sends an observation the
// matrix will not take from the record's status through the unknown
// escape hatch when it is legal, so the next scan can apply it.
func TestStatusReconcilerReopensThroughUnknown(t *testing.T) {
	q := newFakeStatusQuery()
	r, _ := txapp.NewStatusReconciler(q)
	tx := submittedRecord("tx-reorged")
	tx.Status = transaction.TxReorged
	q.observe(tx.ID, transaction.TxIncluded)

	next, err := r.Reconcile(context.Background(), tx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if next != transaction.TxUnknown {
		t.Errorf("next = %s, want unknown (the reopen step)", next)
	}
}

// TestStatusReconcilerNoHashIsANoOp leaves records without a chain
// reference alone: nothing to ask the chain about, and asking is a
// bug-shaped error loop.
func TestStatusReconcilerNoHashIsANoOp(t *testing.T) {
	q := newFakeStatusQuery()
	r, _ := txapp.NewStatusReconciler(q)
	tx := submittedRecord("tx-nohash")
	tx.ChainHash = ""

	next, err := r.Reconcile(context.Background(), tx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if next != transaction.TxSubmitted {
		t.Errorf("next = %s, want the record's own status", next)
	}
	if q.callCount(tx.ID) != 0 {
		t.Error("a record without a chain reference must not query the chain")
	}
}

// TestStatusReconcilerPropagatesQueryFailure reports a chain failure
// instead of a status: a failed query is not evidence of anything.
func TestStatusReconcilerPropagatesQueryFailure(t *testing.T) {
	q := newFakeStatusQuery()
	r, _ := txapp.NewStatusReconciler(q)
	tx := submittedRecord("tx-err")
	q.fail(tx.ID, errors.New("rpc down"))

	if _, err := r.Reconcile(context.Background(), tx); err == nil {
		t.Fatal("Reconcile over a failing query: err = nil, want error")
	}
}

// TestStatusReconcilerSameStatusIsANoOp answers with the record's own
// status when the chain confirms it: the worker treats that as nothing
// to apply.
func TestStatusReconcilerSameStatusIsANoOp(t *testing.T) {
	q := newFakeStatusQuery()
	r, _ := txapp.NewStatusReconciler(q)
	tx := submittedRecord("tx-same")
	q.observe(tx.ID, transaction.TxSubmitted)

	next, err := r.Reconcile(context.Background(), tx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if next != transaction.TxSubmitted {
		t.Errorf("next = %s, want the record's own status", next)
	}
}

// TestStatusReconcilerLeavesTheImpossibleAlone refuses to write an
// observation the matrix rejects and nothing weaker helps: rewriting
// history is not recovery.
func TestStatusReconcilerLeavesTheImpossibleAlone(t *testing.T) {
	q := newFakeStatusQuery()
	r, _ := txapp.NewStatusReconciler(q)
	tx := submittedRecord("tx-created")
	tx.Status = transaction.TxCreated
	q.observe(tx.ID, transaction.TxReverted)

	next, err := r.Reconcile(context.Background(), tx)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if next != transaction.TxCreated {
		t.Errorf("next = %s, want the record's own status", next)
	}
}

// TestStatusReconcilerNeedsAQuery refuses to build without a query.
func TestStatusReconcilerNeedsAQuery(t *testing.T) {
	if _, err := txapp.NewStatusReconciler(nil); err == nil {
		t.Fatal("NewStatusReconciler(nil): err = nil, want error")
	}
}
