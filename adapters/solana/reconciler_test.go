package solana

import (
	"context"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/capability/transaction"
)

// recTx builds one recorded transaction the recovery worker would hand
// over, carrying the adapter-owned chain record the flow stored.
func recTx(id, chainHash string, data any) transaction.Transaction {
	return transaction.Transaction{
		ID:        transaction.TransactionID(id),
		Status:    transaction.TxSubmitted,
		ChainHash: chainHash,
		ChainData: data,
	}
}

// recStatus plants one signature-status answer on the fake.
func recStatus(status rpc.ConfirmationStatusType, err any) []*rpcSignatureStatusesResult {
	confirmations := uint64(40)
	return []*rpcSignatureStatusesResult{{
		Slot:               260,
		Confirmations:      &confirmations,
		ConfirmationStatus: status,
		Err:                err,
	}}
}

// TestStatusOfMapsTheConfirmationLadder reads the node's confirmation
// depth as the strongest state the record may claim.
func TestStatusOfMapsTheConfirmationLadder(t *testing.T) {
	cases := []struct {
		node rpc.ConfirmationStatusType
		want transaction.TransactionStatus
	}{
		{rpc.ConfirmationStatusProcessed, transaction.TxIncluded},
		{rpc.ConfirmationStatusConfirmed, transaction.TxConfirmed},
		{rpc.ConfirmationStatusFinalized, transaction.TxFinalized},
	}
	for _, tc := range cases {
		f := newFakeClient(solanaHashFor(0x10))
		f.signatureStat = recStatus(tc.node, nil)
		n := newTestNetwork(f)
		tx := recTx("tx-ladder", testSignature(0x50), nil)

		status, err := n.StatusOf(context.Background(), tx)
		if err != nil {
			t.Fatalf("%s: StatusOf: %v", tc.node, err)
		}
		if status != tc.want {
			t.Errorf("%s: status = %s, want %s", tc.node, status, tc.want)
		}
	}
}

// TestStatusOfReportsReverted reports an execution failure as terminal:
// the transaction was processed and did not succeed, whatever depth it
// reached afterwards.
func TestStatusOfReportsReverted(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.signatureStat = recStatus(rpc.ConfirmationStatusFinalized,
		map[string]any{"custom": "InstructionError(0, Custom(1))"})
	n := newTestNetwork(f)
	tx := recTx("tx-reverted", testSignature(0x50), nil)

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxReverted {
		t.Errorf("status = %s, want reverted", status)
	}
}

// TestStatusOfReportsPendingFromALiveWindow answers an unobserved
// signature whose blockhash window is still open with pending: the
// transaction may still land, so nothing has failed yet.
func TestStatusOfReportsPendingFromALiveWindow(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.signatureStat = []*rpcSignatureStatusesResult{nil}
	f.withBlockHeight(100)
	n := newTestNetwork(f)
	tx := recTx("tx-live", testSignature(0x50),
		&ChainRecord{Pin: pinFixture(100), Commitment: CommitmentConfirmed})

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxPending {
		t.Errorf("status = %s, want pending", status)
	}
	if len(f.commitments) != 1 || f.commitments[0] != CommitmentFinalized {
		t.Errorf("height read at %v, want finalized commitment", f.commitments)
	}
}

// TestStatusOfReportsUnknownAfterExpiry answers an unobserved signature
// whose window has closed with unknown: the transaction may or may not
// have run, and only a ledger search can say (spec §8.4).
func TestStatusOfReportsUnknownAfterExpiry(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.signatureStat = []*rpcSignatureStatusesResult{nil}
	f.withBlockHeight(300)
	n := newTestNetwork(f)
	tx := recTx("tx-expired", testSignature(0x50),
		&ChainRecord{Pin: pinFixture(100), Commitment: CommitmentConfirmed})

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxUnknown {
		t.Errorf("status = %s, want unknown", status)
	}
}

// TestStatusOfWithoutAPinIsUnknown answers an unobserved signature with
// no chain record by unknown: there is no window to judge, so the record
// stays open for the next scan rather than being declared dead.
func TestStatusOfWithoutAPinIsUnknown(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.signatureStat = []*rpcSignatureStatusesResult{nil}
	n := newTestNetwork(f)
	tx := recTx("tx-nopin", testSignature(0x50), nil)

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxUnknown {
		t.Errorf("status = %s, want unknown", status)
	}
	if f.callCount("block_height") != 0 {
		t.Error("a record without a pin must not read the block height")
	}
}

// TestStatusOfIgnoresForeignChainData treats chain data the adapter did
// not write as no chain data at all: guessing at someone else's payload
// is how records get lied about.
func TestStatusOfIgnoresForeignChainData(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.signatureStat = []*rpcSignatureStatusesResult{nil}
	n := newTestNetwork(f)
	tx := recTx("tx-foreign", testSignature(0x50), "not a chain record")

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxUnknown {
		t.Errorf("status = %s, want unknown", status)
	}
	if f.callCount("block_height") != 0 {
		t.Error("a foreign chain record must not drive a height read")
	}
}

// TestStatusOfSkipsRecordsWithoutAChainHash leaves records without a
// chain reference alone: there is nothing to ask the chain about.
func TestStatusOfSkipsRecordsWithoutAChainHash(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)
	tx := recTx("tx-nohash", "", nil)

	status, err := n.StatusOf(context.Background(), tx)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if status != transaction.TxSubmitted {
		t.Errorf("status = %s, want the record's own status", status)
	}
	if f.callCount("signature_status") != 0 {
		t.Error("a record without a chain reference must not query the chain")
	}
}

// TestStatusOfPropagatesQueryFailure reports a chain failure rather than
// a status: a failed query is not evidence of anything.
func TestStatusOfPropagatesQueryFailure(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.fail("signature_status", errors.New("read: connection reset"))
	n := newTestNetwork(f)
	tx := recTx("tx-err", testSignature(0x50), nil)

	if _, err := n.StatusOf(context.Background(), tx); err == nil {
		t.Fatal("StatusOf over a failing chain: err = nil, want error")
	}
}
