package transaction

import (
	"errors"
	"testing"
	"time"
)

var allStatuses = []TransactionStatus{
	TxCreated, TxPrepared, TxSigned, TxSubmitted, TxPending,
	TxIncluded, TxConfirmed, TxFinalized, TxReverted, TxFailed,
	TxReorged, TxUnknown,
}

// TestMatrixGolden pins the full transition matrix. Every change here is
// an API-level contract change and must be justified against spec §7.8
// and §10.4.
func TestMatrixGolden(t *testing.T) {
	golden := map[TransactionStatus][]TransactionStatus{
		TxCreated:   {TxPrepared, TxFailed},
		TxPrepared:  {TxSigned, TxFailed},
		TxSigned:    {TxSubmitted, TxUnknown, TxFailed},
		TxSubmitted: {TxPending, TxIncluded, TxReverted, TxFailed, TxUnknown},
		TxPending:   {TxIncluded, TxReverted, TxFailed, TxUnknown},
		TxIncluded:  {TxConfirmed, TxReverted, TxReorged},
		TxConfirmed: {TxFinalized, TxReorged},
		TxFinalized: {},
		TxReverted:  {},
		TxFailed:    {},
		TxReorged:   {TxUnknown},
		TxUnknown:   {TxPending, TxIncluded, TxConfirmed, TxFinalized, TxReverted, TxFailed},
	}
	for _, from := range allStatuses {
		got := TransitionsFrom(from)
		want := golden[from]
		if len(got) != len(want) {
			t.Fatalf("TransitionsFrom(%s) = %v, want %v", from, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("TransitionsFrom(%s)[%d] = %s, want %s", from, i, got[i], want[i])
			}
		}
	}
	// The matrix covers exactly the known statuses.
	for _, s := range allStatuses {
		if !s.Valid() {
			t.Errorf("%s should be valid", s)
		}
	}
	if TransactionStatus("invented").Valid() {
		t.Error("unknown status should be invalid")
	}
}

// TestFullMatrix walks every pair; exactly the golden pairs are legal.
func TestFullMatrix(t *testing.T) {
	legal := map[TransactionStatus]map[TransactionStatus]bool{}
	for from, tos := range legalTransitions {
		legal[from] = map[TransactionStatus]bool{}
		for _, to := range tos {
			legal[from][to] = true
		}
	}
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			want := from == to || legal[from][to]
			if got := CanTransition(from, to); got != want {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	// Invalid statuses never transition.
	if CanTransition("bogus", TxCreated) || CanTransition(TxCreated, "bogus") {
		t.Error("invalid statuses must never transition")
	}
}

func TestTerminalFrozen(t *testing.T) {
	for _, term := range []TransactionStatus{TxFinalized, TxReverted, TxFailed} {
		if !term.Terminal() {
			t.Errorf("%s should be terminal", term)
		}
		if TransitionsFrom(term) != nil && len(TransitionsFrom(term)) != 0 {
			t.Errorf("%s must have no outgoing transitions", term)
		}
		for _, to := range allStatuses {
			if to != term && CanTransition(term, to) {
				t.Errorf("terminal %s -> %s must be illegal", term, to)
			}
		}
	}
	for _, nonTerm := range []TransactionStatus{TxCreated, TxPrepared, TxSigned, TxSubmitted, TxPending, TxIncluded, TxConfirmed, TxReorged, TxUnknown} {
		if nonTerm.Terminal() {
			t.Errorf("%s should not be terminal", nonTerm)
		}
		if len(TransitionsFrom(nonTerm)) == 0 {
			t.Errorf("%s must have at least one legal successor", nonTerm)
		}
	}
}

func TestUncertaintyExpressed(t *testing.T) {
	// spec §22: the model must express an unknown outcome.
	if !TxUnknown.Uncertain() || !TxReorged.Uncertain() {
		t.Error("unknown and reorged must be uncertain")
	}
	for _, s := range allStatuses {
		if s != TxUnknown && s != TxReorged && s.Uncertain() {
			t.Errorf("%s should not be uncertain", s)
		}
	}
	// Submission timeouts land in unknown, never failed.
	if !CanTransition(TxSigned, TxUnknown) || !CanTransition(TxSubmitted, TxUnknown) {
		t.Error("submitted work must be able to become unknown")
	}
}

func TestNoBackwardsRollback(t *testing.T) {
	// Certainty never decreases except via reorged.
	forbidden := []struct{ from, to TransactionStatus }{
		{TxIncluded, TxPending},
		{TxConfirmed, TxIncluded},
		{TxFinalized, TxConfirmed},
		{TxReverted, TxIncluded},
		{TxUnknown, TxReorged},
		{TxConfirmed, TxUnknown},
		{TxCreated, TxSigned}, // skipping prepared
		{TxPrepared, TxSubmitted},
		{TxCreated, TxFinalized},
	}
	for _, f := range forbidden {
		if CanTransition(f.from, f.to) {
			t.Errorf("%s -> %s must be illegal", f.from, f.to)
		}
	}
	// The only legal path out of reorged is unknown.
	if !CanTransition(TxReorged, TxUnknown) {
		t.Error("reorged -> unknown must be legal")
	}
	// Unknown resolves through reconciliation to any settled status.
	for _, resolvable := range []TransactionStatus{TxIncluded, TxConfirmed, TxFinalized, TxReverted, TxFailed} {
		if !CanTransition(TxUnknown, resolvable) {
			t.Errorf("unknown -> %s must be legal", resolvable)
		}
	}
}

func TestTransitionTo(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	tx := Transaction{ID: "tx-1", Status: TxSigned}
	next, err := tx.TransitionTo(TxSubmitted, now)
	if err != nil {
		t.Fatalf("legal transition rejected: %v", err)
	}
	if next.Status != TxSubmitted || !next.UpdatedAt.Equal(now) {
		t.Errorf("next = %+v", next)
	}
	// Receiver unchanged.
	if tx.Status != TxSigned {
		t.Error("TransitionTo must not mutate the receiver")
	}
	// Illegal transition error is detectable and names both statuses.
	_, err = next.TransitionTo(TxCreated, now)
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("err = %v, want ErrIllegalTransition", err)
	}
}
