package evm

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
)

// The EVM network implements the flow's chain-specific half (§9) — the
// compile-time check that keeps the seam honest.
var _ txmodel.Transactor = (*Network)(nil)

// flowNetwork builds a network against a fake whose answers satisfy a
// full build: a fee suggestion and a pending nonce.
func flowNetwork(t *testing.T) (*Network, *fakeClient) {
	t.Helper()
	fake := newFakeClient(1)
	fake.withGasPrice(big.NewInt(1_000_000_000))
	fake.withPendingNonce(common.HexToAddress(devKeyAddr), 7)
	n := testNetwork(t, fake)
	return n, fake
}

// flowIntent is the intent the application stores with a record.
func flowIntent(t *testing.T) *BuildIntent {
	t.Helper()
	i, err := IntentFromQuery(BuildQuery{
		Sender:   mustEVMAddr(t, devKeyAddr),
		To:       mustEVMAddr(t, "0x1111111111111111111111111111111111111111"),
		Value:    big.NewInt(123),
		GasLimit: 21_000,
	})
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	return i
}

// flowRecord carries an intent the way the flow's Begin would.
func flowRecord(id string, intent *BuildIntent) txmodel.Transaction {
	return txmodel.Transaction{
		ID:        txmodel.TransactionID(id),
		Network:   recNetwork,
		Status:    txmodel.TxCreated,
		ChainData: &ChainRecord{Intent: intent},
	}
}

func mustEVMAddr(t *testing.T, raw string) identifiers.Address {
	t.Helper()
	return evmAddr(t, raw)
}

// devSignerForOtherAccount signs for a different key, whose address does
// not cover the intent's sender.
func devSignerForOtherAccount(t *testing.T) *LocalDevSigner {
	t.Helper()
	key, err := DevKeyFromHex("59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	if err != nil {
		t.Fatalf("other key: %v", err)
	}
	addr, err := DevKeyAddress(key)
	if err != nil {
		t.Fatalf("other address: %v", err)
	}
	ref, err := signer.NewAccountRef(recNetwork, addr)
	if err != nil {
		t.Fatalf("other ref: %v", err)
	}
	s, err := NewLocalDevSigner("development", key, ref)
	if err != nil {
		t.Fatalf("other signer: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// preparedRecord prepares a record and hands back the record as the flow
// would mirror it after the prepare phase committed.
func preparedRecord(t *testing.T, n *Network, intent *BuildIntent) (txmodel.Transaction, txmodel.Prepared) {
	t.Helper()
	p, err := n.Prepare(context.Background(), flowRecord("tx-1", intent))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	tx := flowRecord("tx-1", intent)
	tx.ChainData = p.ChainData
	return tx, p
}

// TestFlowPrepareBuildsFromTheStoredIntent reads the intent, builds, and
// keeps the nonce and the encoding on the record for the later phases.
func TestFlowPrepareBuildsFromTheStoredIntent(t *testing.T) {
	n, _ := flowNetwork(t)
	p, err := n.Prepare(context.Background(), flowRecord("tx-1", flowIntent(t)))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(p.Payload) == 0 {
		t.Fatal("prepare must hand over an unsigned payload")
	}
	rec, ok := p.ChainData.(*ChainRecord)
	if !ok {
		t.Fatalf("chain data = %T, want the adapter's record", p.ChainData)
	}
	if rec.Nonce == nil || *rec.Nonce != 7 {
		t.Errorf("nonce = %v, want the claimed 7", rec.Nonce)
	}
	if rec.Format == "" {
		t.Error("the encoding format must be recorded for the signer request")
	}
	if string(rec.Unsigned) != string(p.Payload) {
		t.Error("the unsigned payload must be kept on the record")
	}
}

func TestFlowPrepareRefusesARecordWithoutAnIntent(t *testing.T) {
	n, _ := flowNetwork(t)
	if _, err := n.Prepare(context.Background(), flowRecord("tx-1", nil)); err == nil {
		t.Fatal("prepare without an intent: err = nil, want error")
	}
}

// TestFlowAuthorizeSignsAndKeepsTheEncoding signs with the covering
// signer and records the broadcastable bytes.
func TestFlowAuthorizeSignsAndKeepsTheEncoding(t *testing.T) {
	n, _ := flowNetwork(t)
	s := devSigner(t, "development", devAccountRef(t))
	p, err := n.Prepare(context.Background(), flowRecord("tx-1", flowIntent(t)))
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	tx, _ := preparedRecord(t, n, flowIntent(t))
	a, err := n.Authorize(context.Background(), tx, p, s)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if len(a.Signed) == 0 {
		t.Fatal("authorize must hand over broadcastable bytes")
	}
	rec := a.ChainData.(*ChainRecord)
	if string(rec.Signed) != string(a.Signed) {
		t.Error("the signed encoding must be kept on the record")
	}
	if string(rec.Unsigned) != string(p.Payload) {
		t.Error("the unsigned payload must stay for audit")
	}
}

func TestFlowAuthorizeRefusesAnUncoveredSender(t *testing.T) {
	n, _ := flowNetwork(t)
	other := devSignerForOtherAccount(t)
	tx, p := preparedRecord(t, n, flowIntent(t))
	if _, err := n.Authorize(context.Background(), tx, p, other); err == nil {
		t.Fatal("authorize with an uncovered sender: err = nil, want error")
	}
}

// TestFlowAuthorizeResumesFromTheRecord signs a payload recovered from
// the record's chain data, the way a resumed flow arrives.
func TestFlowAuthorizeResumesFromTheRecord(t *testing.T) {
	n, _ := flowNetwork(t)
	s := devSigner(t, "development", devAccountRef(t))
	tx, _ := preparedRecord(t, n, flowIntent(t))

	a, err := n.Authorize(context.Background(), tx, txmodel.Prepared{}, s)
	if err != nil {
		t.Fatalf("authorize on resume: %v", err)
	}
	if len(a.Signed) == 0 {
		t.Fatal("a resumed record must still sign")
	}
}

// TestFlowDispatchBroadcastsTheStoredEncoding drives a record that
// reached signed in an earlier run: the dispatch works from the record.
func TestFlowDispatchBroadcastsTheStoredEncoding(t *testing.T) {
	n, fake := flowNetwork(t)
	s := devSigner(t, "development", devAccountRef(t))
	tx, p := preparedRecord(t, n, flowIntent(t))
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
	if len(fake.capturedSent()) == 0 {
		t.Error("the stored encoding must be broadcast")
	}
}

// TestFlowSettleSpendsAndReleasesTheNonce pins the §7.5 wiring: a
// broadcast outcome spends the nonce forever, a failure releases it, and
// neither errors out over bookkeeping.
func TestFlowSettleSpendsAndReleasesTheNonce(t *testing.T) {
	n, _ := flowNetwork(t)
	tx, p := preparedRecord(t, n, flowIntent(t))
	rec := p.ChainData.(*ChainRecord)
	if rec.Nonce == nil || *rec.Nonce != 7 {
		t.Fatalf("nonce = %v, want 7", rec.Nonce)
	}

	n.Settle(context.Background(), tx, txmodel.SettleFailed)
	// The claim is released: the next build takes the same nonce again,
	// so a failed build leaves no hole.
	tx2, p2 := preparedRecord(t, n, flowIntent(t))
	if rec2 := p2.ChainData.(*ChainRecord); rec2.Nonce == nil || *rec2.Nonce != 7 {
		t.Fatalf("nonce after abandon = %v, want the released 7", rec2.Nonce)
	}
	n.Settle(context.Background(), tx2, txmodel.SettleBroadcast)
	// The claim is spent: it never comes back.
	next, err := n.NonceCoordinator().Next(context.Background(), mustEVMAddr(t, devKeyAddr))
	if err != nil {
		t.Fatalf("next after submitted: %v", err)
	}
	if next != 8 {
		t.Fatalf("next = %d, want 8 (the spent nonce is not reused)", next)
	}
}

// TestBuildIntentRoundTrips pins the storable intent: decimal strings
// come back as the same wei amounts, and an unparsable string is
// refused.
func TestBuildIntentRoundTrips(t *testing.T) {
	q := BuildQuery{
		Sender:               mustEVMAddr(t, devKeyAddr),
		To:                   mustEVMAddr(t, "0x1111111111111111111111111111111111111111"),
		Value:                big.NewInt(0),
		GasPrice:             big.NewInt(1_000_000_000),
		MaxFeePerGas:         big.NewInt(2_000_000_000),
		MaxPriorityFeePerGas: big.NewInt(1),
		GasLimit:             21_000,
	}
	i, err := IntentFromQuery(q)
	if err != nil {
		t.Fatalf("intent: %v", err)
	}
	if i.Value != "0" || i.GasPrice != "1000000000" {
		t.Fatalf("amounts must travel as decimal strings, got %+v", i)
	}
	back, err := i.query()
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if back.Value.Cmp(big.NewInt(0)) != 0 || back.GasPrice.Cmp(big.NewInt(1_000_000_000)) != 0 ||
		back.MaxFeePerGas.Cmp(big.NewInt(2_000_000_000)) != 0 || back.MaxPriorityFeePerGas.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("round trip changed the amounts: %+v", back)
	}

	i.Value = "not a number"
	if _, err := i.query(); err == nil {
		t.Fatal("an unparsable amount: err = nil, want error")
	}
}

func TestRecordOfRefusesForeignChainData(t *testing.T) {
	n, _ := flowNetwork(t)
	tx := flowRecord("tx-1", nil)
	tx.ChainData = "not ours"
	if _, err := n.recordOf(tx); err == nil {
		t.Fatal("recordOf over foreign chain data: err = nil, want error")
	}
}
