// Package lifecycle defines the shared runtime lifecycle states and
// transition rules (spec §4.2).
package lifecycle

import "errors"

// State is a runtime lifecycle state (spec §4.2).
type State int8

const (
	// StateNew accepts module registration.
	StateNew State = iota
	// StateConfiguring merges and validates configuration.
	StateConfiguring
	// StateInitializing creates module resources; no uncontrolled
	// long-running tasks should start here.
	StateInitializing
	// StateStarting starts modules in dependency order.
	StateStarting
	// StateRunning serves normally.
	StateRunning
	// StateStopping refuses new work and finishes what can be finished.
	StateStopping
	// StateStopped is final; a stopped runtime must not be restarted.
	StateStopped
)

// String returns the spec name of the state.
func (s State) String() string {
	switch s {
	case StateNew:
		return "New"
	case StateConfiguring:
		return "Configuring"
	case StateInitializing:
		return "Initializing"
	case StateStarting:
		return "Starting"
	case StateRunning:
		return "Running"
	case StateStopping:
		return "Stopping"
	case StateStopped:
		return "Stopped"
	default:
		return "Unknown"
	}
}

// Valid reports whether s is a known state.
func (s State) Valid() bool { return s >= StateNew && s <= StateStopped }

// Terminal reports whether s is a final state.
func (s State) Terminal() bool { return s == StateStopped }

// ErrInvalidState is returned when a transition is not allowed.
var ErrInvalidState = errors.New("lifecycle: invalid state transition")

// CanTransition reports whether a runtime may move from s to next along
// the path defined in spec §4.2. Forward movement is sequential; any
// state before Running may go to Stopping on failure; only Running may
// enter Stopping normally.
func CanTransition(s, next State) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	if next == s {
		return true
	}
	// Sequential forward path.
	if next == s+1 {
		return true
	}
	// Any pre-running phase fails into Stopping; Running stops normally.
	if next == StateStopping && s < StateRunning {
		return true
	}
	// Stopped follows Stopping only.
	return false
}
