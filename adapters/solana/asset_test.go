package solana

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/application/gameledger"
	"github.com/cuihairu/chame/capability/asset"
	"github.com/cuihairu/chame/capability/nft"
	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

const (
	// A valid mint address and the token account the holder names.
	mintAddress   = voteAccount
	holderAccount = tokenAccount
)

// mintData builds one SPL Token Mint account's data exactly as the
// layout encodes it.
func mintData(decimals byte, authority byte, supply uint64, initialized bool) []byte {
	d := make([]byte, mintLayoutSize)
	d[0] = authority
	binary.LittleEndian.PutUint64(d[mintSupplyOffset:], supply)
	d[mintDecimalsOffset] = decimals
	if initialized {
		d[mintInitOffset] = 1
	}
	return d
}

// withMintData makes the endpoint report the given data for every
// address queried.
func withMintData(f *fakeClient, data []byte) *fakeClient {
	f.account = &accountResult{Value: &rpc.Account{
		Lamports:   1,
		Owner:      solana.MPK(tokenProgram),
		Data:       rpc.DataBytesOrJSONFromBytes(data),
		Executable: false,
		Space:      uint64(len(data)),
	}}
	return f
}

func solAsset(kind, address string) identifiers.AssetID {
	return identifiers.AssetID{Network: clusterID("mainnet", testGenesis(0x10)), Kind: kind, Address: address}
}

func TestAssetProviderNative(t *testing.T) {
	n := newTestNetwork(newFakeClient(solanaHashFor(0x10)))
	got, err := n.AssetProvider().Asset(context.Background(), solAsset(identifiers.KindNative, ""))
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if got.Symbol != "" || got.Name != "" {
		t.Errorf("native asset must carry no symbol or name: %+v", got)
	}
	if got.Decimals != 9 {
		t.Errorf("decimals = %d, want 9", got.Decimals)
	}
	if len(got.Details) != 1 || got.Details[0].Key != "base_unit" || got.Details[0].Value != "lamport" {
		t.Errorf("details = %+v", got.Details)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset info failed validation: %v", err)
	}
}

func TestAssetProviderSPL(t *testing.T) {
	n := newTestNetwork(withMintData(newFakeClient(solanaHashFor(0x10)), mintData(6, 1, 1_000_000_000, true)))
	got, err := n.AssetProvider().Asset(context.Background(), solAsset(identifiers.KindSPLToken, mintAddress))
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if got.Symbol != "" || got.Name != "" {
		t.Errorf("SPL mints carry no on-chain symbol or name: %+v", got)
	}
	if got.Decimals != 6 {
		t.Errorf("decimals = %d, want 6", got.Decimals)
	}
	if got.Asset.Address != mintAddress || got.Asset.Kind != identifiers.KindSPLToken {
		t.Errorf("asset id = %+v", got.Asset)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset info failed validation: %v", err)
	}
	want := []asset.Detail{
		{Key: "mint_authority", Value: "present"},
		{Key: "supply", Value: "1000000000"},
		{Key: "is_initialized", Value: "true"},
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

func TestSolanaMetaplexNFTIsDistinctFromFungibleSPL(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	mint, _ := solana.PublicKeyFromBase58(mintAddress)
	program := solana.MustPublicKeyFromBase58(metaplexMetadataProgram)
	_, _, err := solana.FindProgramAddress([][]byte{[]byte("metadata"), program[:], mint[:]}, program)
	if err != nil {
		t.Fatal(err)
	}
	metadata := make([]byte, 65)
	copy(metadata[33:65], mint[:])
	f.account = &accountResult{Value: &rpc.Account{Owner: program, Data: rpc.DataBytesOrJSONFromBytes(metadata)}}
	f.tokenAccounts = &tokenAccountsResult{}
	n := newTestNetwork(f)
	p := n.AssetProvider()
	owner := mustAddress(t, sysAccount)
	nftID := gameledger.AssetIdentity{Network: n.ID(), Standard: gameledger.StandardSPLNFT, Contract: mintAddress, TokenID: "0"}
	got, err := p.SolanaHolding(context.Background(), nftID, owner)
	if err != nil {
		t.Fatal(err)
	}
	if got.Classification != nft.SolanaMetaplexNFT || got.Held {
		t.Fatalf("NFT classification/holding=%+v", got)
	}
	fungibleID := nftID
	fungibleID.Standard = gameledger.StandardSPL
	fungibleID.TokenID = ""
	if _, err := p.SolanaHolding(context.Background(), fungibleID, owner); err == nil {
		t.Fatal("Metaplex NFT accepted as fungible SPL")
	}
}

func TestSolanaNFTQueryPropagatesRPCFailure(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("account_info", errors.New("offline"))
	n := newTestNetwork(f)
	id := gameledger.AssetIdentity{Network: n.ID(), Standard: gameledger.StandardSPLNFT, Contract: mintAddress, TokenID: "0"}
	if _, err := n.AssetProvider().SolanaHolding(context.Background(), id, mustAddress(t, sysAccount)); err == nil {
		t.Fatal("RPC error was hidden")
	}
}

func TestAssetProviderSPLUninitialized(t *testing.T) {
	n := newTestNetwork(withMintData(newFakeClient(solanaHashFor(0x10)), mintData(0, 0, 0, false)))
	got, err := n.AssetProvider().Asset(context.Background(), solAsset(identifiers.KindSPLToken, mintAddress))
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if got.Decimals != 0 || len(got.Details) != 3 {
		t.Fatalf("token info = %+v", got)
	}
	if got.Details[0].Value != "absent" || got.Details[2].Value != "false" {
		t.Errorf("details = %+v", got.Details)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset info failed validation: %v", err)
	}
}

func TestAssetProviderSPLMissingMint(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("account_info", rpcErrNotFound)
	n := newTestNetwork(f)
	_, err := n.AssetProvider().Asset(context.Background(), solAsset(identifiers.KindSPLToken, mintAddress))
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
}

func TestAssetProviderSPLBadData(t *testing.T) {
	// A non-token account at the mint address: the layout does not fit.
	n := newTestNetwork(withMintData(newFakeClient(solanaHashFor(0x10)), make([]byte, 40)))
	_, err := n.AssetProvider().Asset(context.Background(), solAsset(identifiers.KindSPLToken, mintAddress))
	if !errors.Is(err, cherrs.ErrEventDecodeFailure) {
		t.Fatalf("err = %v, want decode failure", err)
	}
}

func TestAssetProviderRejectsBadAsset(t *testing.T) {
	cases := []struct {
		name string
		id   identifiers.AssetID
		is   error
	}{
		{"foreign network", identifiers.AssetID{Network: clusterID("devnet", testGenesis(0x20)), Kind: identifiers.KindNative}, cherrs.ErrInvalidArgument},
		{"native with address", solAsset(identifiers.KindNative, mintAddress), cherrs.ErrInvalidArgument},
		{"unknown kind", solAsset("nft", mintAddress), cherrs.ErrInvalidArgument},
		{"evm token kind on solana", solAsset(identifiers.KindEVMToken, mintAddress), cherrs.ErrUnsupportedCapability},
		{"bad mint address", solAsset(identifiers.KindSPLToken, "nope"), cherrs.ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeClient(solanaHashFor(0x10))
			n := newTestNetwork(f)
			if _, err := n.AssetProvider().Asset(context.Background(), tc.id); !errors.Is(err, tc.is) {
				t.Fatalf("err = %v, want %v", err, tc.is)
			}
			// No identifier-level mistake may reach the endpoint.
			if got := f.capturedAccounts(); len(got) != 0 {
				t.Errorf("invalid asset reached the endpoint: %+v", got)
			}
		})
	}
}

func TestAssetProviderBalanceNative(t *testing.T) {
	n := newTestNetwork(newFakeClient(solanaHashFor(0x10)))
	prov := n.AssetProvider()
	id := solAsset(identifiers.KindNative, "")
	holder := mustAddress(t, holderAccount)

	bal, err := prov.Balance(context.Background(), id, holder)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal.Raw != "0" || bal.Decimals != 9 || bal.AssetID != id.String() {
		t.Errorf("balance = %+v", bal)
	}
	if err := bal.Validate(); err != nil {
		t.Fatalf("balance invalid: %v", err)
	}
	if _, err := prov.Balance(context.Background(), id, evmAddress(t)); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("foreign holder: %v", err)
	}
}

func TestAssetProviderBalanceSPL(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.tokenBalance = &tokenBalanceResult{Value: &rpc.UiTokenAmount{
		Amount:   "4200000000",
		Decimals: 6,
	}}
	n := newTestNetwork(f)
	id := solAsset(identifiers.KindSPLToken, mintAddress)

	bal, err := n.AssetProvider().Balance(context.Background(), id, mustAddress(t, holderAccount))
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal.Raw != "4200000000" || bal.Decimals != 6 || bal.AssetID != id.String() {
		t.Errorf("balance = %+v", bal)
	}
	if err := bal.Validate(); err != nil {
		t.Fatalf("balance invalid: %v", err)
	}
	// The node's own token account is what the caller named: no
	// associated-token-account resolution is attempted.
	if got := f.capturedAccounts(); len(got) != 1 || got[0].String() != holderAccount {
		t.Errorf("captured accounts = %v", got)
	}
}

func TestAssetProviderBalanceSPLZero(t *testing.T) {
	// The node reports null rather than zero for an address holding no
	// token amount, so the mint's precision stamps the zero.
	f := withMintData(newFakeClient(solanaHashFor(0x10)), mintData(9, 0, 0, false))
	f.tokenBalance = &tokenBalanceResult{Value: nil}
	n := newTestNetwork(f)
	id := solAsset(identifiers.KindSPLToken, mintAddress)

	bal, err := n.AssetProvider().Balance(context.Background(), id, mustAddress(t, holderAccount))
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal.Raw != "0" || bal.Decimals != 9 || bal.AssetID != id.String() {
		t.Errorf("balance = %+v", bal)
	}
	if err := bal.Validate(); err != nil {
		t.Fatalf("balance invalid: %v", err)
	}
}

func TestAssetProviderBalanceErrors(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("token_balance", netErr{msg: "connection refused"})
	n := newTestNetwork(f)
	if _, err := n.AssetProvider().Balance(context.Background(),
		solAsset(identifiers.KindSPLToken, mintAddress), mustAddress(t, holderAccount)); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("err = %v, want network unavailable", err)
	}
	if _, err := n.AssetProvider().Balance(context.Background(), solAsset("nft", mintAddress), mustAddress(t, holderAccount)); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := n.AssetProvider().Balance(context.Background(), solAsset(identifiers.KindEVMToken, mintAddress), mustAddress(t, holderAccount)); !errors.Is(err, cherrs.ErrUnsupportedCapability) {
		t.Fatalf("evm token kind: %v", err)
	}
	otherNet := identifiers.AssetID{Network: clusterID("devnet", testGenesis(0x20)), Kind: identifiers.KindNative}
	if _, err := n.AssetProvider().Balance(context.Background(), otherNet, mustAddress(t, holderAccount)); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("foreign network: %v", err)
	}
}

func TestAssetProviderHealthy(t *testing.T) {
	if err := newTestNetwork(newFakeClient(solanaHashFor(0x10))).AssetProvider().Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	f := newFakeClient(solanaHashFor(0x10)).fail("genesis_hash", netErr{msg: "connection refused"})
	if err := newTestNetwork(f).AssetProvider().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("Healthy: %v", err)
	}
}

func TestAdapterExposesAccountAndAssetProvider(t *testing.T) {
	clients, _, _, _ := twoNetClients()
	store, _ := startModule(t, twoNetConfig(), clients)

	raw, _ := store.Capability(capability.Account)
	adapter, ok := raw.(*Adapter)
	if !ok {
		t.Fatalf("account capability type = %T, want *Adapter", raw)
	}
	for _, id := range []identifiers.NetworkID{clusterID("mainnet", testGenesis(0x10)), clusterID("devnet", testGenesis(0x20))} {
		if _, ok := adapter.AccountProvider(id); !ok {
			t.Fatalf("account provider for %s missing", id)
		}
		if _, ok := adapter.AssetProvider(id); !ok {
			t.Fatalf("asset provider for %s missing", id)
		}
	}
	if _, ok := adapter.AccountProvider(clusterID("unknown", testGenesis(0x99))); ok {
		t.Error("unknown network must not resolve an account provider")
	}
	if _, ok := adapter.AssetProvider(clusterID("unknown", testGenesis(0x99))); ok {
		t.Error("unknown network must not resolve an asset provider")
	}
}
