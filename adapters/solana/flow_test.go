package solana

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/gagliardetto/solana-go"

	txmodel "github.com/cuihairu/chame/capability/transaction"
)

// The Solana network implements the flow's chain-specific half (§8.3) —
// the compile-time check that keeps the seam honest.
var _ txmodel.Transactor = (*Network)(nil)

// flowRecord carries an intent the way the flow's Begin would.
func flowRecord(id string, intent *BuildQuery) txmodel.Transaction {
	return txmodel.Transaction{
		ID:        txmodel.TransactionID(id),
		Status:    txmodel.TxCreated,
		ChainData: &ChainRecord{Intent: intent},
	}
}

// flowNetwork builds a network over a fake whose blockhash window
// satisfies a full build.
func flowNetwork() (*fakeClient, *Network) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockhash(solanaHashFor(0x20), 900)
	return f, newTestNetwork(f)
}

// preparedRecord prepares a record and hands back the record as the flow
// would mirror it after the prepare phase committed.
func preparedRecord(t *testing.T, n *Network) (txmodel.Transaction, txmodel.Prepared) {
	t.Helper()
	p, err := n.Prepare(context.Background(), flowRecord("tx-1", intentOf(t, n)))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	tx := flowRecord("tx-1", intentOf(t, n))
	tx.ChainData = p.ChainData
	return tx, p
}

func intentOf(t *testing.T, n *Network) *BuildQuery {
	t.Helper()
	q := buildQuery(t, devAddress(t))
	return &q
}

// TestFlowPreparePinsTheWindow builds from the stored intent and keeps
// the window, the signer order, and the message on the record.
func TestFlowPreparePinsTheWindow(t *testing.T) {
	_, n := flowNetwork()
	p, err := n.Prepare(context.Background(), flowRecord("tx-1", intentOf(t, n)))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(p.Payload) == 0 {
		t.Fatal("prepare must hand over an unsigned message")
	}
	rec, ok := p.ChainData.(*ChainRecord)
	if !ok {
		t.Fatalf("chain data = %T, want the adapter's record", p.ChainData)
	}
	if rec.Pin.Blockhash != solanaHashFor(0x20).String() || rec.Pin.LastValidBlockHeight != 900 {
		t.Errorf("pin = %+v, want the node's pinned window", rec.Pin)
	}
	if len(rec.Signers) == 0 || rec.Signers[0].String() != devAddress(t).String() {
		t.Errorf("signers = %v, want the fee payer first", rec.Signers)
	}
	if string(rec.Unsigned) != string(p.Payload) {
		t.Error("the unsigned message must be kept on the record")
	}
	if rec.Commitment != CommitmentConfirmed {
		t.Errorf("commitment = %s, want the intent's", rec.Commitment)
	}
}

func TestFlowPrepareRefusesARecordWithoutAnIntent(t *testing.T) {
	_, n := flowNetwork()
	if _, err := n.Prepare(context.Background(), flowRecord("tx-1", nil)); err == nil {
		t.Fatal("prepare without an intent: err = nil, want error")
	}
}

// TestFlowAuthorizeSignsAndKeepsTheTransaction signs the message in
// build order and keeps the assembled transaction on the record.
func TestFlowAuthorizeSignsAndKeepsTheTransaction(t *testing.T) {
	_, n := flowNetwork()
	s := devSignerFor(t, n)
	tx, p := preparedRecord(t, n)

	a, err := n.Authorize(context.Background(), tx, p, s)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if len(a.Signed) == 0 {
		t.Fatal("authorize must hand over a signed transaction")
	}
	rec := a.ChainData.(*ChainRecord)
	if string(rec.Signed) != string(a.Signed) {
		t.Error("the signed transaction must be kept on the record")
	}
	// The signed bytes are a real transaction whose signatures verify.
	var stx solana.Transaction
	if err := stx.UnmarshalBase64(base64.StdEncoding.EncodeToString(a.Signed)); err != nil {
		t.Fatalf("signed bytes are not a transaction: %v", err)
	}
	if err := stx.VerifySignatures(); err != nil {
		t.Errorf("signatures must verify over the stored message: %v", err)
	}
}

func TestFlowAuthorizeRefusesAMissingSigner(t *testing.T) {
	_, n := flowNetwork()
	tx, p := preparedRecord(t, n)
	if _, err := n.Authorize(context.Background(), tx, p); err == nil {
		t.Fatal("authorize with no signers: err = nil, want error")
	}
}

// TestFlowAuthorizeResumesFromTheRecord signs a message recovered from
// the record's chain data, the way a resumed flow arrives.
func TestFlowAuthorizeResumesFromTheRecord(t *testing.T) {
	_, n := flowNetwork()
	s := devSignerFor(t, n)
	tx, _ := preparedRecord(t, n)

	a, err := n.Authorize(context.Background(), tx, txmodel.Prepared{}, s)
	if err != nil {
		t.Fatalf("authorize on resume: %v", err)
	}
	if len(a.Signed) == 0 {
		t.Fatal("a resumed record must still sign")
	}
}

// TestFlowDispatchBroadcastsTheStoredTransaction drives a record that
// reached signed in an earlier run: the dispatch works from the record.
func TestFlowDispatchBroadcastsTheStoredTransaction(t *testing.T) {
	f, n := flowNetwork()
	f.withSentSignature(sigFor(0x33))
	s := devSignerFor(t, n)
	tx, p := preparedRecord(t, n)
	a, err := n.Authorize(context.Background(), tx, p, s)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	tx.ChainData = a.ChainData

	ref, err := n.Dispatch(context.Background(), tx, txmodel.Authorized{})
	if err != nil {
		t.Fatalf("dispatch on resume: %v", err)
	}
	if ref == "" {
		t.Fatal("dispatch must report the chain reference")
	}
	if len(f.capturedSentTransaction()) == 0 {
		t.Error("the stored transaction must be broadcast")
	}
}

// TestFlowSettleIsANoOp settles both outcomes without effect: Solana
// claims nothing across steps, so there is nothing to spend or release.
func TestFlowSettleIsANoOp(t *testing.T) {
	_, n := flowNetwork()
	tx, _ := preparedRecord(t, n)
	n.Settle(context.Background(), tx, txmodel.SettleBroadcast)
	n.Settle(context.Background(), tx, txmodel.SettleFailed)
}

func TestRecordOfRefusesForeignChainData(t *testing.T) {
	_, n := flowNetwork()
	tx := flowRecord("tx-1", nil)
	tx.ChainData = "not ours"
	if _, err := n.recordOf(tx); err == nil {
		t.Fatal("recordOf over foreign chain data: err = nil, want error")
	}
}
