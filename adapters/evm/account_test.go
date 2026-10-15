package evm

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

const weiHolder = "0x00000000000000000000000000000000000000aa"

func TestNetworkCodeAt(t *testing.T) {
	fake := newFakeClient(1)
	other := "0x0000000000000000000000000000000000000003"
	fake.withCode(common.HexToAddress(weiHolder), []byte{0x60, 0x80, 0x60, 0x04})
	fake.withCode(common.HexToAddress(other), []byte{0x60, 0x80})
	n := testNetwork(t, fake)

	if code, err := n.CodeAt(context.Background(), evmAddr(t, other)); err != nil || len(code) != 2 || code[0] != 0x60 {
		t.Fatalf("code = %x, err=%v", code, err)
	}
	if code, err := n.CodeAt(context.Background(), evmAddr(t, weiHolder)); err != nil || len(code) != 4 {
		t.Fatalf("per-address code = %x, err=%v", code, err)
	}
	// An address the endpoint reports no code for answers an empty
	// reply, not an error.
	if code, err := n.CodeAt(context.Background(), evmAddr(t, "0x0000000000000000000000000000000000000005")); err != nil || len(code) != 0 {
		t.Fatalf("empty code = %x, err=%v", code, err)
	}
	captured := fake.capturedCodes()
	if len(captured) != 3 {
		t.Fatalf("captured codes = %v", captured)
	}
}

func TestNetworkCodeAtRejectsForeignFamily(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	_, err := n.CodeAt(context.Background(), solAddr(t, "11111111111111111111111111111111"))
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
	if fake.callCount("code_at") != 0 {
		t.Error("foreign-family address must not reach the endpoint")
	}
}

func TestNetworkCodeAtFailure(t *testing.T) {
	fake := newFakeClient(1).fail("code_at", cherrs.New(cherrs.NetworkUnavailable, "evm.mainnet.code_at", "rpc down"))
	n := testNetwork(t, fake)
	_, err := n.CodeAt(context.Background(), evmAddr(t, weiHolder))
	if !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("err = %v, want network unavailable", err)
	}
	if got := n.Stats(); got.Errors != 1 {
		t.Errorf("errors = %d, want 1", got.Errors)
	}
}

func TestAccountProviderExternalOwned(t *testing.T) {
	fake := newFakeClient(1)
	fake.balance = big.NewInt(1_000_000_000)
	n := testNetwork(t, fake)
	addr := evmAddr(t, weiHolder)

	got, err := n.AccountProvider().Account(context.Background(), addr)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got.Owner != account.OwnerExternal {
		t.Errorf("owner = %s, want external", got.Owner)
	}
	if got.Network != n.ID() || !got.Address.Equal(addr) {
		t.Errorf("identity = %+v", got)
	}
	if got.NativeBalance == nil {
		t.Fatal("native balance missing")
	}
	if got.NativeBalance.Raw != "1000000000" || got.NativeBalance.Decimals != 18 {
		t.Errorf("balance = %+v", got.NativeBalance)
	}
	if got.NativeBalance.AssetID != nativeAssetID(n.ID()) {
		t.Errorf("asset id = %q", got.NativeBalance.AssetID)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("account failed validation: %v", err)
	}
	if details := got.Details; len(details) != 2 || details[0].Key != "has_code" || details[0].Value != "false" ||
		details[1].Key != "code_size" || details[1].Value != "0" {
		t.Errorf("details = %+v", details)
	}
	if got.Owner == account.OwnerProgram {
		t.Error("evm must not report program accounts")
	}
}

func TestAccountProviderContract(t *testing.T) {
	fake := newFakeClient(1)
	fake.code = []byte{0x60, 0x80, 0x60, 0x04}
	fake.balance = big.NewInt(0)
	n := testNetwork(t, fake)
	addr := evmAddr(t, weiHolder)

	got, err := n.AccountProvider().Account(context.Background(), addr)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got.Owner != account.OwnerContract {
		t.Errorf("owner = %s, want contract", got.Owner)
	}
	if got.NativeBalance == nil || got.NativeBalance.Raw != "0" {
		t.Errorf("balance = %+v", got.NativeBalance)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("account failed validation: %v", err)
	}
}

func TestAccountProviderAccountErrors(t *testing.T) {
	cases := []struct {
		name  string
		addr  identifiers.Address
		isErr error
	}{
		{"foreign family", solAddr(t, "11111111111111111111111111111111"), cherrs.ErrInvalidArgument},
		{"malformed address", identifiers.Address{Family: identifiers.FamilyEVM, Value: "0x1234"}, cherrs.ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeClient(1)
			n := testNetwork(t, fake)
			_, err := n.AccountProvider().Account(context.Background(), tc.addr)
			if !errors.Is(err, tc.isErr) {
				t.Fatalf("err = %v, want %v", err, tc.isErr)
			}
			if fake.callCount("balance_at") != 0 || fake.callCount("code_at") != 0 {
				t.Error("invalid address must not reach the endpoint")
			}
		})
	}
	// A balance failure surfaces with no account, and the code query is
	// not attempted.
	t.Run("balance failure", func(t *testing.T) {
		fake := newFakeClient(1).fail("balance_at", cherrs.New(cherrs.NetworkUnavailable, "x", "down"))
		n := testNetwork(t, fake)
		if _, err := n.AccountProvider().Account(context.Background(), evmAddr(t, weiHolder)); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if fake.callCount("code_at") != 0 {
			t.Error("code query after a balance failure")
		}
	})
	// A code failure also fails the query: owner kind cannot be guessed.
	t.Run("code failure", func(t *testing.T) {
		fake := newFakeClient(1).fail("code_at", cherrs.New(cherrs.NetworkUnavailable, "x", "down"))
		n := testNetwork(t, fake)
		if _, err := n.AccountProvider().Account(context.Background(), evmAddr(t, weiHolder)); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestAccountProviderBalance(t *testing.T) {
	fake := newFakeClient(1)
	fake.balance = big.NewInt(2_500_000)
	n := testNetwork(t, fake)
	prov := n.AccountProvider()

	got, err := prov.Balance(context.Background(), evmAddr(t, weiHolder))
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if got.Raw != "2500000" || got.Decimals != 18 || got.AssetID != nativeAssetID(n.ID()) {
		t.Errorf("balance = %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("balance invalid: %v", err)
	}
	if _, err := prov.Balance(context.Background(), solAddr(t, "11111111111111111111111111111111")); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("foreign family: %v", err)
	}
}

func TestAccountProviderHealthy(t *testing.T) {
	fake := newFakeClient(1)
	n := testNetwork(t, fake)
	if err := n.AccountProvider().Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	fake2 := newFakeClient(1).fail("chain_id", cherrs.New(cherrs.NetworkUnavailable, "x", "down"))
	if err := testNetwork(t, fake2).AccountProvider().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("Healthy: %v", err)
	}
}

func TestAdapterExposesAccountProvider(t *testing.T) {
	clients, _, _, _ := threeNetClients()
	store, _ := startModule(t, threeNetConfig(), clients)

	raw, ok := store.Capability(capability.Account)
	if !ok {
		t.Fatal("account capability not registered")
	}
	adapter, ok := raw.(*Adapter)
	if !ok {
		t.Fatalf("account capability type = %T, want *Adapter", raw)
	}
	n, ok := adapter.Network(netID("mainnet", 1))
	if !ok {
		t.Fatal("mainnet network missing")
	}
	if got, err := n.AccountProvider().Account(context.Background(), evmAddr(t, weiHolder)); err != nil {
		t.Fatalf("Account through the module: %v", err)
	} else if got.Owner != account.OwnerExternal {
		t.Errorf("owner = %s, want external", got.Owner)
	}
	if _, ok := adapter.AccountProvider(netID("mainnet", 1)); !ok {
		t.Error("account provider for mainnet missing")
	}
	if _, ok := adapter.AccountProvider(netID("unknown", 7)); ok {
		t.Error("unknown network must not resolve an account provider")
	}
}
