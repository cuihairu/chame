package evm

import (
	"context"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// txHashFixture is a 32-byte transaction hash in hex.
const txHashFixture = "0x1111111111111111111111111111111111111111111111111111111111111111"

// signedTxFixture builds one signed transaction, the payload Submit
// consumes and the receipt lookups key on.
func signedTxFixture(t *testing.T) []byte {
	t.Helper()
	receiver := common.HexToAddress(txReceiver)
	unsigned := types.NewTx(&types.LegacyTx{
		Nonce:    5,
		To:       &receiver,
		Value:    big.NewInt(1000),
		Gas:      21000,
		GasPrice: big.NewInt(20_000_000_000),
	})
	key, err := DevKeyFromHex(devKeyHex)
	if err != nil {
		t.Fatalf("dev key: %v", err)
	}
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(big.NewInt(1)), key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestNetworkSubmitBroadcastsSignedTx(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	payload := signedTxFixture(t)

	hash, err := n.Submit(context.Background(), payload)
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if hash == "" || len(hash) != 66 {
		t.Errorf("hash = %q, want a 32-byte hex hash", hash)
	}
	// The broadcast transaction is the one the caller signed: nonce and
	// receiver must survive the round trip.
	sent := fake.capturedSent()
	if len(sent) != 1 {
		t.Fatalf("sent transactions = %d, want 1", len(sent))
	}
	if sent[0].Nonce() != 5 {
		t.Errorf("sent nonce = %d, want 5", sent[0].Nonce())
	}
	if fake.callCount("send_transaction") != 1 {
		t.Errorf("chain queries = %d, want 1", fake.callCount("send_transaction"))
	}
}

func TestNetworkSubmitRejectsMalformedPayload(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	if _, err := n.Submit(context.Background(), []byte("not a transaction")); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("err = %v, want invalid argument", err)
	}
	if fake.callCount("send_transaction") != 0 {
		t.Error("a malformed payload must not reach the endpoint")
	}
}

func TestNetworkSubmitTimeoutStaysOpen(t *testing.T) {
	fake := newFakeClient(1)
	// A hung endpoint must classify as a request timeout, and the
	// transaction may still have been accepted: the caller reconciles.
	fake.fail("send_transaction", netErr{msg: "read: i/o timeout", timeout: true})
	n := testNetwork(t, fake)
	if _, err := n.Submit(context.Background(), signedTxFixture(t)); err == nil {
		t.Fatal("Submit over a dead endpoint: err = nil, want error")
	} else if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.RequestTimeout {
		t.Errorf("err = %v, want request timeout (the outcome stays open)", err)
	}
}

func TestNetworkReceiptIncluded(t *testing.T) {
	fake := newFakeClient(1)
	fake.withReceipt(txHashFixture, &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(42),
		BlockHash:   common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
		GasUsed:     21000,
	})
	n := testNetwork(t, fake)

	status, summary, err := n.Receipt(context.Background(), txHashFixture)
	if err != nil {
		t.Fatalf("Receipt: %v", err)
	}
	if status != transaction.TxIncluded {
		t.Errorf("status = %s, want included", status)
	}
	if summary.BlockNumber != 42 || summary.GasUsed != 21000 || summary.Status != transaction.TxIncluded {
		t.Errorf("summary = %+v", summary)
	}
	if summary.BlockHash != "0x2222222222222222222222222222222222222222222222222222222222222222" {
		t.Errorf("block hash = %q", summary.BlockHash)
	}
}

func TestNetworkReceiptReverted(t *testing.T) {
	fake := newFakeClient(1)
	fake.withReceipt(txHashFixture, &types.Receipt{
		Status:      types.ReceiptStatusFailed,
		BlockNumber: big.NewInt(42),
		BlockHash:   common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222"),
		GasUsed:     18000,
	})
	n := testNetwork(t, fake)

	status, summary, err := n.Receipt(context.Background(), txHashFixture)
	if err != nil {
		t.Fatalf("Receipt: %v", err)
	}
	if status != transaction.TxReverted {
		t.Errorf("status = %s, want reverted", status)
	}
	if summary.Status != transaction.TxReverted || summary.GasUsed != 18000 {
		t.Errorf("summary = %+v", summary)
	}
}

func TestNetworkReceiptPending(t *testing.T) {
	fake := newFakeClient(1)
	// No receipt yet, but the node still holds the transaction in its
	// mempool: pending, not unknown.
	fake.withPooled(txHashFixture, types.NewTx(&types.LegacyTx{Nonce: 5}))
	n := testNetwork(t, fake)

	status, summary, err := n.Receipt(context.Background(), txHashFixture)
	if err != nil {
		t.Fatalf("Receipt: %v", err)
	}
	if status != transaction.TxPending {
		t.Errorf("status = %s, want pending", status)
	}
	if summary != nil {
		t.Errorf("summary = %+v, want nil while not included", summary)
	}
}

func TestNetworkReceiptUnknown(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)

	status, summary, err := n.Receipt(context.Background(), txHashFixture)
	if err != nil {
		t.Fatalf("Receipt: %v", err)
	}
	if status != transaction.TxUnknown {
		t.Errorf("status = %s, want unknown", status)
	}
	if summary != nil {
		t.Errorf("summary = %+v, want nil while unknown", summary)
	}
}

func TestNetworkReceiptRejectsBadHash(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	for _, bad := range []string{"", "zz", "0x12", "0x0000000000000000000000000000000000000000000000000000000000000000"} {
		if _, _, err := n.Receipt(context.Background(), bad); err == nil {
			t.Errorf("hash %q: err = nil, want error", bad)
		}
	}
	if fake.callCount("transaction_receipt") != 0 {
		t.Error("a malformed hash must not reach the endpoint")
	}
}

func TestNetworkReceiptClassifiesEndpointFailure(t *testing.T) {
	fake := newFakeClient(1)
	fake.fail("transaction_receipt", &net.OpError{Op: "dial", Err: errors.New("refused")})
	n := testNetwork(t, fake)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, _, err := n.Receipt(ctx, txHashFixture); err == nil {
		t.Fatal("Receipt over a dead endpoint: err = nil, want error")
	}
}
