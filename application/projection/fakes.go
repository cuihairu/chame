package projection

import (
	"context"
	"sync"

	"github.com/cuihairu/chame/capability/event"
)

// FakeA models an indexer that maintains its read model in a keyed map.
type FakeA struct {
	mu   sync.Mutex
	rows map[string]int64
}

func NewFakeA() *FakeA { return &FakeA{rows: map[string]int64{}} }
func (b *FakeA) Reset(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows = map[string]int64{}
	return nil
}
func (b *FakeA) Apply(_ context.Context, ev event.Event) error {
	c, err := decode(ev)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows[projectionKey(c)] += c.Delta
	return nil
}
func (b *FakeA) Snapshot(context.Context) (map[string]int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]int64{}
	for k, v := range b.rows {
		out[k] = v
	}
	return out, nil
}

// FakeB models an independently implemented append-oriented indexer. Snapshot
// aggregates its rows, giving a second adapter implementation to compare.
type FakeB struct {
	mu   sync.Mutex
	rows []struct {
		key   string
		delta int64
	}
}

func NewFakeB() *FakeB { return &FakeB{} }
func (b *FakeB) Reset(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows = nil
	return nil
}
func (b *FakeB) Apply(_ context.Context, ev event.Event) error {
	c, err := decode(ev)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.rows = append(b.rows, struct {
		key   string
		delta int64
	}{projectionKey(c), c.Delta})
	return nil
}
func (b *FakeB) Snapshot(context.Context) (map[string]int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]int64{}
	for _, r := range b.rows {
		out[r.key] += r.delta
	}
	return out, nil
}
