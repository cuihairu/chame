package finality

import "testing"

// TestStateVocabularyIsStable pins the serialized values: they are part
// of the persisted event contract and adapters' stored observations.
func TestStateVocabularyIsStable(t *testing.T) {
	cases := map[State]string{
		Pending:   "pending",
		Confirmed: "confirmed",
		Final:     "final",
	}
	for s, want := range cases {
		if string(s) != want {
			t.Errorf("state %q, want %q", string(s), want)
		}
		if !s.Valid() {
			t.Errorf("state %q reported invalid", string(s))
		}
	}
}

// TestRankOrdersStates pins the "at least this state" comparisons the
// dispatcher gate relies on.
func TestRankOrdersStates(t *testing.T) {
	if Pending.Rank() >= Confirmed.Rank() || Confirmed.Rank() >= Final.Rank() {
		t.Fatalf("ranks pending=%d confirmed=%d final=%d", Pending.Rank(), Confirmed.Rank(), Final.Rank())
	}
	if State("").Valid() || State("settled").Valid() {
		t.Fatal("unknown states must not validate")
	}
	if State("").Rank() >= 0 {
		t.Fatal("unknown state must rank below every known state")
	}
}
