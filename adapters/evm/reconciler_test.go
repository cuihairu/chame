package evm

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
)

// recNetwork is the logical network the fixture records name.
var recNetwork = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}

// recordFixture builds one recorded transaction the recovery worker
// would hand over.
func recordFixture(id, chainHash string) transaction.Transaction {
	return transaction.Transaction{
		ID:        transaction.TransactionID(id),
		Network:   recNetwork,
		Status:    transaction.TxSubmitted,
		ChainHash: chainHash,
	}
}

func TestStatusOfReportsTheReceipt(t *testing.T) {
	fake := newFakeClient(1)
	fake.withReceipt(txHashFixture, &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(42),
	})
	n := testNetwork(t, fake)
	tx := recordFixture("tx-1", txHashFixture)

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxIncluded {
		t.Errorf("status = %s, want included", status)
	}
}

func TestStatusOfReportsReverted(t *testing.T) {
	fake := newFakeClient(1)
	fake.withReceipt(txHashFixture, &types.Receipt{
		Status:      types.ReceiptStatusFailed,
		BlockNumber: big.NewInt(42),
	})
	n := testNetwork(t, fake)
	tx := recordFixture("tx-2", txHashFixture)

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxReverted {
		t.Errorf("status = %s, want reverted", status)
	}
}

// TestStatusOfReportsPendingAndUnknown distinguishes a transaction the
// node still holds from one it no longer knows: the first is on its
// way, the second is an open outcome for reconciliation.
func TestStatusOfReportsPendingAndUnknown(t *testing.T) {
	fake := newFakeClient(1)
	fake.withPooled(txHashFixture, &types.Transaction{})
	n := testNetwork(t, fake)
	tx := recordFixture("tx-3", txHashFixture)

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxPending {
		t.Errorf("status = %s, want pending", status)
	}

	quiet := newFakeClient(1)
	n = testNetwork(t, quiet)
	status, err = n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxUnknown {
		t.Errorf("status = %s, want unknown", status)
	}
}

// TestStatusOfSkipsRecordsWithoutAChainHash leaves records without a
// chain reference alone: there is nothing to ask the chain about.
func TestStatusOfSkipsRecordsWithoutAChainHash(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	tx := recordFixture("tx-4", "")

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxSubmitted {
		t.Errorf("status = %s, want the record's own status", status)
	}
	if fake.callCount("transaction_receipt") != 0 {
		t.Error("a record without a chain reference must not query the chain")
	}
}

// TestStatusOfPropagatesQueryFailure reports a chain failure rather
// than a status: a failed query is not evidence of anything.
func TestStatusOfPropagatesQueryFailure(t *testing.T) {
	fake := newFakeClient(1)
	fake.fail("transaction_receipt", errors.New("read: connection reset"))
	n := testNetwork(t, fake)
	tx := recordFixture("tx-5", txHashFixture)

	if _, err := n.StatusOf(context.Background(), tx); err == nil {
		t.Fatal("StatusOf over a failing chain: err = nil, want error")
	}
}
