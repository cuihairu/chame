package prometheus

import (
	"context"
	"errors"
	"math/big"
	"sort"
	"sync"
)

// Cost is an observed chain cost. Fee is the smallest-unit decimal string to
// avoid floating point loss. Fake/test environments may supply deterministic values.
type Cost struct {
	IdempotencyKey, TransactionID, Network string
	Gas                                    uint64
	Fee                                    string
}

// CostSummary aggregates one key and network without mixing denominations.
type CostSummary struct {
	IdempotencyKey, Network string
	Transactions            int
	Gas                     uint64
	Fee                     string
}

// CostRecorder and CostQuery are the integration seams for durable host stores.
type CostRecorder interface {
	Record(context.Context, Cost) error
}
type CostQuery interface {
	AggregateByIdempotencyKey(context.Context, string) ([]CostSummary, error)
}

var ErrCostNotFound = errors.New("observability: transaction cost not found")

// CostStore records actual or fake costs and aggregates by internal idempotency key.
type CostStore struct {
	mu   sync.RWMutex
	rows map[string]Cost
}

func NewCostStore() *CostStore { return &CostStore{rows: make(map[string]Cost)} }
func (s *CostStore) Record(_ context.Context, c Cost) error {
	if c.IdempotencyKey == "" || c.TransactionID == "" || c.Network == "" || c.Fee == "" {
		return errors.New("observability: incomplete cost")
	}
	if n, ok := new(big.Int).SetString(c.Fee, 10); !ok || n.Sign() < 0 {
		return errors.New("observability: fee must be a non-negative decimal integer")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[c.TransactionID] = c
	return nil
}

// AggregateByIdempotencyKey sums gas and fee by network for the internal key.
func (s *CostStore) AggregateByIdempotencyKey(_ context.Context, key string) ([]CostSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	byNetwork := make(map[string]*CostSummary)
	for _, c := range s.rows {
		if c.IdempotencyKey == key {
			entry := byNetwork[c.Network]
			if entry == nil {
				entry = &CostSummary{IdempotencyKey: key, Network: c.Network, Fee: "0"}
				byNetwork[c.Network] = entry
			}
			entry.Transactions++
			entry.Gas += c.Gas
			fee, _ := new(big.Int).SetString(entry.Fee, 10)
			add, _ := new(big.Int).SetString(c.Fee, 10)
			entry.Fee = fee.Add(fee, add).String()
		}
	}
	if len(byNetwork) == 0 {
		return nil, ErrCostNotFound
	}
	networks := make([]string, 0, len(byNetwork))
	for n := range byNetwork {
		networks = append(networks, n)
	}
	sort.Strings(networks)
	out := make([]CostSummary, 0, len(networks))
	for _, n := range networks {
		out = append(out, *byNetwork[n])
	}
	return out, nil
}
