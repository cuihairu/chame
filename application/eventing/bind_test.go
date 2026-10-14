package eventing

import (
	"context"
	"testing"

	"github.com/cuihairu/chame/capability/event"
	"github.com/cuihairu/chame/core/module"
	"github.com/cuihairu/chame/infrastructure/persistence/memory"
)

func TestBindModuleHandlers(t *testing.T) {
	src := newFakeSource(testNet, event.SourceEVMLog)
	src.addBlock(10, "0xa", 1)
	var id event.EventID
	for _, ev := range src.blocks[10] {
		id = ev.ID
	}

	store := module.NewStore()
	reg := module.NewRegistry(store, "game")
	if err := reg.AddEventHandler("deposit.detected", func(_ context.Context, ev event.Event) error {
		if ev.ID != id {
			t.Errorf("handler got %s, want %s", ev.ID, id)
		}
		return nil
	}); err != nil {
		t.Fatalf("register handler: %v", err)
	}

	d, err := NewDispatcher(memory.NewEventStore(), Config{}, WithSource(src))
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}
	if err := d.Bind(reg); err != nil {
		t.Fatalf("bind: %v", err)
	}
	report, err := d.Tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if report.Delivered != 1 {
		t.Fatalf("a module-bound handler must deliver: %+v", report)
	}
}

func TestBindRejectsForeignHandlersAndDuplicates(t *testing.T) {
	src := newFakeSource(testNet, event.SourceEVMLog)
	d, err := NewDispatcher(memory.NewEventStore(), Config{}, WithSource(src))
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	store := module.NewStore()
	reg := module.NewRegistry(store, "mod")
	if err := reg.AddEventHandler("deposit.detected", "not a handler"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := d.Bind(reg); err == nil {
		t.Fatal("a foreign handler type must be rejected")
	}

	// A statically registered handler wins; a module claiming the same
	// type is a configuration conflict.
	if err := d.Handle("withdrawn.detected", func(context.Context, event.Event) error { return nil }); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if err := reg.AddEventHandler("withdrawn.detected", func(context.Context, event.Event) error { return nil }); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := d.Bind(reg); err == nil {
		t.Fatal("a duplicate event type must be rejected")
	}
}
