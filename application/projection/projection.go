// Package projection defines a rebuildable derived read model over retained
// raw chain evidence. It is downstream of eventing, not another chain poller.
package projection

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cuihairu/chame/capability/event"
)

var ErrCursorMissing = errors.New("projection: saved source cursor required")

// Backend is the replaceable indexer boundary. Implementations hold derived
// state only; Reset must never affect the raw event repository.
type Backend interface {
	Reset(context.Context) error
	Apply(context.Context, event.Event) error
	Snapshot(context.Context) (map[string]int64, error)
}

// Replayer combines the existing event evidence store/cursor with a backend.
type Replayer struct {
	Raw     event.Repository
	Backend Backend
}

// Rebuild clears only derived state, then deterministically replays raw events
// through the persisted cursor. Reorged observations remain evidence but do
// not contribute to the canonical read model.
func (r Replayer) Rebuild(ctx context.Context, key event.CursorKey) error {
	if r.Raw == nil || r.Backend == nil {
		return fmt.Errorf("projection: raw repository and backend are required")
	}
	cursor, err := r.Raw.GetCursor(ctx, key)
	if errors.Is(err, event.ErrCursorNotFound) {
		return ErrCursorMissing
	}
	if err != nil {
		return fmt.Errorf("projection: read saved cursor: %w", err)
	}
	events, err := r.Raw.ReplayEvents(ctx, key, cursor)
	if err != nil {
		return fmt.Errorf("projection: read raw events: %w", err)
	}
	if err := r.Backend.Reset(ctx); err != nil {
		return fmt.Errorf("projection: reset derived model: %w", err)
	}
	for _, ev := range events {
		if ev.Observation == event.ObservationReorged {
			continue
		}
		if err := r.Backend.Apply(ctx, ev); err != nil {
			return fmt.Errorf("projection: apply %s: %w", ev.ID, err)
		}
	}
	return nil
}

type change struct {
	Asset string `json:"asset"`
	Owner string `json:"owner"`
	Delta int64  `json:"delta"`
}

func decode(ev event.Event) (change, error) {
	var c change
	if err := json.Unmarshal(ev.Data, &c); err != nil {
		return c, fmt.Errorf("decode event %s: %w", ev.ID, err)
	}
	if c.Asset == "" || c.Owner == "" {
		return c, fmt.Errorf("event %s: asset and owner are required", ev.ID)
	}
	return c, nil
}

func projectionKey(c change) string { return c.Asset + "|" + c.Owner }
