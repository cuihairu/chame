package evm

import (
	"context"
	"encoding/hex"
	"encoding/json"

	"github.com/ethereum/go-ethereum/common"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/clock"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// Finality depths (ADR-0006: per-network configurable, mainnet-class
// defaults applied when unset).
const (
	// defaultPollDepth is the confirmation window: a block becomes
	// pollable once one block has been built on top of it. One block
	// deep is the minimum window that keeps a poll from reading a block
	// a reorg could still remove.
	defaultPollDepth = 1
	// defaultFinalityDepth is the depth at which a log reaches final.
	defaultFinalityDepth = 12
)

// anonymousLogType is the normalized type for a log without a signature
// topic: anonymous events carry no event signature to key a handler by
// (spec §11.3: the type must never be empty).
const anonymousLogType = "log"

// zeroHashHex is what a node that omits a hash reports; the event
// identity cannot be built from a zero block or transaction hash
// (spec §11.3).
var zeroHashHex = common.Hash{}.Hex()

// LogSourceConfig tunes one log source. Zero values take the defaults.
type LogSourceConfig struct {
	// Filter selects the logs that become events; the zero value accepts
	// every log on the network.
	Filter LogFilter
	// Depth is the confirmation window: only blocks at least Depth deep
	// are polled (spec §11.5: the window is the adapter's reorg safety
	// margin). Default 1.
	Depth uint64
	// FinalityDepth is the depth at which an event is final (ADR-0006).
	// Default 12.
	FinalityDepth uint64
}

func (c LogSourceConfig) withDefaults() LogSourceConfig {
	if c.Depth == 0 {
		c.Depth = defaultPollDepth
	}
	if c.FinalityDepth == 0 {
		c.FinalityDepth = defaultFinalityDepth
	}
	return c
}

// LogSource implements event.Source for EVM logs (spec §11.1): it polls
// the confirmation window, never a bare integer position, and reports
// finality from the chain's own block depths.
type LogSource struct {
	net *Network
	cfg LogSourceConfig
	now clock.Clock
}

// NewLogSource builds the source for one network.
func NewLogSource(n *Network, cfg LogSourceConfig) *LogSource {
	return &LogSource{net: n, cfg: cfg.withDefaults(), now: clock.System{}}
}

var _ event.Source = (*LogSource)(nil)

// Network implements event.Source.
func (s *LogSource) Network() identifiers.NetworkID { return s.net.ID() }

// Type implements event.Source.
func (s *LogSource) Type() event.SourceType { return event.SourceEVMLog }

// Poll reads committed logs after the cursor and returns the furthest
// committable position with its hash (spec §11.5). Nothing committable
// leaves the cursor untouched.
func (s *LogSource) Poll(ctx context.Context, cursor event.Cursor) ([]event.Event, event.Cursor, error) {
	head, err := s.net.BlockNumber(ctx)
	if err != nil {
		return nil, event.Cursor{}, err
	}
	if head < s.cfg.Depth {
		// Nothing has reached the confirmation window yet.
		return nil, cursor, nil
	}
	end := head - s.cfg.Depth
	start := cursor.BlockHeight + 1
	if end < start {
		return nil, cursor, nil
	}

	logs, err := s.net.Logs(ctx, LogFilter{
		FromBlock: start,
		ToBlock:   &end,
		Addresses: s.cfg.Filter.Addresses,
		Topics:    s.cfg.Filter.Topics,
	})
	if err != nil {
		return nil, event.Cursor{}, err
	}

	events := make([]event.Event, 0, len(logs))
	observed := s.now.Now()
	for i := range logs {
		l := &logs[i]
		if l.BlockHash == zeroHashHex {
			return nil, event.Cursor{}, cherrs.New(cherrs.EventDecodeFailure,
				s.net.opBase+".logs", "log %d in block %d carries no block hash: event identity requires it (spec §11.3)",
				i, l.BlockNumber)
		}
		if l.TxHash == zeroHashHex {
			return nil, event.Cursor{}, cherrs.New(cherrs.EventDecodeFailure,
				s.net.opBase+".logs", "log %d in block %d carries no transaction hash: event identity requires it (spec §11.3)",
				i, l.BlockNumber)
		}
		data, err := json.Marshal(logPayload{
			Address: l.Address.Value,
			Topics:  l.Topics,
			Data:    hexOf(l.Data),
		})
		if err != nil {
			return nil, event.Cursor{}, cherrs.New(cherrs.EventDecodeFailure,
				s.net.opBase+".logs", "log %d: %v", i, err)
		}
		events = append(events, event.Event{
			ID: event.PositionKey(s.net.ID(), event.SourceEVMLog,
				l.TxHash, l.BlockHash, l.BlockNumber, uint64(l.LogIndex), ""),
			Network:       s.net.ID(),
			SourceType:    event.SourceEVMLog,
			TransactionID: l.TxHash,
			BlockHash:     l.BlockHash,
			BlockHeight:   l.BlockNumber,
			Index:         uint64(l.LogIndex),
			Type:          normalizedLogType(l.Topics),
			Data:          data,
			DecodeVersion: 1,
			At:            observed,
			CreatedAt:     observed,
			UpdatedAt:     observed,
			Observation:   event.ObservationObserved,
			Finality:      event.FinalityPending, // Observe promotes per confirmation depth
			Processing:    event.ProcessingQueued,
		})
	}

	// The returned cursor carries the canonical hash at the furthest
	// position that may be persisted (spec §11.5): the next tick compares
	// it against Head to detect a reorganization.
	info, err := s.net.BlockByNumber(ctx, end)
	if err != nil {
		return nil, event.Cursor{}, err
	}
	next := cursor
	next.BlockHeight = end
	next.BlockHash = info.Hash
	next.UpdatedAt = s.now.Now()
	return events, next, nil
}

// Head returns the canonical block hash at height, for the dispatcher's
// reorg comparison (spec §11.6). Any inability to verify the position is
// ErrPositionUnknown, never a fabricated hash.
func (s *LogSource) Head(ctx context.Context, height uint64) (string, error) {
	if height == 0 {
		return "", event.ErrPositionUnknown
	}
	info, err := s.net.BlockByNumber(ctx, height)
	if err != nil {
		return "", event.ErrPositionUnknown
	}
	return info.Hash, nil
}

// Observe re-checks one stored event on the current chain (spec §11.4):
// the block must still be canonical, and finality comes from the chain's
// own depth. A block that no longer matches is not an error to report —
// it is the reorg path's answer — so the event stays unverifiable until
// the cursor rollback invalidates it (spec §11.6).
func (s *LogSource) Observe(ctx context.Context, ev event.Event) (event.FinalityState, error) {
	info, err := s.net.BlockByNumber(ctx, ev.BlockHeight)
	if err != nil {
		return "", event.ErrPositionUnknown
	}
	if info.Hash != ev.BlockHash {
		return "", event.ErrPositionUnknown
	}
	head, err := s.net.BlockNumber(ctx)
	if err != nil {
		return "", event.ErrPositionUnknown
	}
	// A head below the event's block cannot be confirmed at all.
	depth := uint64(0)
	if head > info.Number {
		depth = head - info.Number
	}
	switch {
	case depth >= s.cfg.FinalityDepth:
		return event.FinalityFinal, nil
	case depth >= 1:
		return event.FinalityConfirmed, nil
	default:
		return event.FinalityPending, nil
	}
}

// logPayload is the raw log payload handed to consumers: the shared
// model never interprets it (spec §11.2).
type logPayload struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// normalizedLogType selects the handler key: the event signature topic,
// or the anonymous-log type when the log carries none.
func normalizedLogType(topics []string) string {
	if len(topics) == 0 {
		return anonymousLogType
	}
	return topics[0]
}

// hexOf renders log data as hex without a prefix (empty for no data).
func hexOf(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	return hex.EncodeToString(data)
}
