package finality

// State is the finality state of one chain observation (spec §11.2,
// §11.4). It is the shared vocabulary for the finality capability slot
// (capability.Finality): adapters map their per-network confirmation
// knobs (EVM depth, Solana commitment) onto these states, and the event
// dispatcher gates delivery on them. The serialized values are part of
// the persisted event contract and must not change.
type State string

const (
	// Pending means the observation is on-chain but below the network's
	// confirmation threshold.
	Pending State = "pending"
	// Confirmed means the network's confirmation threshold is met.
	Confirmed State = "confirmed"
	// Final means the network considers the observation irreversible.
	Final State = "final"
)

// Rank orders the states so a requirement can be expressed as "at least
// this state". An unknown state ranks below every known one.
func (s State) Rank() int {
	switch s {
	case Pending:
		return 0
	case Confirmed:
		return 1
	case Final:
		return 2
	default:
		return -1
	}
}

// Valid reports whether s is a known finality state.
func (s State) Valid() bool { return s.Rank() >= 0 }
