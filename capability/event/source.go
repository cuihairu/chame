package event

import (
	"context"
	"errors"

	"github.com/cuihairu/chame/core/identifiers"
)

// ErrPositionUnknown is returned by a Source whose Head cannot verify a
// height: the block is pruned, below the node's horizon, or not indexed.
// It is not a chain failure; a consumer keeps its cursor position and
// lets the next poll make progress.
var ErrPositionUnknown = errors.New("event: block position unknown to source")

// Source is one chain event origin (§11.1). Chain adapters implement it
// against their own log models; consumers speak only the shared model.
// Event models differ between chains and are never assumed identical
// (§11.1): Poll decodes into the shared record, with the raw payload kept
// as data and the chain-specific position kept in the identity fields.
type Source interface {
	// Network pins the source to one logical network.
	Network() identifiers.NetworkID
	// Type names the origin (§11.1).
	Type() SourceType
	// Poll reads committed events after the cursor position (§11.5). It
	// returns only events whose blocks the source considers committed
	// under its confirmation policy, plus the furthest position that may
	// be persisted as the new cursor — never a bare integer ignoring
	// reorg semantics (§11.5).
	Poll(ctx context.Context, cursor Cursor) ([]Event, Cursor, error)
	// Head returns the canonical block hash at height (the slot
	// reference for Solana). ErrPositionUnknown means the position cannot
	// be verified. Consumers compare it against the stored cursor's hash
	// to detect a reorganization (§11.6).
	Head(ctx context.Context, height uint64) (string, error)
	// Observe re-checks one stored event's finality on the current chain
	// (§11.4: wait for confirmation per configuration). It is called for
	// events whose finality has not reached the requirement yet.
	Observe(ctx context.Context, ev Event) (FinalityState, error)
}
