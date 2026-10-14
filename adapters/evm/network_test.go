package evm

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/health"
	"github.com/cuihairu/chame/core/identifiers"
)

func evmAddr(t *testing.T, s string) identifiers.Address {
	t.Helper()
	a, err := identifiers.NewEVMAddress(s)
	if err != nil {
		t.Fatalf("bad test address %q: %v", s, err)
	}
	return a
}

func solAddr(t *testing.T, s string) identifiers.Address {
	t.Helper()
	a, err := identifiers.NewSolanaAddress(s)
	if err != nil {
		t.Fatalf("bad test address %q: %v", s, err)
	}
	return a
}

func testNetwork(t *testing.T, fake *fakeClient) *Network {
	t.Helper()
	cfg := NetworkConfig{Name: "mainnet", ChainID: 1, Endpoints: []string{"https://a.example"}}
	return newNetwork(cfg, []rpcClient{fake})
}

func TestNetworkBalanceAt(t *testing.T) {
	fake := newFakeClient(1)
	fake.balance = big.NewInt(1000)
	n := testNetwork(t, fake)
	holder := evmAddr(t, "0x00000000000000000000000000000000000000aa")
	got, err := n.BalanceAt(context.Background(), holder)
	if err != nil {
		t.Fatalf("BalanceAt: %v", err)
	}
	if got.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("balance = %s, want 1000", got)
	}
	captured := fake.capturedBalances()
	if len(captured) != 1 || captured[0] != common.HexToAddress(holder.Value) {
		t.Errorf("captured address = %v", captured)
	}
}

func TestNetworkBalanceRejectsForeignFamily(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	_, err := n.BalanceAt(context.Background(), solAddr(t, "11111111111111111111111111111111"))
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
	if fake.callCount("balance_at") != 0 {
		t.Error("foreign-family address must not reach the endpoint")
	}
}

func TestNetworkBalanceRejectsMalformedAddress(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	// A family-correct but malformed value: construction would reject
	// it, so build the struct directly.
	bad := identifiers.Address{Family: identifiers.FamilyEVM, Value: "0x1234"}
	_, err := n.BalanceAt(context.Background(), bad)
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
	if fake.callCount("balance_at") != 0 {
		t.Error("malformed address must not reach the endpoint")
	}
}

func TestNetworkTokenBalanceEncodesBalanceOf(t *testing.T) {
	fake := newFakeClient(1)
	fake.callReply = make([]byte, 32)
	fake.callReply[31] = 0x2a // 42
	n := testNetwork(t, fake)
	token := evmAddr(t, "0x0000000000000000000000000000000000000001")
	holder := evmAddr(t, "0x0000000000000000000000000000000000000002")
	got, err := n.TokenBalance(context.Background(), token, holder)
	if err != nil {
		t.Fatalf("TokenBalance: %v", err)
	}
	if got.Cmp(big.NewInt(42)) != 0 {
		t.Errorf("balance = %s, want 42", got)
	}
	calls := fake.capturedCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	want := make([]byte, 36)
	want[0], want[1], want[2], want[3] = 0x70, 0xa0, 0x82, 0x31
	want[35] = 0x02 // holder left-padded
	gotData := calls[0].Data
	if len(gotData) != len(want) {
		t.Fatalf("calldata = %x, want %x", gotData, want)
	}
	for i := range want {
		if gotData[i] != want[i] {
			t.Fatalf("calldata = %x, want %x", gotData, want)
		}
	}
	if calls[0].To == nil || calls[0].To.Hex() != "0x0000000000000000000000000000000000000001" {
		t.Errorf("call target = %v", calls[0].To)
	}
}

func TestNetworkTokenBalanceMalformedReply(t *testing.T) {
	fake := newFakeClient(1)
	fake.callReply = []byte{1, 2, 3}
	n := testNetwork(t, fake)
	token := evmAddr(t, "0x0000000000000000000000000000000000000001")
	holder := evmAddr(t, "0x0000000000000000000000000000000000000002")
	_, err := n.TokenBalance(context.Background(), token, holder)
	if !errors.Is(err, cherrs.ErrEventDecodeFailure) {
		t.Fatalf("err = %v, want decode failure", err)
	}
}

func TestNetworkCall(t *testing.T) {
	fake := newFakeClient(1)
	fake.callReply = []byte{0xde, 0xad}
	n := testNetwork(t, fake)
	out, err := n.Call(context.Background(), CallQuery{
		To:   evmAddr(t, "0x0000000000000000000000000000000000000003"),
		From: evmAddr(t, "0x0000000000000000000000000000000000000004"),
		Data: []byte{0x01, 0x02},
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if len(out) != 2 || out[0] != 0xde {
		t.Errorf("out = %x", out)
	}
	calls := fake.capturedCalls()
	if len(calls) != 1 || calls[0].From.Hex() == "" || len(calls[0].Data) != 2 {
		t.Errorf("captured call = %+v", calls)
	}
}

func TestNetworkLogs(t *testing.T) {
	fake := newFakeClient(1)
	emitter := common.HexToAddress("0x00000000000000000000000000000000000000aa")
	t0, t1 := common.HexToHash("0x"+strings.Repeat("ab", 32)), common.HexToHash("0x"+strings.Repeat("cd", 32))
	fake.logs = []types.Log{{
		BlockNumber: 5,
		TxHash:      common.HexToHash("0xccc"),
		Index:       3,
		Address:     emitter,
		Topics:      []common.Hash{t0, t1},
		Data:        []byte{1, 2},
	}}
	n := testNetwork(t, fake)
	to := uint64(9)
	recs, err := n.Logs(context.Background(), LogFilter{
		FromBlock: 1,
		ToBlock:   &to,
		Addresses: []identifiers.Address{evmAddr(t, "0x00000000000000000000000000000000000000aa")},
		Topics:    [][]string{{t0.Hex()}, nil},
	})
	if err != nil {
		t.Fatalf("Logs: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	r := recs[0]
	if r.BlockNumber != 5 || r.LogIndex != 3 || r.TxHash != "0x0000000000000000000000000000000000000000000000000000000000000ccc" {
		t.Errorf("record = %+v", r)
	}
	if r.Address.Family != identifiers.FamilyEVM || r.Address.Value == "" {
		t.Errorf("record address = %+v", r.Address)
	}
	if len(r.Topics) != 2 || r.Topics[0] != t0.Hex() || r.Topics[1] != t1.Hex() {
		t.Errorf("topics = %v", r.Topics)
	}
	q := fake.capturedQueries()
	if len(q) != 1 {
		t.Fatalf("queries = %d", len(q))
	}
	if q[0].FromBlock.Cmp(big.NewInt(1)) != 0 || q[0].ToBlock.Cmp(big.NewInt(9)) != 0 {
		t.Errorf("query blocks = %v..%v", q[0].FromBlock, q[0].ToBlock)
	}
	if len(q[0].Topics) != 2 || len(q[0].Topics[1]) != 0 {
		t.Errorf("query topics = %v", q[0].Topics)
	}
}

func TestNetworkLogsRejectBadTopic(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	_, err := n.Logs(context.Background(), LogFilter{Topics: [][]string{{"0x12"}}})
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
	if fake.callCount("filter_logs") != 0 {
		t.Error("bad topic must not reach the endpoint")
	}
}

func TestNetworkBlockQueries(t *testing.T) {
	fake := newFakeClient(1)
	fake.blockNumber = 99
	fake.header = &types.Header{Number: big.NewInt(99), Time: 1700000000}
	n := testNetwork(t, fake)
	num, err := n.BlockNumber(context.Background())
	if err != nil || num != 99 {
		t.Fatalf("BlockNumber = %d, %v", num, err)
	}
	blk, err := n.BlockByNumber(context.Background(), 99)
	if err != nil {
		t.Fatalf("BlockByNumber: %v", err)
	}
	if blk.Number != 99 || blk.TimeSec != 1700000000 || blk.Hash != fake.header.Hash().Hex() {
		t.Errorf("block = %+v", blk)
	}
}

func TestNetworkHealthyVerifiesIdentity(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	if err := n.Healthy(context.Background()); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	fake.chainID = big.NewInt(999)
	err := n.Healthy(context.Background())
	if !errors.Is(err, cherrs.ErrInvalidConfiguration) {
		t.Fatalf("err = %v, want invalid configuration", err)
	}
}

func TestNetworkCheckHealthBeforeStart(t *testing.T) {
	cfg := NetworkConfig{Name: "mainnet", ChainID: 1, Endpoints: []string{"https://a.example"}}
	n := newNetwork(cfg, nil)
	status, err := n.CheckHealth(context.Background())
	if status != health.Unhealthy || err == nil {
		t.Fatalf("status = %v, err = %v, want unhealthy before start", status, err)
	}
}

func TestNetworkTimeoutClassified(t *testing.T) {
	fake := &blockingClient{fakeClient: newFakeClient(1)}
	cfg := NetworkConfig{Name: "mainnet", ChainID: 1, Endpoints: []string{"https://a.example"}, RequestTimeout: 5 * time.Millisecond}
	n := newNetwork(cfg, []rpcClient{fake})
	_, err := n.ChainID(context.Background())
	if !errors.Is(err, cherrs.ErrRequestTimeout) {
		t.Fatalf("err = %v, want request timeout", err)
	}
	if n.Stats().Errors != 1 {
		t.Errorf("errors = %d, want 1", n.Stats().Errors)
	}
}

func TestNetworkStatsCounters(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	if _, err := n.ChainID(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.fail("block_number", netErr{msg: "refused"})
	if _, err := n.BlockNumber(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	stats := n.Stats()
	if stats.Requests != 2 || stats.Errors != 1 {
		t.Errorf("stats = %+v, want 2 requests 1 error", stats)
	}
}
