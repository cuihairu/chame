package lifecycle

import "testing"

func TestStateString(t *testing.T) {
	cases := map[State]string{
		StateNew:          "New",
		StateConfiguring:  "Configuring",
		StateInitializing: "Initializing",
		StateStarting:     "Starting",
		StateRunning:      "Running",
		StateStopping:     "Stopping",
		StateStopped:      "Stopped",
		State(99):         "Unknown",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestStateValidAndTerminal(t *testing.T) {
	for s := StateNew; s <= StateStopped; s++ {
		if !s.Valid() {
			t.Errorf("state %d should be valid", s)
		}
	}
	if State(99).Valid() {
		t.Error("State(99) should not be valid")
	}
	if !StateStopped.Terminal() {
		t.Error("Stopped should be terminal")
	}
	for s := StateNew; s < StateStopped; s++ {
		if s.Terminal() {
			t.Errorf("state %s should not be terminal", s)
		}
	}
}

func TestCanTransition(t *testing.T) {
	// Forward sequential path.
	for s := StateNew; s < StateStopped; s++ {
		if !CanTransition(s, s+1) {
			t.Errorf("%s -> %s should be allowed", s, s+1)
		}
	}
	// Self-transition (no-op) allowed.
	if !CanTransition(StateRunning, StateRunning) {
		t.Error("Running -> Running should be allowed")
	}
	// Pre-running phases fail into Stopping.
	for s := StateNew; s < StateRunning; s++ {
		if !CanTransition(s, StateStopping) {
			t.Errorf("%s -> Stopping should be allowed on failure", s)
		}
	}
	// Stopped is final.
	for s := StateNew; s <= StateStopped; s++ {
		if s != StateStopped && CanTransition(StateStopped, s) {
			t.Errorf("Stopped -> %s must not be allowed", s)
		}
	}
	// Skipping phases is not allowed.
	if CanTransition(StateNew, StateStarting) {
		t.Error("New -> Starting must not be allowed")
	}
	// Restarting a stopped runtime is not allowed.
	if CanTransition(StateStopped, StateNew) {
		t.Error("Stopped -> New must not be allowed")
	}
	// Unknown states never transition.
	if CanTransition(State(99), StateNew) || CanTransition(StateNew, State(99)) {
		t.Error("unknown states must not transition")
	}
}
