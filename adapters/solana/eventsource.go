package solana

import (
	"context"
	"encoding/json"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/clock"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// signatureType is the normalized type of one transaction-signature
// event: a signature source has no per-event type, so handlers route on
// the fixed key (spec §11.2: the type must never be empty).
const signatureType = "signature"

// signatureLocator records the identity strategy in the event position
// (§11.3): a signature source cannot give a stable in-transaction index,
// so the signature itself is the identity anchor and the locator names
// the strategy.
const signatureLocator = "signature"

// defaultSignatureLimit matches the node's getSignaturesForAddress page
// size.
const defaultSignatureLimit = 1000

// SignatureSourceConfig tunes one signature source. Zero values take the
// defaults.
type SignatureSourceConfig struct {
	// Account is the address whose signature history is polled: a
	// program's activity stream or a wallet's.
	Account identifiers.Address
	// Commitment is the confirmation level the source reads at (§8.5).
	// Zero takes the network's configured default commitment (finalized,
	// ADR-0006).
	Commitment Commitment
	// Limit is the signature-history page size. Default 1000.
	Limit int
}

// SignatureSource implements event.Source from one account's signature
// history (spec §11.1, §8.2): it polls a slot window, anchors the cursor
// on the window's blockhash (§11.5), and reports finality from the node's
// own confirmation status (ADR-0006).
type SignatureSource struct {
	net        *Network
	account    identifiers.Address
	commitment Commitment
	limit      int
	now        clock.Clock
}

// NewSignatureSource builds the source for one network.
func NewSignatureSource(n *Network, cfg SignatureSourceConfig) *SignatureSource {
	limit := cfg.Limit
	if limit <= 0 {
		limit = defaultSignatureLimit
	}
	return &SignatureSource{
		net:        n,
		account:    cfg.Account,
		commitment: n.commit(cfg.Commitment),
		limit:      limit,
		now:        clock.System{},
	}
}

var _ event.Source = (*SignatureSource)(nil)

// Network implements event.Source.
func (s *SignatureSource) Network() identifiers.NetworkID { return s.net.ID() }

// Type implements event.Source.
func (s *SignatureSource) Type() event.SourceType { return event.SourceSolanaLog }

// Poll reads committed signatures after the cursor's slot and returns
// the window end with its blockhash (§11.5). Nothing committable leaves
// the cursor untouched.
func (s *SignatureSource) Poll(ctx context.Context, cursor event.Cursor) ([]event.Event, event.Cursor, error) {
	end, err := s.net.Slot(ctx, s.commitment)
	if err != nil {
		return nil, event.Cursor{}, err
	}
	start := cursor.BlockHeight + 1
	if end < start {
		return nil, cursor, nil
	}
	// The window end anchors the next cursor (§11.5): a position the node
	// cannot describe is never persisted, not even together with events.
	info, err := s.net.Block(ctx, end, s.commitment)
	if err != nil {
		return nil, event.Cursor{}, err
	}
	if info == nil || info.Blockhash == "" {
		return nil, event.Cursor{}, event.ErrPositionUnknown
	}

	recs, err := s.net.Signatures(ctx, s.account, s.limit, s.commitment)
	if err != nil {
		return nil, event.Cursor{}, err
	}
	// The history arrives newest first: keep the signatures inside the
	// window (cursor, end]. The first record at or before the cursor ends
	// the walk; the whole rest of the page is older.
	kept := make([]SignatureRecord, 0, len(recs))
	for _, r := range recs {
		if r.Slot <= cursor.BlockHeight {
			break
		}
		if r.Slot > end {
			continue
		}
		kept = append(kept, r)
	}
	if len(recs) == s.limit && len(kept) == len(recs) {
		// The page boundary cut inside the window: advancing would
		// silently drop everything between the page end and the window
		// start (spec §7.4: never quietly resolve conflicts).
		return nil, event.Cursor{}, cherrs.New(cherrs.InternalError, s.net.opBase+".signatures",
			"signature history overran the %d-entry page for the window %d..%d: raise the source limit",
			s.limit, start, end)
	}

	// Report in chain order, oldest first (§11.4 delivery order).
	events := make([]event.Event, 0, len(kept))
	observed := s.now.Now()
	for i := len(kept) - 1; i >= 0; i-- {
		r := &kept[i]
		data, err := json.Marshal(signaturePayload{
			Signature: r.Signature,
			Slot:      r.Slot,
			Status:    r.Status,
			Err:       r.ErrDetail,
			Memo:      r.Memo,
		})
		if err != nil {
			return nil, event.Cursor{}, cherrs.New(cherrs.EventDecodeFailure, s.net.opBase+".signatures",
				"signature %s: %v", r.Signature, err)
		}
		events = append(events, event.Event{
			ID: event.PositionKey(s.net.ID(), event.SourceSolanaLog,
				r.Signature, "", r.Slot, 0, signatureLocator),
			Network:       s.net.ID(),
			SourceType:    event.SourceSolanaLog,
			TransactionID: r.Signature,
			BlockHeight:   r.Slot,
			Type:          signatureType,
			Data:          data,
			DecodeVersion: 1,
			At:            observed,
			CreatedAt:     observed,
			UpdatedAt:     observed,
			Observation:   event.ObservationObserved,
			// The poll commitment sets the floor; Observe promotes per the
			// node's own confirmation status (ADR-0006).
			Finality:   finalityOfStatus(r.Status),
			Processing: event.ProcessingQueued,
		})
	}

	// The returned cursor carries the blockhash at the window end (§11.5):
	// the next tick compares it against Head to detect a reorganization.
	next := cursor
	next.BlockHeight = end
	next.BlockHash = info.Blockhash
	next.UpdatedAt = s.now.Now()
	return events, next, nil
}

// Head returns the canonical blockhash at slot, for the dispatcher's
// reorg comparison (§11.6). A skipped or unavailable slot cannot be
// verified: ErrPositionUnknown, never a fabricated hash.
func (s *SignatureSource) Head(ctx context.Context, slot uint64) (string, error) {
	if slot == 0 {
		return "", event.ErrPositionUnknown
	}
	info, err := s.net.Block(ctx, slot, s.commitment)
	if err != nil || info == nil || info.Blockhash == "" {
		return "", event.ErrPositionUnknown
	}
	return info.Blockhash, nil
}

// Observe re-checks one stored signature on the current node (§11.4):
// the confirmation status is the chain's own answer (ADR-0006). A status
// the node no longer holds — never observed, or dropped when the slot it
// lived in left the canonical chain — is the reorg path's answer: the
// event stays unverifiable until the cursor rollback invalidates it
// (§11.6).
func (s *SignatureSource) Observe(ctx context.Context, ev event.Event) (event.FinalityState, error) {
	st, err := s.net.TransactionStatus(ctx, ev.TransactionID)
	if err != nil || st == nil {
		return "", event.ErrPositionUnknown
	}
	return finalityOfStatus(st.Status), nil
}

// signaturePayload is the raw signature record handed to consumers: the
// shared model never interprets it (§11.2).
type signaturePayload struct {
	Signature string `json:"signature"`
	Slot      uint64 `json:"slot"`
	Status    string `json:"status"`
	Err       string `json:"err,omitempty"`
	Memo      string `json:"memo,omitempty"`
}

// finalityOfStatus maps the node's confirmation level onto the shared
// finality ladder (ADR-0006: finalized commitment means final). An
// unreported or processed level stays pending: uncertainty is expressed,
// never resolved by promotion.
func finalityOfStatus(status string) event.FinalityState {
	switch status {
	case "finalized":
		return event.FinalityFinal
	case "confirmed":
		return event.FinalityConfirmed
	default:
		return event.FinalityPending
	}
}
