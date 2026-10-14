package event_test

import (
	"testing"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
	events "github.com/cuihairu/chame/tests/conformance/event"
)

// TestMemoryEventStoreConformance runs the shared event persistence suite
// against the in-memory store (ADR-0002: identical semantics, tests only).
func TestMemoryEventStoreConformance(t *testing.T) {
	events.Run(t, func(t *testing.T) event.Repository {
		return memory.NewEventStore()
	})
}
