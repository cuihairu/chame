package txload

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/chame/adapters/evm"
	"github.com/cuihairu/chame/core/capability"
	"github.com/cuihairu/chame/core/identifiers"
	"github.com/cuihairu/chame/core/module"
	"github.com/cuihairu/chame/tools/txload/devchain"
)

// TestPoolFailoverCounter isolates the adapter's endpoint pool: taking
// the current endpoint down must fail over and raise Stats.Failovers.
func TestPoolFailoverCounter(t *testing.T) {
	chain := devchain.NewChain(1337, 20*time.Millisecond)
	defer chain.Stop()
	var urls []string
	var nodes []*devchain.Node
	for i := 0; i < 2; i++ {
		node := chain.NewNode(devchain.Faults{})
		srv := &http.Server{Handler: node}
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		go func() { _ = srv.Serve(ln) }()
		t.Cleanup(func() { _ = srv.Close() })
		urls = append(urls, "http://"+ln.Addr().String())
		nodes = append(nodes, node)
	}
	mod := evm.New(evm.Config{Networks: []evm.NetworkConfig{{
		Name:           "pooltest",
		ChainID:        1337,
		Endpoints:      urls,
		RequestTimeout: time.Second,
	}}})
	store := module.NewStore()
	reg := module.NewRegistry(store, "test")
	if err := mod.Register(reg); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx := context.Background()
	if err := mod.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	adapterAny, _ := store.Capability(capability.Network)
	net0, _ := adapterAny.(*evm.Adapter).Network(identifiers.NetworkID{
		Family: identifiers.FamilyEVM, Name: "pooltest", ChainID: "1337",
	})
	for i := 0; i < 5; i++ {
		if _, err := net0.BlockNumber(ctx); err != nil {
			t.Fatalf("block number: %v", err)
		}
	}
	fmt.Printf("failovers before down: %d\n", net0.Stats().Failovers)
	if err := nodes[0].SetFaults(devchain.Faults{Down: true}); err != nil {
		t.Fatalf("down: %v", err)
	}
	// Calls now fail over to the healthy endpoint; tolerate the very
	// first attempts under slow CI machines, but the pool must recover
	// and record the failover.
	for i := 0; i < 10; i++ {
		_, err := net0.BlockNumber(ctx)
		if err == nil {
			break
		}
		if i >= 2 {
			t.Fatalf("block number after failover: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	got := net0.Stats().Failovers
	fmt.Printf("failovers after down: %d\n", got)
	if got == 0 {
		t.Fatal("pool recorded no failover with the current endpoint down")
	}
}
