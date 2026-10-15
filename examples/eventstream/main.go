// Command eventstream shows the chain-event consumption pattern (spec
// §11): a source, a repository, a dispatcher with handlers, finality
// gating, and resumable cursors. The chain here is simulated in-process,
// so the example runs with no chain service anywhere (spec §16.3).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cuihairu/chame/application/eventing"
	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

// transferType routes demo events to the handler below.
const transferType = "demo.transfer"

// transfer is the raw payload, owned by the source (spec §11.2): the
// framework persists it as data and never interprets it.
type transfer struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

// demoChain is one in-process event source: every poll mines one block,
// and the first blocks carry one transfer each. It implements the same
// contract (capability/event.Source) the EVM log source and the Solana
// signature source implement against real nodes, with the same
// disciplines: a cursor that never degrades to a bare integer (§11.5)
// and finality read from the chain's own depth (§11.4).
type demoChain struct {
	net identifiers.NetworkID

	mu   sync.Mutex
	head uint64 // highest mined block
	last uint64 // highest slot that carries an event
}

func (c *demoChain) Network() identifiers.NetworkID { return c.net }
func (c *demoChain) Type() event.SourceType         { return event.SourceEVMLog }

// blockHash is the deterministic block identity at height.
func blockHash(height uint64) string { return fmt.Sprintf("0x%064x", height) }

// txHash is the one transaction each demo block carries.
func txHash(height uint64) string { return fmt.Sprintf("0x%063x", height) }

// Poll mines one block and returns the events after the cursor, oldest
// first. The chain keeps growing past the last event, so every event
// eventually reaches confirmation depth. A window without events still
// advances the cursor: nothing new means nothing returned.
func (c *demoChain) Poll(ctx context.Context, cursor event.Cursor) ([]event.Event, event.Cursor, error) {
	c.mu.Lock()
	c.head++
	head := c.head
	c.mu.Unlock()

	var events []event.Event
	now := time.Now()
	for slot := cursor.BlockHeight + 1; slot <= head && slot <= c.last; slot++ {
		data, err := json.Marshal(transfer{
			From:   fmt.Sprintf("player-%d", slot),
			To:     "treasury",
			Amount: "10",
		})
		if err != nil {
			return nil, event.Cursor{}, err
		}
		events = append(events, event.Event{
			ID:            event.PositionKey(c.net, event.SourceEVMLog, txHash(slot), blockHash(slot), slot, 0, ""),
			Network:       c.net,
			SourceType:    event.SourceEVMLog,
			TransactionID: txHash(slot),
			BlockHash:     blockHash(slot),
			BlockHeight:   slot,
			Type:          transferType,
			Data:          data,
			DecodeVersion: 1,
			At:            now,
			CreatedAt:     now,
			UpdatedAt:     now,
			Observation:   event.ObservationObserved,
			Finality:      event.FinalityPending,
			Processing:    event.ProcessingQueued,
		})
	}
	next := cursor
	next.BlockHeight = head
	next.BlockHash = blockHash(head)
	next.UpdatedAt = now
	return events, next, nil
}

// Head returns the canonical block hash at height, for reorg comparison
// (§11.6). The demo chain never reorganizes.
func (c *demoChain) Head(ctx context.Context, height uint64) (string, error) {
	return blockHash(height), nil
}

// Observe reports the confirmation depth: one block deep is confirmed,
// three deep is final. The EVM adapter's real ladder waits twelve blocks
// for final (ADR-0006); the demo chain is shallower so the example
// finishes fast.
func (c *demoChain) Observe(ctx context.Context, ev event.Event) (event.FinalityState, error) {
	c.mu.Lock()
	head := c.head
	c.mu.Unlock()
	if head < ev.BlockHeight {
		return event.FinalityPending, nil
	}
	switch depth := head - ev.BlockHeight; {
	case depth >= 3:
		return event.FinalityFinal, nil
	case depth >= 1:
		return event.FinalityConfirmed, nil
	default:
		return event.FinalityPending, nil
	}
}

func main() {
	chain := &demoChain{
		net:  identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "example", ChainID: "1"},
		last: 4,
	}
	store := memory.NewEventStore()

	d, err := eventing.NewDispatcher(store, eventing.Config{
		PollInterval: 200 * time.Millisecond,
	}, eventing.WithSource(chain))
	if err != nil {
		fmt.Fprintln(os.Stderr, "dispatcher:", err)
		os.Exit(1)
	}
	// The handler is idempotent by contract (§11.4): delivery is at least
	// once, so replaying an event must be a no-op for the business.
	if err := d.Handle(transferType, func(ctx context.Context, ev event.Event) error {
		var t transfer
		if err := json.Unmarshal(ev.Data, &t); err != nil {
			return err
		}
		slog.Info("delivered",
			slog.String("type", ev.Type),
			slog.Uint64("block", ev.BlockHeight),
			slog.String("from", t.From),
			slog.String("to", t.To),
			slog.String("amount", t.Amount),
			slog.String("finality", string(ev.Finality)))
		return nil
	}); err != nil {
		fmt.Fprintln(os.Stderr, "handler:", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("consuming events; press Ctrl-C to stop")
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(os.Stderr, "dispatcher:", err)
		os.Exit(1)
	}
	slog.Info("clean shutdown")
}
