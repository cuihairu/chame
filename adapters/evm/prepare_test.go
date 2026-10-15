package evm

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/signer"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

const (
	prepareSender = "0x00000000000000000000000000000000000000cc"
	storageKeyHex = "0x290decd9548b62a8d60345a988386fc84ba6bc95484008f6362f93160ef3e563"
)

func buildQuery() BuildQuery {
	return BuildQuery{
		Sender:   mustEVMAddress(prepareSender),
		To:       mustEVMAddress(txReceiver),
		Value:    big.NewInt(1000),
		Data:     []byte{0xa9, 0x05, 0x9c, 0xbb},
		GasLimit: 21000,
		GasPrice: big.NewInt(20_000_000_000),
		Nonce:    uint64Ptr(5),
	}
}

// uint64Ptr exists so one literal can fill the optional nonce field.
func uint64Ptr(v uint64) *uint64 { return &v }

func decodeBuiltTx(t *testing.T, payload []byte) types.Transaction {
	t.Helper()
	var tx types.Transaction
	if err := tx.UnmarshalBinary(payload); err != nil {
		t.Fatalf("built payload is not an EVM transaction: %v", err)
	}
	if v, r, s := tx.RawSignatureValues(); v != nil && v.Sign() != 0 || r != nil && r.Sign() != 0 || s != nil && s.Sign() != 0 {
		t.Error("built payload already carries a signature")
	}
	return tx
}

// TestNetworkBuildsLegacyTransaction covers every field spec §7.6 lists
// for a legacy build: explicit chain id, sender, recipient, value,
// calldata, gas limit, gas price, nonce, idempotency key.
func TestNetworkBuildsLegacyTransaction(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.IdempotencyKey = "order-42"

	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.Format != signer.FormatEIP155 {
		t.Errorf("format = %s, want eip-155", built.Format)
	}
	if built.Nonce != 5 || built.GasLimit != 21000 || built.GasPrice.Uint64() != 20_000_000_000 {
		t.Errorf("built summary = nonce %d gas %d price %s, want 5/21000/20gwei", built.Nonce, built.GasLimit, built.GasPrice)
	}
	if built.IdempotencyKey != "order-42" {
		t.Errorf("idempotency key = %q, want order-42", built.IdempotencyKey)
	}
	tx := decodeBuiltTx(t, built.Payload)
	if tx.Type() != types.LegacyTxType {
		t.Errorf("tx type = %d, want legacy", tx.Type())
	}
	if tx.Nonce() != 5 || tx.Gas() != 21000 || tx.GasPrice().Cmp(big.NewInt(20_000_000_000)) != 0 {
		t.Errorf("legacy fields = nonce %d gas %d price %s", tx.Nonce(), tx.Gas(), tx.GasPrice())
	}
	if tx.Value().Cmp(big.NewInt(1000)) != 0 || string(tx.Data()) != "\xa9\x05\x9c\xbb" {
		t.Errorf("value/data = %s/%x", tx.Value(), tx.Data())
	}
	if tx.To() == nil || tx.To().Hex() != txReceiver {
		t.Errorf("to = %v, want %s", tx.To(), txReceiver)
	}
	// An explicit gas and fee must not reach the node for anything.
	for _, method := range []string{"estimate_gas", "suggest_gas_price", "suggest_gas_tip_cap"} {
		if fake.callCount(method) != 0 {
			t.Errorf("chain queries = %d, want 0 for %s", fake.callCount(method), method)
		}
	}
}

// TestNetworkBuildsDynamicFeeTransaction covers the EIP-1559 fields and
// the optional access list.
func TestNetworkBuildsDynamicFeeTransaction(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasPrice = nil
	q.GasLimit = 100_000
	q.MaxFeePerGas = big.NewInt(50_000_000_000)
	q.MaxPriorityFeePerGas = big.NewInt(2_000_000_000)
	q.AccessList = []AccessListEntry{{
		To:  mustEVMAddress("0x00000000000000000000000000000000000000ee"),
		Key: storageKeyHex,
	}}

	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.Format != signer.FormatEIP1559 {
		t.Errorf("format = %s, want eip-1559", built.Format)
	}
	tx := decodeBuiltTx(t, built.Payload)
	if tx.Type() != types.DynamicFeeTxType {
		t.Fatalf("tx type = %d, want dynamic fee", tx.Type())
	}
	if tx.ChainId().Uint64() != 1 {
		t.Errorf("chain id = %s, want the network chain id 1", tx.ChainId())
	}
	if tx.GasFeeCap().Cmp(big.NewInt(50_000_000_000)) != 0 || tx.GasTipCap().Cmp(big.NewInt(2_000_000_000)) != 0 {
		t.Errorf("fee caps = %s/%s, want 50gwei/2gwei", tx.GasFeeCap(), tx.GasTipCap())
	}
	if len(tx.AccessList()) != 1 {
		t.Fatalf("access list = %v, want one entry", tx.AccessList())
	}
	if tx.AccessList()[0].Address.Hex() != "0x00000000000000000000000000000000000000ee" {
		t.Errorf("access list address = %s", tx.AccessList()[0].Address.Hex())
	}
	if len(tx.AccessList()[0].StorageKeys) != 1 || tx.AccessList()[0].StorageKeys[0].Hex() != storageKeyHex {
		t.Errorf("access list key = %v", tx.AccessList()[0].StorageKeys)
	}
}

// TestNetworkBuildDefaultsPriorityFee caps the priority fee at the max fee
// when the caller names only the ceiling.
func TestNetworkBuildDefaultsPriorityFee(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasPrice = nil
	q.MaxFeePerGas = big.NewInt(50_000_000_000)
	q.MaxPriorityFeePerGas = nil

	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	tx := decodeBuiltTx(t, built.Payload)
	if tx.GasTipCap().Cmp(tx.GasFeeCap()) != 0 {
		t.Errorf("priority fee = %s, want the max fee %s", tx.GasTipCap(), tx.GasFeeCap())
	}
	// The ceiling alone is enough: no fee probe happens.
	if fake.callCount("suggest_gas_price")+fake.callCount("suggest_gas_tip_cap") != 0 {
		t.Error("a fully specified fee must not probe the node")
	}
}

// TestNetworkBuildEstimatesGas fills the gas limit from the node when the
// caller leaves it unset.
func TestNetworkBuildEstimatesGas(t *testing.T) {
	fake := newFakeClient(1)
	fake.withEstimateGas(88_000)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasLimit = 0

	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.GasLimit != 88_000 {
		t.Errorf("gas limit = %d, want the estimate 88000", built.GasLimit)
	}
	queries := fake.capturedEstimates()
	if len(queries) != 1 {
		t.Fatalf("estimate calls = %d, want 1", len(queries))
	}
	// The estimate names the same sender, recipient, value and calldata,
	// and carries no gas limit: the node is what measures.
	call := queries[0]
	if call.Gas != 0 {
		t.Errorf("estimate gas = %d, want 0 (measure, not cap)", call.Gas)
	}
	if call.From.Hex() != prepareSender {
		t.Errorf("estimate from = %s, want the sender", call.From.Hex())
	}
	if call.To == nil || call.To.Hex() != txReceiver {
		t.Errorf("estimate to = %v, want the recipient", call.To)
	}
	if call.Value == nil || call.Value.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("estimate value = %s, want 1000", call.Value)
	}
	if string(call.Data) != "\xa9\x05\x9c\xbb" {
		t.Errorf("estimate data = %x", call.Data)
	}
}

// TestNetworkBuildAllocatesNonce seeds the coordinator from the chain and
// hands out one nonce per build, without a second chain query per build.
func TestNetworkBuildAllocatesNonce(t *testing.T) {
	fake := newFakeClient(1)
	fake.withPendingNonce(mustEVMAddress(prepareSender), 7)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.Nonce = nil

	for i, want := range []uint64{7, 8} {
		built, err := n.Build(context.Background(), q)
		if err != nil {
			t.Fatalf("build %d: %v", i, err)
		}
		if built.Nonce != want {
			t.Errorf("build %d: nonce = %d, want %d", i, built.Nonce, want)
		}
	}
	if got := fake.capturedNonces(); len(got) != 1 {
		t.Errorf("chain nonce queries = %d, want one seed for the whole account", len(got))
	}
	snap := n.NonceCoordinator().Snapshot(mustEVMAddress(prepareSender))
	if snap.Next != 9 || snap.Allocated != 2 {
		t.Errorf("snapshot = %+v, want next 9 with 2 open allocations", snap)
	}
}

// TestNetworkBuildAbandonsNonceOnFailure returns a nonce the build could
// not spend: a failed estimate must not leave a hole the account waits
// out, and the next build reuses it.
func TestNetworkBuildAbandonsNonceOnFailure(t *testing.T) {
	fake := newFakeClient(1)
	fake.withPendingNonce(mustEVMAddress(prepareSender), 7)
	fake.fail("estimate_gas", netErr{msg: "execution reverted"})
	n := testNetwork(t, fake)
	q := buildQuery()
	q.Nonce = nil
	q.GasLimit = 0

	sender := mustEVMAddress(prepareSender)
	if _, err := n.Build(context.Background(), q); err == nil {
		t.Fatal("Build over a failing estimate: err = nil, want error")
	}
	snap := n.NonceCoordinator().Snapshot(sender)
	if snap.Allocated != 0 || snap.Next != 8 {
		t.Errorf("snapshot = %+v, want the nonce back in the pool", snap)
	}
	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if built.Nonce != 7 {
		t.Errorf("retry nonce = %d, want the abandoned 7 back", built.Nonce)
	}
}

// TestNetworkBuildProbesFeesForDynamicFeeNetwork asks the node which fee
// mechanism it supports and answers with EIP-1559 when the node can name
// a priority fee.
func TestNetworkBuildProbesFeesForDynamicFeeNetwork(t *testing.T) {
	fake := newFakeClient(1)
	fake.withGasPrice(big.NewInt(10_000_000_000))
	fake.withGasTipCap(big.NewInt(1_000_000_000))
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasPrice = nil
	q.MaxFeePerGas = nil
	q.MaxPriorityFeePerGas = nil

	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.Format != signer.FormatEIP1559 {
		t.Fatalf("format = %s, want eip-1559", built.Format)
	}
	tx := decodeBuiltTx(t, built.Payload)
	if tx.GasFeeCap().Cmp(big.NewInt(10_000_000_000)) != 0 {
		t.Errorf("max fee = %s, want the node's current gas price", tx.GasFeeCap())
	}
	if tx.GasTipCap().Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("priority fee = %s, want the suggested tip", tx.GasTipCap())
	}
}

// TestNetworkBuildProbesFeesForLegacyNetwork is the rule spec §7.6 states:
// EIP-1559 fields must never be forced onto a network that cannot name a
// priority fee. That network answers with a legacy gas price instead.
func TestNetworkBuildProbesFeesForLegacyNetwork(t *testing.T) {
	fake := newFakeClient(1)
	fake.withGasPrice(big.NewInt(20_000_000_000))
	// The node answers no priority fee: it does not run EIP-1559.
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasPrice = nil
	q.MaxFeePerGas = nil
	q.MaxPriorityFeePerGas = nil

	built, err := n.Build(context.Background(), q)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if built.Format != signer.FormatEIP155 {
		t.Fatalf("format = %s, want eip-155", built.Format)
	}
	tx := decodeBuiltTx(t, built.Payload)
	if tx.GasPrice().Cmp(big.NewInt(20_000_000_000)) != 0 {
		t.Errorf("gas price = %s, want the node's suggestion", tx.GasPrice())
	}
}

// TestNetworkBuildRefusesBothFeeSchemes rejects a query that names two fee
// mechanisms at once rather than picking one silently.
func TestNetworkBuildRefusesBothFeeSchemes(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasPrice = big.NewInt(20_000_000_000)
	q.MaxFeePerGas = big.NewInt(50_000_000_000)

	_, err := n.Build(context.Background(), q)
	if err == nil {
		t.Fatal("Build with both fee schemes: err = nil, want error")
	}
	if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.InvalidArgument {
		t.Errorf("err = %v, want invalid argument", err)
	}
	for _, method := range []string{"pending_nonce_at", "estimate_gas", "suggest_gas_price", "suggest_gas_tip_cap"} {
		if fake.callCount(method) != 0 {
			t.Errorf("%s queries = %d, want 0", method, fake.callCount(method))
		}
	}
}

// TestNetworkBuildRefusesAccessListWithoutDynamicFee rejects an access
// list on a legacy transaction: the pre-warmed slots are an EIP-2930/1559
// feature and a legacy encoding cannot carry them.
func TestNetworkBuildRefusesAccessListWithoutDynamicFee(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.AccessList = []AccessListEntry{{To: mustEVMAddress("0x00000000000000000000000000000000000000ee"), Key: storageKeyHex}}

	_, err := n.Build(context.Background(), q)
	if err == nil {
		t.Fatal("Build with an access list on a legacy fee: err = nil, want error")
	}
	if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.InvalidArgument {
		t.Errorf("err = %v, want invalid argument", err)
	}
	for _, method := range []string{"pending_nonce_at", "estimate_gas", "suggest_gas_price", "suggest_gas_tip_cap"} {
		if fake.callCount(method) != 0 {
			t.Errorf("%s queries = %d, want 0", method, fake.callCount(method))
		}
	}
}

// TestNetworkBuildRejectsInvalidQueries covers the malformed-input rules
// before any chain call happens.
func TestNetworkBuildRejectsInvalidQueries(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*BuildQuery)
	}{
		{"no sender", func(q *BuildQuery) { q.Sender = identifiers.Address{} }},
		{"no recipient", func(q *BuildQuery) { q.To = identifiers.Address{} }},
		{"non-evm sender", func(q *BuildQuery) {
			q.Sender = identifiers.Address{Family: identifiers.FamilySolana, Value: "4Nd1mBQtrMJVYVfKf2PJy9NZUZdTAsp7D4xWLs4gDB4T"}
		}},
		{"negative value", func(q *BuildQuery) { q.Value = big.NewInt(-1) }},
		{"bad access list key", func(q *BuildQuery) {
			q.AccessList = []AccessListEntry{{To: mustEVMAddress("0x00000000000000000000000000000000000000ee"), Key: "0x1234"}}
		}},
		{"negative deadline", func(q *BuildQuery) { q.Deadline = -time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeClient(1)
			n := testNetwork(t, fake)
			q := buildQuery()
			tc.mut(&q)

			_, err := n.Build(context.Background(), q)
			if err == nil {
				t.Fatalf("Build(%s): err = nil, want error", tc.name)
			}
			if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.InvalidArgument {
				t.Errorf("Build(%s): err = %v, want invalid argument", tc.name, err)
			}
			for method, count := range fake.attempts {
				if count != 0 {
					t.Errorf("%s queries = %d, want 0", method, count)
				}
			}
		})
	}
}

// TestNetworkBuildHonorsDeadline bounds the whole build, not just one
// request: the deadline is reached while the node is still estimating.
func TestNetworkBuildHonorsDeadline(t *testing.T) {
	fake := newFakeClient(1)
	fake.withPendingNonce(mustEVMAddress(prepareSender), 7)
	fake.withSlowEstimate(50 * time.Millisecond)
	n := testNetwork(t, fake)
	q := buildQuery()
	q.Nonce = nil
	q.GasLimit = 0
	q.Deadline = 5 * time.Millisecond

	sender := mustEVMAddress(prepareSender)
	start := time.Now()
	if _, err := n.Build(context.Background(), q); err == nil {
		t.Fatal("Build past its deadline: err = nil, want error")
	} else if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.RequestTimeout {
		t.Errorf("err = %v, want request timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("build took %s, want it to stop at the deadline", elapsed)
	}
	// A build never broadcasts, so a timed-out build gives its nonce back
	// the same way a reverted estimate does.
	snap := n.NonceCoordinator().Snapshot(sender)
	if snap.Allocated != 0 || snap.Next != 8 {
		t.Errorf("snapshot = %+v, want the nonce back in the pool", snap)
	}
}

// TestNetworkBuildRefusesAccessListOnLegacyNetwork is the deferred half of
// the access-list rule: when the caller leaves the fee mechanism to the
// node, an access list is refused rather than silently dropped once the
// probe reports a legacy network.
func TestNetworkBuildRefusesAccessListOnLegacyNetwork(t *testing.T) {
	fake := newFakeClient(1)
	fake.withGasPrice(big.NewInt(20_000_000_000))
	n := testNetwork(t, fake)
	q := buildQuery()
	q.GasPrice = nil
	q.AccessList = []AccessListEntry{{To: mustEVMAddress("0x00000000000000000000000000000000000000ee"), Key: storageKeyHex}}

	_, err := n.Build(context.Background(), q)
	if err == nil {
		t.Fatal("Build with an access list on a legacy network: err = nil, want error")
	}
	if cat, ok := cherrs.CategoryOf(err); !ok || cat != cherrs.InvalidArgument {
		t.Errorf("err = %v, want invalid argument", err)
	}
	if fake.callCount("suggest_gas_tip_cap") != 1 || fake.callCount("suggest_gas_price") != 0 {
		t.Errorf("tip queries = %d, price queries = %d, want the probe to decide first",
			fake.callCount("suggest_gas_tip_cap"), fake.callCount("suggest_gas_price"))
	}
}
