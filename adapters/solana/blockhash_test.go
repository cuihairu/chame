package solana

import (
	"context"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
)

// pinFixture builds one pin whose window ends 150 blocks past height.
func pinFixture(height uint64) BlockhashPin {
	return BlockhashPin{
		Blockhash:            solanaHashFor(0x20).String(),
		LastValidBlockHeight: height + 150,
	}
}

// TestNetworkLatestBlockhashPinsWindow reads the blockhash and the
// last block height it is valid for, both at the caller's commitment.
func TestNetworkLatestBlockhashPinsWindow(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockhash(solanaHashFor(0x20), 900)
	n := newTestNetwork(f)

	pin, err := n.LatestBlockhash(context.Background(), CommitmentConfirmed)
	if err != nil {
		t.Fatalf("LatestBlockhash: %v", err)
	}
	if pin.Blockhash != solanaHashFor(0x20).String() {
		t.Errorf("blockhash = %q, want the blockhash the node pinned", pin.Blockhash)
	}
	if pin.LastValidBlockHeight != 900 {
		t.Errorf("last valid height = %d, want 900", pin.LastValidBlockHeight)
	}
	commitments := f.capturedCommitments()
	if len(commitments) != 1 || commitments[0] != CommitmentConfirmed {
		t.Errorf("commitments = %v, want one query at the caller's commitment", commitments)
	}
}

// TestNetworkLatestBlockhashRejectsEmptyBlockhash refuses a node answer
// that carries no blockhash: a pin without a hash cannot anchor a
// transaction, and the caller must not guess one.
func TestNetworkLatestBlockhashRejectsEmptyBlockhash(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockhash(solana.Hash{}, 900)
	n := newTestNetwork(f)

	if _, err := n.LatestBlockhash(context.Background(), CommitmentConfirmed); err == nil {
		t.Fatal("LatestBlockhash over an empty answer: err = nil, want error")
	}
}

// TestNetworkBlockHeight reads the current block height at the caller's
// commitment, the observation a pin's window is judged against.
func TestNetworkBlockHeight(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(742)
	n := newTestNetwork(f)

	h, err := n.BlockHeight(context.Background(), CommitmentFinalized)
	if err != nil {
		t.Fatalf("BlockHeight: %v", err)
	}
	if h != 742 {
		t.Errorf("height = %d, want 742", h)
	}
	commitments := f.capturedCommitments()
	if len(commitments) != 1 || commitments[0] != CommitmentFinalized {
		t.Errorf("commitments = %v, want one query at the caller's commitment", commitments)
	}
}

// TestBlockhashPinValidClassifiesWindow states the boundary: the pin
// stays valid below its last valid height and expires at it.
func TestBlockhashPinValidClassifiesWindow(t *testing.T) {
	pin := pinFixture(100)
	cases := []struct {
		height uint64
		want   ExpiryState
	}{
		{100, ExpiryValid},
		{249, ExpiryValid},
		{250, ExpiryExpired},
		{251, ExpiryExpired},
	}
	for _, tc := range cases {
		if got := pin.Valid(tc.height); got != tc.want {
			t.Errorf("height %d: state = %s, want %s", tc.height, got, tc.want)
		}
	}
}

// TestExpiryStillValidSkipsTheChain leaves the transaction alone while
// the blockhash is inside its window: nothing is queried, because there
// is nothing to recover.
func TestExpiryStillValidSkipsTheChain(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(100)
	n := newTestNetwork(f)

	report, err := n.Expire(context.Background(), pinFixture(100), testSignature(0x50), ExpiryOptions{})
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if report.Verdict != ExpiryStillValid || report.Height != 100 {
		t.Errorf("report = %+v, want still-valid at height 100", report)
	}
	if f.callCount("signature_status") != 0 {
		t.Error("a valid pin must not query the signature")
	}
}

// TestExpiryLanded reports a transaction the chain accepted: the business
// effect happened and must not be retried.
func TestExpiryLanded(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(300)
	confirmations := uint64(40)
	f.signatureStat = []*rpcSignatureStatusesResult{{
		Slot:               260,
		Confirmations:      &confirmations,
		ConfirmationStatus: rpc.ConfirmationStatusFinalized,
	}}
	n := newTestNetwork(f)

	report, err := n.Expire(context.Background(), pinFixture(100), testSignature(0x50), ExpiryOptions{})
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if report.Verdict != ExpiryLanded {
		t.Errorf("verdict = %s, want landed", report.Verdict)
	}
	if report.Status == nil || report.Status.Slot != 260 || report.Status.ErrDetail != "" {
		t.Errorf("status = %+v, want the observed slot without an error", report.Status)
	}
}

// TestExpiryFailed reports an execution failure: the transaction was
// processed and did not succeed. Terminal, not a retry — the business
// compensates instead of resending.
func TestExpiryFailed(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(300)
	confirmations := uint64(40)
	f.signatureStat = []*rpcSignatureStatusesResult{{
		Slot:               260,
		Confirmations:      &confirmations,
		Err:                map[string]any{"custom": "InstructionError(0, Custom(1))"},
		ConfirmationStatus: rpc.ConfirmationStatusFinalized,
	}}
	n := newTestNetwork(f)

	report, err := n.Expire(context.Background(), pinFixture(100), testSignature(0x50), ExpiryOptions{})
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if report.Verdict != ExpiryFailed {
		t.Errorf("verdict = %s, want failed", report.Verdict)
	}
	if report.Status == nil || report.Status.ErrDetail == "" {
		t.Errorf("status = %+v, want the error detail", report.Status)
	}
}

// TestExpiryUncertainWithoutHistorySearch is the rule spec §8.4 states:
// an expired blockhash with no observed status does not prove the
// transaction did not run, so it stays unknown and keeps reconciling.
func TestExpiryUncertainWithoutHistorySearch(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(300)
	f.signatureStat = []*rpcSignatureStatusesResult{nil}
	n := newTestNetwork(f)

	report, err := n.Expire(context.Background(), pinFixture(100), testSignature(0x50), ExpiryOptions{})
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if report.Verdict != ExpiryUncertain {
		t.Errorf("verdict = %s, want uncertain", report.Verdict)
	}
	if report.Status != nil {
		t.Errorf("status = %+v, want no observation", report.Status)
	}
	if report.HistorySearched {
		t.Error("the ledger search was not requested")
	}
}

// TestExpiryRebuildableWithHistorySearch is the one case that permits a
// rebuild: the window has closed and the ledger search found nothing at
// all. Even then the report carries the evidence, not a guarantee.
func TestExpiryRebuildableWithHistorySearch(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(300)
	f.signatureStat = []*rpcSignatureStatusesResult{nil}
	n := newTestNetwork(f)

	report, err := n.Expire(context.Background(), pinFixture(100), testSignature(0x50),
		ExpiryOptions{SearchHistory: true})
	if err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if report.Verdict != ExpiryRebuildable {
		t.Errorf("verdict = %s, want rebuildable", report.Verdict)
	}
	if !report.HistorySearched || f.historySearches() != 1 {
		t.Errorf("searches = %d, history searched = %v, want one ledger-wide query",
			f.historySearches(), report.HistorySearched)
	}
}

// TestExpiryWithoutVerdictOnQueryFailure returns an error rather than a
// verdict when the chain cannot be asked: a failed query is not evidence
// that the transaction did not run.
func TestExpiryWithoutVerdictOnQueryFailure(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.withBlockHeight(300)
	f.fail("signature_status", netErr{msg: "connection refused"})
	n := newTestNetwork(f)

	if _, err := n.Expire(context.Background(), pinFixture(100), testSignature(0x50), ExpiryOptions{}); err == nil {
		t.Fatal("Expire over a failing status query: err = nil, want error")
	}
}
