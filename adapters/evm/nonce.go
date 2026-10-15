package evm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/cuihairu/chame/core/identifiers"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// NonceState classifies one allocated nonce (spec §7.5: the coordinator
// must tell a chain-observed nonce, a locally allocated one, and a
// submitted one with an open outcome apart).
type NonceState string

const (
	// NonceAllocated marks a nonce handed out with no chain material
	// behind it yet: the build may still fail and the nonce be returned.
	NonceAllocated NonceState = "allocated"
	// NonceSubmitted marks a nonce broadcast to the network; its outcome
	// is open until the chain consumes it.
	NonceSubmitted NonceState = "submitted"
)

// ErrUnknownNonce marks a state change for a nonce the coordinator has no
// open allocation for: never handed out, already submitted, or already
// abandoned. Use errors.Is to detect it.
var ErrUnknownNonce = errors.New("evm: no open allocation for this nonce")

// NonceReconcile is one account's classification after a chain refresh
// (spec §7.5: 已提交但结果未知的交易 and 需要重新提交或人工处理的交易
// must be visible, not silently retried).
type NonceReconcile struct {
	// ChainNext is the chain's next unconsumed nonce as observed.
	ChainNext uint64
	// Open lists allocated nonces at or above ChainNext: ours to build
	// and submit.
	Open []uint64
	// InFlight lists submitted nonces the chain has not consumed: the
	// replay, bump, or cancel candidates.
	InFlight []uint64
	// Consumed lists submitted nonces the chain consumed (mined or
	// pooled).
	Consumed []uint64
	// Foreign lists allocated nonces the chain consumed without our
	// broadcast: another writer used them, and the operator must look.
	Foreign []uint64
}

// NonceSnapshot summarizes one account's allocations.
type NonceSnapshot struct {
	// Seeded reports whether the chain nonce has been observed.
	Seeded bool
	// Next is the next nonce the coordinator would hand out.
	Next uint64
	// ChainNext is the chain's next unconsumed nonce as last observed.
	ChainNext uint64
	// Allocated counts open allocations.
	Allocated int
	// Submitted counts broadcast allocations with an open outcome.
	Submitted int
}

// NonceCoordinator allocates account nonces for one EVM network (spec
// §7.5). It exists so concurrent callers never read the chain nonce and
// independently send the same value: every allocation is recorded, and
// the record distinguishes the chain-observed nonce, locally allocated
// nonces, and submitted ones with an open outcome.
//
// Scope: this coordinator coordinates within one process. Its locks are
// in-memory, so a multi-process deployment must either run a single
// writer or back the coordinator with a shared store — the coordinator
// itself does not solve cross-process races (spec §7.5: 单机内存锁不能
// 解决跨进程竞争).
//
// Failure discipline: a failed chain query never mutates state. The
// coordinator never increments, resets, or reuses a nonce on timeout —
// a stuck allocation stays visible for the operator instead of being
// silently retried (spec §7.5: 不得在超时后盲目递增或重置 Nonce).
type NonceCoordinator struct {
	net *Network

	mu       sync.Mutex
	bySender map[string]*senderNonces
}

// senderNonces is one account's allocation ledger. The mutex is held
// across the seeding query so two goroutines cannot both seed the
// account (spec §7.5: 不能让多个 goroutine 各自读取链上 Nonce 后独立
// 发送).
type senderNonces struct {
	mu sync.Mutex

	seeded    bool
	chainNext uint64
	next      uint64
	free      map[uint64]bool
	leases    map[uint64]NonceState
}

// NewNonceCoordinator returns the coordinator for one network instance.
func NewNonceCoordinator(n *Network) *NonceCoordinator {
	return &NonceCoordinator{net: n, bySender: map[string]*senderNonces{}}
}

// Next reserves the next nonce for sender. The first call for an account
// queries the chain; later calls allocate locally. An abandoned nonce is
// reused before new ones, so a failed build does not leave a hole the
// account must wait out.
func (c *NonceCoordinator) Next(ctx context.Context, sender identifiers.Address) (uint64, error) {
	s := c.sender(sender)
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.seeded {
		nonce, err := c.net.PendingNonceAt(ctx, sender)
		if err != nil {
			return 0, err
		}
		s.seeded = true
		s.chainNext = nonce
		s.next = nonce
	}
	return s.take(), nil
}

// Submitted records that a reserved nonce was broadcast. The nonce stays
// leased: a broadcast nonce is never handed out again, whatever the
// outcome turns out to be.
func (c *NonceCoordinator) Submitted(sender identifiers.Address, nonce uint64) error {
	s := c.sender(sender)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[nonce] != NonceAllocated {
		return c.unknownNonce(sender, nonce)
	}
	s.leases[nonce] = NonceSubmitted
	return nil
}

// Abandon returns a reserved nonce that will never be broadcast — the
// build failed before submission. The nonce goes back to the pool and is
// reused before new ones.
func (c *NonceCoordinator) Abandon(sender identifiers.Address, nonce uint64) error {
	s := c.sender(sender)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.leases[nonce] != NonceAllocated {
		return c.unknownNonce(sender, nonce)
	}
	delete(s.leases, nonce)
	if s.free == nil {
		s.free = map[uint64]bool{}
	}
	s.free[nonce] = true
	return nil
}

// Reconcile refreshes the chain nonce and classifies every lease (spec
// §7.5). A chain that moved backwards — a reorg, a lagging endpoint —
// never lowers the coordinator's high-water mark: the next allocation
// stays above every nonce this process has ever handed out.
func (c *NonceCoordinator) Reconcile(ctx context.Context, sender identifiers.Address) (NonceReconcile, error) {
	s := c.sender(sender)
	s.mu.Lock()
	defer s.mu.Unlock()

	report := NonceReconcile{}
	if !s.seeded {
		return report, nil
	}
	nonce, err := c.net.PendingNonceAt(ctx, sender)
	if err != nil {
		return NonceReconcile{}, err
	}
	if nonce > s.chainNext {
		s.chainNext = nonce
	}
	report.ChainNext = s.chainNext

	for n, state := range s.leases {
		switch {
		case state == NonceSubmitted && n < s.chainNext:
			report.Consumed = append(report.Consumed, n)
		case state == NonceSubmitted:
			report.InFlight = append(report.InFlight, n)
		case n < s.chainNext:
			report.Foreign = append(report.Foreign, n)
		default:
			report.Open = append(report.Open, n)
		}
	}
	sort.Slice(report.Open, func(i, j int) bool { return report.Open[i] < report.Open[j] })
	sort.Slice(report.InFlight, func(i, j int) bool { return report.InFlight[i] < report.InFlight[j] })
	sort.Slice(report.Consumed, func(i, j int) bool { return report.Consumed[i] < report.Consumed[j] })
	sort.Slice(report.Foreign, func(i, j int) bool { return report.Foreign[i] < report.Foreign[j] })
	return report, nil
}

// Snapshot summarizes one account's allocations. An untouched account
// reports unseeded with zero counters.
func (c *NonceCoordinator) Snapshot(sender identifiers.Address) NonceSnapshot {
	s := c.sender(sender)
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := NonceSnapshot{
		Seeded:    s.seeded,
		Next:      s.next,
		ChainNext: s.chainNext,
	}
	for _, state := range s.leases {
		switch state {
		case NonceAllocated:
			snap.Allocated++
		case NonceSubmitted:
			snap.Submitted++
		}
	}
	return snap
}

// sender returns the ledger for one account, creating it on first use.
func (c *NonceCoordinator) sender(addr identifiers.Address) *senderNonces {
	key := addr.Value
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.bySender[key]
	if !ok {
		s = &senderNonces{leases: map[uint64]NonceState{}}
		c.bySender[key] = s
	}
	return s
}

// take hands out the lowest reusable nonce, or the next fresh one.
func (s *senderNonces) take() uint64 {
	if len(s.free) > 0 {
		lowest := uint64(0)
		first := true
		for n := range s.free {
			if first || n < lowest {
				lowest = n
				first = false
			}
		}
		delete(s.free, lowest)
		s.leases[lowest] = NonceAllocated
		return lowest
	}
	n := s.next
	s.next++
	s.leases[n] = NonceAllocated
	return n
}

// unknownNonce builds the classified error for a state change on a nonce
// with no open allocation.
func (c *NonceCoordinator) unknownNonce(sender identifiers.Address, nonce uint64) error {
	return of(cherrs.InvalidArgument, c.net.opBase+".nonce", c.net.id.String(),
		fmt.Errorf("%w: nonce %d for %s", ErrUnknownNonce, nonce, sender.Value))
}
