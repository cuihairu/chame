package evm

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/cuihairu/chame/core/identifiers"
)

// nonceAddr is the sender the coordinator tests allocate for.
var nonceAddr = mustEVMAddress("0x00000000000000000000000000000000000000aa")

// mustEVMAddress builds the package-level fixture; test-time addresses
// use evmAddr so failures report through t.
func mustEVMAddress(s string) identifiers.Address {
	a, err := identifiers.NewEVMAddress(s)
	if err != nil {
		panic(err)
	}
	return a
}

// seededCoordinator returns a coordinator over a fake whose account
// already has on-chain history, mirroring a live account.
func seededCoordinator(t *testing.T, chainNext uint64) (*NonceCoordinator, *fakeClient) {
	t.Helper()
	fake := newFakeClient(1)
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), chainNext)
	return NewNonceCoordinator(testNetwork(t, fake)), fake
}

func TestNonceNextSeedsFromChain(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	first, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	second, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if first != 7 || second != 8 {
		t.Errorf("nonces = %d, %d; want 7, 8", first, second)
	}
	if fake.callCount("pending_nonce_at") != 1 {
		t.Errorf("chain queries = %d, want 1 (seed once, then allocate locally)", fake.callCount("pending_nonce_at"))
	}
	queried := fake.capturedNonces()
	if len(queried) != 1 || queried[0] != common.HexToAddress(nonceAddr.Value) {
		t.Errorf("queried addresses = %v, want exactly the sender", queried)
	}
}

func TestNonceNextConcurrentAllocations(t *testing.T) {
	const goroutines = 50
	c, _ := seededCoordinator(t, 100)

	var mu sync.Mutex
	got := map[uint64]bool{}
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := c.Next(context.Background(), nonceAddr)
			if err != nil {
				t.Errorf("Next: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if got[n] {
				t.Errorf("nonce %d allocated twice", n)
			}
			got[n] = true
		}()
	}
	wg.Wait()
	if len(got) != goroutines {
		t.Fatalf("distinct nonces = %d, want %d", len(got), goroutines)
	}
	for n := uint64(100); n < 100+goroutines; n++ {
		if !got[n] {
			t.Errorf("nonce %d never allocated; the window must be contiguous", n)
		}
	}
}

func TestNonceIsolatedPerSender(t *testing.T) {
	fake := newFakeClient(1)
	other := evmAddr(t, "0x00000000000000000000000000000000000000bb")
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 10)
	fake.withPendingNonce(common.HexToAddress(other.Value), 900)
	c := NewNonceCoordinator(testNetwork(t, fake))

	a, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next a: %v", err)
	}
	b, err := c.Next(context.Background(), other)
	if err != nil {
		t.Fatalf("Next b: %v", err)
	}
	a2, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next a2: %v", err)
	}
	if a != 10 || b != 900 || a2 != 11 {
		t.Errorf("nonces = %d, %d, %d; want 10, 900, 11", a, b, a2)
	}
}

func TestNonceAbandonRefillsTheGap(t *testing.T) {
	c, _ := seededCoordinator(t, 7)

	first, _ := c.Next(context.Background(), nonceAddr)
	if err := c.Abandon(nonceAddr, first); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	again, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after abandon: %v", err)
	}
	fresh, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after refill: %v", err)
	}
	if again != first {
		t.Errorf("reused nonce = %d, want the abandoned %d", again, first)
	}
	if fresh != 8 {
		t.Errorf("fresh nonce = %d, want 8", fresh)
	}
}

func TestNonceSubmittedNeverRefilled(t *testing.T) {
	c, _ := seededCoordinator(t, 7)

	first, _ := c.Next(context.Background(), nonceAddr)
	if err := c.Submitted(nonceAddr, first); err != nil {
		t.Fatalf("Submitted: %v", err)
	}
	next, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if next != 8 {
		t.Errorf("nonce after submit = %d, want 8 (a broadcast nonce is never handed out again)", next)
	}
}

func TestNonceRejectsUnknownLeases(t *testing.T) {
	c, _ := seededCoordinator(t, 7)

	if err := c.Abandon(nonceAddr, 42); !errors.Is(err, ErrUnknownNonce) {
		t.Errorf("Abandon of unknown nonce: err = %v, want unknown nonce", err)
	}
	n, _ := c.Next(context.Background(), nonceAddr)
	if err := c.Submitted(nonceAddr, n); err != nil {
		t.Fatalf("Submitted: %v", err)
	}
	if err := c.Submitted(nonceAddr, n); !errors.Is(err, ErrUnknownNonce) {
		t.Errorf("double Submitted: err = %v, want unknown nonce (no open allocation)", err)
	}
	if err := c.Abandon(nonceAddr, n); !errors.Is(err, ErrUnknownNonce) {
		t.Errorf("Abandon of submitted nonce: err = %v, want unknown nonce", err)
	}
}

func TestNonceQueryFailureKeepsState(t *testing.T) {
	fake := newFakeClient(1)
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 7)
	fake.fail("pending_nonce_at", &net.OpError{Op: "dial", Err: errors.New("refused")})
	c := NewNonceCoordinator(testNetwork(t, fake))

	if _, err := c.Next(context.Background(), nonceAddr); err == nil {
		t.Fatal("Next over a dead endpoint: err = nil, want error")
	}
	// §7.5: a failed seed must not poison the account — the retry starts
	// clean, and the failure classified as network trouble.
	if fake.callCount("pending_nonce_at") != 1 {
		t.Errorf("chain queries = %d, want 1", fake.callCount("pending_nonce_at"))
	}

	fake.errByMethod = map[string]error{} // endpoint recovers
	n, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after recovery: %v", err)
	}
	if n != 7 {
		t.Errorf("nonce after recovery = %d, want 7", n)
	}
}

func TestNonceReconcileClassifiesLeases(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	// Four leases, one per classification.
	seven, _ := c.Next(context.Background(), nonceAddr) // never broadcast; the chain consumed it anyway
	eight, _ := c.Next(context.Background(), nonceAddr) // submitted, the chain consumed it
	nine, _ := c.Next(context.Background(), nonceAddr)  // submitted, the chain has not
	open, _ := c.Next(context.Background(), nonceAddr)  // allocated, ours to submit
	if seven != 7 || eight != 8 || nine != 9 || open != 10 {
		t.Fatalf("nonces = %d, %d, %d, %d; want 7, 8, 9, 10", seven, eight, nine, open)
	}
	if err := c.Submitted(nonceAddr, eight); err != nil {
		t.Fatalf("Submitted: %v", err)
	}
	if err := c.Submitted(nonceAddr, nine); err != nil {
		t.Fatalf("Submitted: %v", err)
	}

	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 9)
	report, err := c.Reconcile(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.ChainNext != 9 {
		t.Errorf("chain next = %d, want 9", report.ChainNext)
	}
	if len(report.Consumed) != 1 || report.Consumed[0] != 8 {
		t.Errorf("consumed = %v, want [8]", report.Consumed)
	}
	if len(report.InFlight) != 1 || report.InFlight[0] != 9 {
		t.Errorf("in flight = %v, want [9] (replay or bump candidates)", report.InFlight)
	}
	if len(report.Open) != 1 || report.Open[0] != 10 {
		t.Errorf("open = %v, want [10]", report.Open)
	}
	if len(report.Foreign) != 1 || report.Foreign[0] != 7 {
		t.Errorf("foreign = %v, want [7] (consumed without our broadcast)", report.Foreign)
	}
}

func TestNonceReconcileReportsForeignConsumption(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	seven, _ := c.Next(context.Background(), nonceAddr) // we never broadcast it
	if seven != 7 {
		t.Fatalf("nonce = %d, want 7", seven)
	}
	// Another process or a manual send consumed 7 and 8.
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 9)
	report, err := c.Reconcile(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(report.Foreign) != 1 || report.Foreign[0] != 7 {
		t.Errorf("foreign = %v, want [7]", report.Foreign)
	}
	if report.ChainNext != 9 {
		t.Errorf("chain next = %d, want 9", report.ChainNext)
	}
}

func TestNonceReconcileNeverHandsOutStaleNonce(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	// We planned to hand out 7 but never broadcast it.
	n, _ := c.Next(context.Background(), nonceAddr)
	if n != 7 {
		t.Fatalf("nonce = %d, want 7", n)
	}
	// An external writer consumed 7..9; the chain now points at 10.
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 10)
	if _, err := c.Reconcile(context.Background(), nonceAddr); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	next, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after reconcile: %v", err)
	}
	// The stale allocated 7 must never be handed out again; the fresh
	// allocation resumes at the chain's high-water mark.
	if next != 10 {
		t.Errorf("nonce after external advance = %d, want 10 (no stale nonce below chainNext)", next)
	}
}

func TestNonceReconcileDropsStaleAbandonedNonce(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	seven, _ := c.Next(context.Background(), nonceAddr)
	if seven != 7 {
		t.Fatalf("nonce = %d, want 7", seven)
	}
	// The build failed before broadcast; the nonce goes back to the pool.
	if err := c.Abandon(nonceAddr, seven); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	// An external writer consumed 7 and 8.
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 9)
	if _, err := c.Reconcile(context.Background(), nonceAddr); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	next, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after reconcile: %v", err)
	}
	// The abandoned nonce 7 sits below the chain and must be dropped from
	// the free pool, not reused.
	if next != 9 {
		t.Errorf("nonce after reconcile = %d, want 9 (abandoned nonce below chainNext is not reused)", next)
	}
}

func TestNonceReconcileForeignLeaseNotReusable(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	// We planned to hand out 7 but never broadcast it. An external writer
	// consumed it, so Reconcile classifies it Foreign and retires the lease.
	seven, _ := c.Next(context.Background(), nonceAddr)
	if seven != 7 {
		t.Fatalf("nonce = %d, want 7", seven)
	}
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 8)
	report, err := c.Reconcile(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(report.Foreign) != 1 || report.Foreign[0] != 7 {
		t.Fatalf("foreign = %v, want [7]", report.Foreign)
	}

	// The build that never broadcast it later fails and abandons it. The
	// chain already consumed 7, so Abandon must refuse — the lease is gone,
	// and 7 must never re-enter the free pool.
	if err := c.Abandon(nonceAddr, seven); !errors.Is(err, ErrUnknownNonce) {
		t.Errorf("Abandon after foreign reconcile: err = %v, want unknown nonce", err)
	}
	next, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after foreign reconcile: %v", err)
	}
	if next != 8 {
		t.Errorf("nonce after foreign reconcile = %d, want 8 (consumed nonce never reused)", next)
	}
}

func TestNonceReconcileNeverRegresses(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	n, _ := c.Next(context.Background(), nonceAddr) // 7 allocated
	if n != 7 {
		t.Fatalf("nonce = %d, want 7", n)
	}
	// A reorg or a lagging endpoint reports the account back at 3.
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 3)
	report, err := c.Reconcile(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if report.ChainNext != 7 {
		t.Errorf("chain next = %d, want the kept high-water 7 (never blindly reset)", report.ChainNext)
	}
	next, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if next != 8 {
		t.Errorf("nonce after regress = %d, want 8", next)
	}
}

func TestNonceReconcileQueryFailureChangesNothing(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	n, _ := c.Next(context.Background(), nonceAddr)
	if n != 7 {
		t.Fatalf("nonce = %d, want 7", n)
	}
	fake.fail("pending_nonce_at", &net.OpError{Op: "read", Err: errors.New("reset")})
	if _, err := c.Reconcile(context.Background(), nonceAddr); err == nil {
		t.Fatal("Reconcile over a dead endpoint: err = nil, want error")
	}
	next, err := c.Next(context.Background(), nonceAddr)
	if err != nil {
		t.Fatalf("Next after failed reconcile: %v", err)
	}
	if next != 8 {
		t.Errorf("nonce = %d, want 8 (a failed reconcile must not reset anything)", next)
	}
}

func TestNonceSnapshotSummarizesLeases(t *testing.T) {
	c, fake := seededCoordinator(t, 7)

	a, _ := c.Next(context.Background(), nonceAddr) // consumed by another writer
	b, _ := c.Next(context.Background(), nonceAddr) // submitted
	d, _ := c.Next(context.Background(), nonceAddr) // abandoned
	_ = a
	if err := c.Submitted(nonceAddr, b); err != nil {
		t.Fatalf("Submitted: %v", err)
	}
	if err := c.Abandon(nonceAddr, d); err != nil {
		t.Fatalf("Abandon: %v", err)
	}
	fake.withPendingNonce(common.HexToAddress(nonceAddr.Value), 8)
	if _, err := c.Reconcile(context.Background(), nonceAddr); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	snap := c.Snapshot(nonceAddr)
	if snap.Seeded != true || snap.Allocated != 0 || snap.Submitted != 1 {
		t.Errorf("snapshot = %+v, want seeded with one submitted lease (the foreign lease is retired)", snap)
	}
	if snap.Next != 10 {
		t.Errorf("next = %d, want 10", snap.Next)
	}
	if snap.ChainNext != 8 {
		t.Errorf("chain next = %d, want 8", snap.ChainNext)
	}
}

func TestNonceSnapshotUnseededAccount(t *testing.T) {
	c, _ := seededCoordinator(t, 7)
	other := evmAddr(t, "0x00000000000000000000000000000000000000bb")
	snap := c.Snapshot(other)
	if snap.Seeded {
		t.Error("snapshot of an untouched account reports seeded")
	}
	if snap.Next != 0 || snap.Allocated != 0 || snap.Submitted != 0 {
		t.Errorf("snapshot = %+v, want all zeros", snap)
	}
}
