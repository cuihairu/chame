package solana

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/capability/account"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// solAccountFixture builds a fake endpoint reporting one account.
func solAccountFixture(lamports uint64, owner string, data *rpc.DataBytesOrJSON, executable bool, space uint64) *fakeClient {
	f := newFakeClient(solanaHashFor(0x10))
	f.lamports = lamports
	f.account = &accountResult{Value: &rpc.Account{
		Lamports:   lamports,
		Owner:      solana.MPK(owner),
		Data:       data,
		Executable: executable,
		RentEpoch:  new(big.Int).SetInt64(3),
		Space:      space,
	}}
	return f
}

func helloData(t *testing.T) *rpc.DataBytesOrJSON {
	t.Helper()
	data, err := rpc.DataBytesOrJSONFromBase64("aGVsbG8=")
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	return data
}

func evmAddress(t *testing.T) identifiers.Address {
	t.Helper()
	a, err := identifiers.NewEVMAddress("0x1234567890123456789012345678901234567890")
	if err != nil {
		t.Fatalf("evm address: %v", err)
	}
	return a
}

func TestAccountProviderSystemOwned(t *testing.T) {
	n := newTestNetwork(solAccountFixture(2_500_000, systemProgramID, helloData(t), false, 5))
	holder := mustAddress(t, voteAccount)

	got, err := n.AccountProvider().Account(context.Background(), holder)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got.Owner != account.OwnerExternal {
		t.Errorf("owner = %s, want external", got.Owner)
	}
	if got.Network != n.ID() || !got.Address.Equal(holder) {
		t.Errorf("identity = %+v", got)
	}
	if got.NativeBalance == nil || got.NativeBalance.Raw != "2500000" || got.NativeBalance.Decimals != 9 {
		t.Errorf("balance = %+v", got.NativeBalance)
	}
	if got.NativeBalance.AssetID != nativeAssetID(n.ID()) {
		t.Errorf("asset id = %q", got.NativeBalance.AssetID)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("account failed validation: %v", err)
	}
	want := []account.Detail{
		{Key: "owner_program", Value: systemProgramID},
		{Key: "data_length", Value: "5"},
		{Key: "executable", Value: "false"},
		{Key: "rent_epoch", Value: "3"},
		{Key: "space", Value: "5"},
	}
	if len(got.Details) != len(want) {
		t.Fatalf("details = %+v", got.Details)
	}
	for i := range want {
		if got.Details[i] != want[i] {
			t.Errorf("details[%d] = %+v, want %+v", i, got.Details[i], want[i])
		}
	}
}

func TestAccountProviderProgramOwned(t *testing.T) {
	// A program-owned account: the address exists, no key does.
	n := newTestNetwork(solAccountFixture(0, tokenProgram, helloData(t), true, 2048))

	got, err := n.AccountProvider().Account(context.Background(), mustAddress(t, voteAccount))
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got.Owner != account.OwnerProgram {
		t.Errorf("owner = %s, want program", got.Owner)
	}
	if got.NativeBalance == nil || got.NativeBalance.Raw != "0" {
		t.Errorf("balance = %+v", got.NativeBalance)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("account failed validation: %v", err)
	}
}

func TestAccountProviderNotFound(t *testing.T) {
	// The node answers null for an address it has never seen: that is
	// not an account, and the provider says so.
	f := newFakeClient(solanaHashFor(0x10)).fail("account_info", rpcErrNotFound)
	n := newTestNetwork(f)

	_, err := n.AccountProvider().Account(context.Background(), mustAddress(t, voteAccount))
	if !errors.Is(err, account.ErrAccountNotFound) {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

func TestAccountProviderErrors(t *testing.T) {
	cases := []struct {
		name string
		addr identifiers.Address
	}{
		{"foreign family", evmAddress(t)},
		{"malformed address", identifiers.Address{Family: identifiers.FamilySolana, Value: "nope"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeClient(solanaHashFor(0x10))
			n := newTestNetwork(f)
			_, err := n.AccountProvider().Account(context.Background(), tc.addr)
			if !errors.Is(err, cherrs.ErrInvalidArgument) {
				t.Fatalf("err = %v, want invalid argument", err)
			}
			if f.callCount("account_info") != 0 {
				t.Error("invalid address must not reach the endpoint")
			}
		})
	}
	t.Run("endpoint failure", func(t *testing.T) {
		f := newFakeClient(solanaHashFor(0x10)).fail("account_info", netErr{msg: "connection refused"})
		if _, err := newTestNetwork(f).AccountProvider().Account(context.Background(), mustAddress(t, voteAccount)); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
			t.Fatalf("err = %v, want network unavailable", err)
		}
	})
}

func TestAccountProviderBalance(t *testing.T) {
	n := newTestNetwork(solAccountFixture(1_000_000_000, systemProgramID, nil, false, 0))
	prov := n.AccountProvider()

	got, err := prov.Balance(context.Background(), mustAddress(t, voteAccount))
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if got.Raw != "1000000000" || got.Decimals != 9 || got.AssetID != nativeAssetID(n.ID()) {
		t.Errorf("balance = %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("balance invalid: %v", err)
	}
	if _, err := prov.Balance(context.Background(), evmAddress(t)); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("foreign family: %v", err)
	}
}

func TestAccountProviderHealthy(t *testing.T) {
	if err := newTestNetwork(newFakeClient(solanaHashFor(0x10))).AccountProvider().Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	f := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	if err := newTestNetwork(f).AccountProvider().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("Healthy: %v", err)
	}
}

func TestAccountProviderCommitment(t *testing.T) {
	// The provider states no commitment, so the network's configured
	// level applies.
	f := solAccountFixture(1, systemProgramID, helloData(t), false, 1)
	n := newTestNetwork(f)

	if _, err := n.AccountProvider().Account(context.Background(), mustAddress(t, voteAccount)); err != nil {
		t.Fatalf("Account: %v", err)
	}
	if got := f.capturedCommitments(); len(got) != 1 || got[0] != CommitmentFinalized {
		t.Errorf("commitments = %v, want [finalized]", got)
	}
	if _, err := n.AccountProvider().Balance(context.Background(), mustAddress(t, voteAccount)); err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if got := f.capturedCommitments(); len(got) != 2 {
		t.Errorf("commitments = %v", got)
	}
}
