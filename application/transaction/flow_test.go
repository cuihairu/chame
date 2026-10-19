package transaction_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	txapp "github.com/cuihairu/chame/application/transaction"
	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

// fakeTransactor is the chain-specific half a composition root would
// wire: it records what the flow asked of it and answers from its
// script.
type fakeTransactor struct {
	mu sync.Mutex

	prepares    int
	authorizes  int
	dispatches  int
	settles     []txmodel.SettleOutcome
	sawPayload  []byte
	sawSigners  int
	resumedSign bool

	payload []byte
	signed  []byte
	ref     string

	prepareErr   error
	authorizeErr error
	dispatchErr  error

	// onPrepare runs inside Prepare, so a test can race the flow's
	// conditional writes with a concurrent winner.
	onPrepare func()
}

func (t *fakeTransactor) Prepare(ctx context.Context, tx txmodel.Transaction) (txmodel.Prepared, error) {
	t.mu.Lock()
	t.prepares++
	hook := t.onPrepare
	t.mu.Unlock()
	if hook != nil {
		hook()
	}
	if t.prepareErr != nil {
		return txmodel.Prepared{}, t.prepareErr
	}
	return txmodel.Prepared{Payload: t.payload, ChainData: map[string]any{"stage": "prepared"}}, nil
}

func (t *fakeTransactor) Authorize(ctx context.Context, tx txmodel.Transaction, p txmodel.Prepared, signers ...signer.Signer) (txmodel.Authorized, error) {
	t.mu.Lock()
	t.authorizes++
	t.sawPayload = p.Payload
	t.sawSigners = len(signers)
	t.resumedSign = p.Payload == nil
	t.mu.Unlock()
	if t.authorizeErr != nil {
		return txmodel.Authorized{}, t.authorizeErr
	}
	return txmodel.Authorized{Signed: t.signed, ChainData: map[string]any{"stage": "signed"}}, nil
}

func (t *fakeTransactor) Dispatch(ctx context.Context, tx txmodel.Transaction, a txmodel.Authorized) (string, error) {
	t.mu.Lock()
	t.dispatches++
	t.mu.Unlock()
	if t.dispatchErr != nil {
		return "", t.dispatchErr
	}
	return t.ref, nil
}

func (t *fakeTransactor) Settle(ctx context.Context, tx txmodel.Transaction, outcome txmodel.SettleOutcome) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.settles = append(t.settles, outcome)
}

func (t *fakeTransactor) calls() (prepares, authorizes, dispatches int, settles []txmodel.SettleOutcome, sawPayload []byte, resumedSign bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.prepares, t.authorizes, t.dispatches, append([]txmodel.SettleOutcome(nil), t.settles...), t.sawPayload, t.resumedSign
}

// flowScope is a stable idempotency scope for the suite.
func flowScope() txmodel.IdempotencyScope {
	return txmodel.IdempotencyScope{Application: "game", OperationType: "payout"}
}

// flowSeed builds one created record the application would open the
// operation with.
func flowSeed(id string) txmodel.Transaction {
	return txmodel.Transaction{
		ID:          txmodel.TransactionID(id),
		OperationID: txmodel.OperationID("op-" + id),
		Network:     testNet,
		RequestHash: "req-" + id,
		ChainData:   map[string]any{"kind": "intent"},
	}
}

// newFlow builds a flow over its own store and hands both back, so a
// test can drive the repository the way a second caller would.
func newFlow(t *testing.T, tr txmodel.Transactor) (*txapp.Flow, txmodel.Repository) {
	t.Helper()
	repo := memory.New()
	f, err := txapp.NewFlow(repo, tr)
	if err != nil {
		t.Fatalf("new flow: %v", err)
	}
	return f, repo
}

func TestFlowBeginReplaysTheSameRequest(t *testing.T) {
	f, _ := newFlow(t, &fakeTransactor{})

	first, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// A replay builds a different record identity but the same request.
	replaySeed := flowSeed("tx-2")
	replaySeed.RequestHash = "req-tx-1"
	replay, err := f.Begin(context.Background(), flowScope(), "key-1", replaySeed)
	if err != nil {
		t.Fatalf("replay begin: %v", err)
	}
	if replay.ID != first.ID {
		t.Fatalf("replay must return the stored operation, got %s want %s", replay.ID, first.ID)
	}

	_, err = f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-3"))
	if !errors.Is(err, cherrs.ErrIdempotencyConflict) {
		t.Fatalf("same key with a different request: want ErrIdempotencyConflict, got %v", err)
	}
}

func TestFlowLogsIdempotencyKeyForReplayCorrelation(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	repo := memory.New()
	f, err := txapp.NewFlow(repo, &fakeTransactor{}, txapp.WithFlowLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Begin(context.Background(), flowScope(), "internal-order-17", flowSeed("tx-log-1"))
	if err != nil {
		t.Fatal(err)
	}
	replay := flowSeed("tx-log-2")
	replay.RequestHash = "req-tx-log-1"
	_, err = f.Begin(context.Background(), flowScope(), "internal-order-17", replay)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "idempotency_key=internal-order-17") != 3 || !strings.Contains(output.String(), "transaction operation replayed") {
		t.Fatalf("same-key correlation missing from logs: %s", output.String())
	}
}

func TestFlowBeginRequiresARequestHash(t *testing.T) {
	f, _ := newFlow(t, &fakeTransactor{})
	seed := flowSeed("tx-1")
	seed.RequestHash = ""

	if _, err := f.Begin(context.Background(), flowScope(), "key-1", seed); err == nil {
		t.Fatal("begin with a key and no request hash: err = nil, want error")
	}
}

// TestFlowDrivesToSubmitted walks the happy path: every phase lands in
// the store before the next begins, the reference lands with the status
// it belongs to, and the claim is reported spent.
func TestFlowDrivesToSubmitted(t *testing.T) {
	tr := &fakeTransactor{payload: []byte{1}, signed: []byte{2}, ref: "0xref"}
	f, _ := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxSubmitted || out.ChainHash != "0xref" {
		t.Fatalf("want submitted with its reference, got %+v", out)
	}
	if stage, _ := out.ChainData.(map[string]any); stage["stage"] != "signed" {
		t.Fatalf("chain data must be the sign step's, got %v", out.ChainData)
	}
	prepares, authorizes, dispatches, settles, _, _ := tr.calls()
	if prepares != 1 || authorizes != 1 || dispatches != 1 {
		t.Fatalf("steps: prepare=%d authorize=%d dispatch=%d, want 1/1/1", prepares, authorizes, dispatches)
	}
	if len(settles) != 1 || settles[0] != txmodel.SettleBroadcast {
		t.Fatalf("settles = %v, want one broadcast", settles)
	}
}

// TestFlowResumesAPreparedRecord drives a record that reached prepared in
// an earlier run: the sign step works from the record's chain data.
func TestFlowResumesAPreparedRecord(t *testing.T) {
	tr := &fakeTransactor{payload: []byte{1}, signed: []byte{2}, ref: "0xref"}
	f, repo := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxCreated, txmodel.TxPrepared,
		"", map[string]any{"stage": "prepared"}, tx.CreatedAt); err != nil {
		t.Fatalf("stage prepared: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxSubmitted || out.ChainHash != "0xref" {
		t.Fatalf("want submitted with its reference, got %+v", out)
	}
	_, _, _, _, sawPayload, resumedSign := tr.calls()
	if !resumedSign {
		t.Error("a resumed record must reach the sign step without a payload carry-over")
	}
	if len(sawPayload) != 0 {
		t.Errorf("resumed sign saw payload %v, want none", sawPayload)
	}
}

// TestFlowResumesASignedRecord drives a record that reached signed in an
// earlier run: only the dispatch step runs.
func TestFlowResumesASignedRecord(t *testing.T) {
	tr := &fakeTransactor{ref: "0xref"}
	f, repo := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxCreated, txmodel.TxPrepared, "", nil, tx.CreatedAt); err != nil {
		t.Fatalf("stage prepared: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxPrepared, txmodel.TxSigned, "", nil, tx.CreatedAt); err != nil {
		t.Fatalf("stage signed: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxSubmitted || out.ChainHash != "0xref" {
		t.Fatalf("want submitted with its reference, got %+v", out)
	}
	prepares, authorizes, dispatches, _, _, _ := tr.calls()
	if prepares != 0 || authorizes != 0 || dispatches != 1 {
		t.Fatalf("steps: prepare=%d authorize=%d dispatch=%d, want 0/0/1", prepares, authorizes, dispatches)
	}
}

// TestFlowRecordsUnknownWhenDispatchIsUndecided pins §14.3: a submission
// whose outcome is open becomes unknown, the claim is spent, and the
// recovery worker takes over.
func TestFlowRecordsUnknownWhenDispatchIsUndecided(t *testing.T) {
	tr := &fakeTransactor{
		signed:      []byte{2},
		ref:         "0xref",
		dispatchErr: cherrs.New(cherrs.RequestTimeout, "test.dispatch", "the node stopped answering"),
	}
	f, _ := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxUnknown {
		t.Fatalf("status = %s, want unknown", out.Status)
	}
	_, _, _, settles, _, _ := tr.calls()
	if len(settles) != 1 || settles[0] != txmodel.SettleBroadcast {
		t.Fatalf("settles = %v, want one broadcast (the claim is spent)", settles)
	}
}

// TestFlowFailsTheRecordOnADecisiveDispatch pins the other side: a node
// that refused the bytes has answered, so the record ends and the claim
// is released.
func TestFlowFailsTheRecordOnADecisiveDispatch(t *testing.T) {
	tr := &fakeTransactor{
		signed:      []byte{2},
		dispatchErr: cherrs.New(cherrs.TransactionRejected, "test.dispatch", "the node refused the payload"),
	}
	f, _ := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxFailed {
		t.Fatalf("status = %s, want failed", out.Status)
	}
	_, _, _, settles, _, _ := tr.calls()
	if len(settles) != 1 || settles[0] != txmodel.SettleFailed {
		t.Fatalf("settles = %v, want one failed (the claim is released)", settles)
	}
}

// TestFlowFailsTheRecordOnADecisivePrepare ends the record on a build
// the chain will never accept, instead of leaving it to spin.
func TestFlowFailsTheRecordOnADecisivePrepare(t *testing.T) {
	tr := &fakeTransactor{
		prepareErr: cherrs.New(cherrs.InvalidArgument, "test.prepare", "the intent is not buildable"),
	}
	f, _ := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxFailed {
		t.Fatalf("status = %s, want failed", out.Status)
	}
	if out.ChainData == nil {
		t.Error("a failed record keeps its chain data as evidence")
	}
	_, _, _, settles, _, _ := tr.calls()
	if len(settles) != 1 || settles[0] != txmodel.SettleFailed {
		t.Fatalf("settles = %v, want one failed", settles)
	}
}

// TestFlowLeavesARecordOpenOnTransientFailure pins the retry path: a
// chain that cannot answer a build is not a verdict, so the record stays
// where it is and the next Drive takes it from there.
func TestFlowLeavesARecordOpenOnTransientFailure(t *testing.T) {
	tr := &fakeTransactor{
		prepareErr: cherrs.New(cherrs.NetworkUnavailable, "test.prepare", "no endpoint answered"),
		payload:    []byte{1},
		signed:     []byte{2},
		ref:        "0xref",
	}
	f, repo := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	if _, err := f.Drive(context.Background(), tx.ID); err == nil {
		t.Fatal("drive over an unreachable chain: err = nil, want error")
	}
	stored, err := repo.Get(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.Status != txmodel.TxCreated {
		t.Fatalf("status = %s, want created (nothing has been decided)", stored.Status)
	}

	tr.mu.Lock()
	tr.prepareErr = nil
	tr.mu.Unlock()
	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive after the chain answers: %v", err)
	}
	if out.Status != txmodel.TxSubmitted {
		t.Fatalf("status = %s, want submitted", out.Status)
	}
}

// TestFlowStopsOnAConflictItDidNotWin races the flow's advance with a
// concurrent winner: the winner's decision stands, the loser releases
// the claim it made, and the record ends consistent.
func TestFlowStopsOnAConflictItDidNotWin(t *testing.T) {
	tr := &fakeTransactor{ref: "0xref"}
	f, repo := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	// A concurrent winner completes the record while this flow is
	// preparing.
	tr.onPrepare = func() {
		_ = repo.Attach(context.Background(), tx.ID, txmodel.TxCreated, txmodel.TxPrepared, "", nil, tx.CreatedAt)
		_ = repo.Attach(context.Background(), tx.ID, txmodel.TxPrepared, txmodel.TxSigned, "", nil, tx.CreatedAt)
		_ = repo.Attach(context.Background(), tx.ID, txmodel.TxSigned, txmodel.TxSubmitted, "0xwinner", nil, tx.CreatedAt)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxSubmitted || out.ChainHash != "0xwinner" {
		t.Fatalf("want the winner's record, got %+v", out)
	}
	_, _, _, settles, _, _ := tr.calls()
	if len(settles) != 1 || settles[0] != txmodel.SettleFailed {
		t.Fatalf("settles = %v, want one failed (our claim was never broadcast)", settles)
	}
}

// TestFlowLeavesSubmittedRecordsAlone hands the flow a record the
// recovery worker already owns.
func TestFlowLeavesSubmittedRecordsAlone(t *testing.T) {
	tr := &fakeTransactor{}
	f, repo := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxCreated, txmodel.TxPrepared, "", nil, tx.CreatedAt); err != nil {
		t.Fatalf("stage prepared: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxPrepared, txmodel.TxSigned, "", nil, tx.CreatedAt); err != nil {
		t.Fatalf("stage signed: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxSigned, txmodel.TxSubmitted, "0xref", nil, tx.CreatedAt); err != nil {
		t.Fatalf("stage submitted: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if out.Status != txmodel.TxSubmitted {
		t.Fatalf("status = %s, want submitted untouched", out.Status)
	}
	prepares, authorizes, dispatches, _, _, _ := tr.calls()
	if prepares != 0 || authorizes != 0 || dispatches != 0 {
		t.Fatalf("the flow must not touch a submitted record, steps: %d/%d/%d", prepares, authorizes, dispatches)
	}
}

// TestFlowNeedsRepositoryAndTransactor refuses to build without both.
func TestFlowNeedsRepositoryAndTransactor(t *testing.T) {
	if _, err := txapp.NewFlow(nil, &fakeTransactor{}); err == nil {
		t.Fatal("NewFlow(nil, transactor): err = nil, want error")
	}
	repo := memory.New()
	if _, err := txapp.NewFlow(repo, nil); err == nil {
		t.Fatal("NewFlow(repo, nil): err = nil, want error")
	}
}

// TestFlowKeepsChainDataAcrossAResumedDispatch pins the resume rule: a
// signed record the flow did not prepare in this run keeps its chain
// data after submission — a nil from the dispatch step is "no carry
// over", not "erase the evidence".
func TestFlowKeepsChainDataAcrossAResumedDispatch(t *testing.T) {
	tr := &fakeTransactor{ref: "0xref"}
	f, repo := newFlow(t, tr)
	tx, err := f.Begin(context.Background(), flowScope(), "key-1", flowSeed("tx-1"))
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxCreated, txmodel.TxPrepared, "", nil, tx.CreatedAt); err != nil {
		t.Fatalf("stage prepared: %v", err)
	}
	if err := repo.Attach(context.Background(), tx.ID, txmodel.TxPrepared, txmodel.TxSigned,
		"", map[string]any{"stage": "signed"}, tx.CreatedAt); err != nil {
		t.Fatalf("stage signed: %v", err)
	}

	out, err := f.Drive(context.Background(), tx.ID)
	if err != nil {
		t.Fatalf("drive: %v", err)
	}
	if stage, _ := out.ChainData.(map[string]any); stage["stage"] != "signed" {
		t.Fatalf("chain data must survive the resumed dispatch, got %v", out.ChainData)
	}
}
