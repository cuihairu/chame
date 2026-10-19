package evm

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"math/big"
	"testing"

	"github.com/cuihairu/chame/application/gameledger"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

func TestNFT721SameContractDifferentTokenIDsAreDistinct(t *testing.T) {
	f := newFakeClient(1)
	f.selectorReply = map[string][]byte{"6352211e": append(make([]byte, 12), common.HexToAddress("0x0000000000000000000000000000000000000011").Bytes()...)}
	p := testNetwork(t, f).AssetProvider()
	base := gameledger.AssetIdentity{Network: p.Network(), Standard: gameledger.StandardERC721, Contract: erc20Addr, TokenID: "1"}
	a, err := p.ERC721Owner(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	base.TokenID = "2"
	b, err := p.ERC721Owner(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if a.Owner.Value != "0x0000000000000000000000000000000000000011" || b.Owner.Value != a.Owner.Value || a.FinalitySatisfied || len(f.calls) != 2 || !bytes.Equal(f.calls[0].Data[4:], word32(1)) || !bytes.Equal(f.calls[1].Data[4:], word32(2)) {
		t.Fatalf("token identities not preserved: %+v %+v calls=%d", a, b, len(f.calls))
	}
}

func TestNFTQueryRPCErrorAndUnfinalizedObservation(t *testing.T) {
	f := newFakeClient(1)
	f.fail("call_contract", errors.New("offline"))
	p := testNetwork(t, f).AssetProvider()
	id := gameledger.AssetIdentity{Network: p.Network(), Standard: gameledger.StandardERC721, Contract: erc20Addr, TokenID: "9"}
	if _, err := p.ERC721Owner(context.Background(), id); err == nil {
		t.Fatal("RPC error was hidden")
	}
	f2 := newFakeClient(1)
	f2.selectorReply = map[string][]byte{"6352211e": append(make([]byte, 12), common.HexToAddress("0x0000000000000000000000000000000000000011").Bytes()...)}
	got, err := testNetwork(t, f2).AssetProvider().ERC721Owner(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.FinalitySatisfied {
		t.Fatal("latest eth_call must remain an unfinalized observation")
	}
}

func TestNFT1155QuantityIsPreserved(t *testing.T) {
	f := newFakeClient(1)
	f.selectorReply = map[string][]byte{"00fdd58e": word32(17)}
	p := testNetwork(t, f).AssetProvider()
	id := gameledger.AssetIdentity{Network: p.Network(), Standard: gameledger.StandardERC1155, Contract: erc20Addr, TokenID: "7"}
	got, err := p.ERC1155Balance(context.Background(), id, evmAddr(t, "0x0000000000000000000000000000000000000022"))
	if err != nil {
		t.Fatal(err)
	}
	if got.RawQuantity.Cmp(big.NewInt(17)) != 0 {
		t.Fatalf("quantity=%s want 17", got.RawQuantity)
	}
}

const (
	erc20Addr = "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	// keccak256 of the ERC-20 metadata signatures, without the 0x prefix.
	decimalsSel = "313ce567"
	symbolSel   = "95d89b41"
	nameSel     = "06fdde03"
)

func word32(v int64) []byte {
	out := make([]byte, 32)
	new(big.Int).SetInt64(v).FillBytes(out)
	return out
}

func stringWord(s string) []byte {
	out := append([]byte(nil), word32(0x20)...)
	out = append(out, word32(int64(len(s)))...)
	buf := make([]byte, 32)
	copy(buf, s)
	return append(out, buf...)
}

func evmAsset(kind string, addr string) identifiers.AssetID {
	return identifiers.AssetID{Network: netID("mainnet", 1), Kind: kind, Address: addr}
}

func TestAssetProviderNative(t *testing.T) {
	n := testNetwork(t, newFakeClient(1))
	got, err := n.AssetProvider().Asset(context.Background(), evmAsset(identifiers.KindNative, ""))
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if got.Symbol != "" || got.Name != "" {
		t.Errorf("native asset must carry no symbol or name: %+v", got)
	}
	if got.Decimals != 18 {
		t.Errorf("decimals = %d, want 18", got.Decimals)
	}
	if got.Asset.Kind != identifiers.KindNative {
		t.Errorf("kind = %s", got.Asset.Kind)
	}
	if len(got.Details) != 1 || got.Details[0].Key != "base_unit" || got.Details[0].Value != "wei" {
		t.Errorf("details = %+v", got.Details)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset info failed validation: %v", err)
	}
}

func TestAssetProviderToken(t *testing.T) {
	fake := newFakeClient(1).
		withCall(decimalsSel, word32(6)).
		withCall(symbolSel, stringWord("USDT")).
		withCall(nameSel, stringWord("Tether USD"))
	n := testNetwork(t, fake)

	got, err := n.AssetProvider().Asset(context.Background(), evmAsset(identifiers.KindEVMToken, erc20Addr))
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if got.Symbol != "USDT" || got.Name != "Tether USD" || got.Decimals != 6 {
		t.Errorf("token info = %+v", got)
	}
	if got.Asset.Address != erc20Addr || got.Asset.Kind != identifiers.KindEVMToken {
		t.Errorf("asset id = %+v", got.Asset)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset info failed validation: %v", err)
	}
	// The three metadata functions are each called against the token
	// contract, by selector.
	seen := map[string]bool{}
	for _, c := range fake.capturedCalls() {
		if c.To == nil {
			t.Fatalf("call without a target: %+v", c)
		}
		if c.To.Hex() != erc20Addr {
			t.Errorf("call target = %s, want %s", c.To.Hex(), erc20Addr)
		}
		if len(c.Data) != 4 {
			t.Errorf("call data = %x, want a bare selector", c.Data)
		}
		seen[hex.EncodeToString(c.Data)] = true
	}
	for _, sel := range []string{decimalsSel, symbolSel, nameSel} {
		if !seen[sel] {
			t.Errorf("selector %s never called", sel)
		}
	}
}

func TestAssetProviderTokenOptionalMetadata(t *testing.T) {
	// A contract without symbol() or name() answers a short reply: the
	// fields degrade to empty and the query still succeeds.
	fake := newFakeClient(1).
		withCall(decimalsSel, word32(18)).
		withCall(symbolSel, []byte{0x01}).
		withCall(nameSel, []byte{0x01, 0x02})
	n := testNetwork(t, fake)

	got, err := n.AssetProvider().Asset(context.Background(), evmAsset(identifiers.KindEVMToken, erc20Addr))
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if got.Symbol != "" || got.Name != "" || got.Decimals != 18 {
		t.Errorf("token info = %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset info failed validation: %v", err)
	}
}

func TestAssetProviderTokenRequiresDecimals(t *testing.T) {
	fake := newFakeClient(1).withCall(decimalsSel, []byte{0x01})
	n := testNetwork(t, fake)
	_, err := n.AssetProvider().Asset(context.Background(), evmAsset(identifiers.KindEVMToken, erc20Addr))
	if !errors.Is(err, cherrs.ErrEventDecodeFailure) {
		t.Fatalf("err = %v, want decode failure", err)
	}
}

func TestAssetProviderRejectsBadAsset(t *testing.T) {
	cases := []struct {
		name  string
		id    identifiers.AssetID
		isErr error
	}{
		{"foreign network", identifiers.AssetID{Network: netID("base", 8453), Kind: identifiers.KindNative}, cherrs.ErrInvalidArgument},
		{"native with address", evmAsset(identifiers.KindNative, erc20Addr), cherrs.ErrInvalidArgument},
		{"unknown kind", evmAsset("nft", erc20Addr), cherrs.ErrInvalidArgument},
		{"spl kind on evm", identifiers.AssetID{Network: netID("mainnet", 1), Kind: identifiers.KindSPLToken, Address: erc20Addr}, cherrs.ErrUnsupportedCapability},
		{"bad token address", identifiers.AssetID{Network: netID("mainnet", 1), Kind: identifiers.KindEVMToken, Address: "nope"}, cherrs.ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeClient(1)
			n := testNetwork(t, fake)
			if _, err := n.AssetProvider().Asset(context.Background(), tc.id); !errors.Is(err, tc.isErr) {
				t.Fatalf("err = %v, want %v", err, tc.isErr)
			}
			if calls := fake.capturedCalls(); len(calls) != 0 {
				t.Errorf("invalid asset reached the endpoint: %+v", calls)
			}
		})
	}
}

func TestAssetProviderBalance(t *testing.T) {
	fake := newFakeClient(1)
	fake.balance = big.NewInt(1_500_000_000)
	fake.withCall(decimalsSel, word32(6))
	fake.callReply = word32(42)
	n := testNetwork(t, fake)
	prov := n.AssetProvider()
	holder := evmAddr(t, weiHolder)

	native, err := prov.Balance(context.Background(), evmAsset(identifiers.KindNative, ""), holder)
	if err != nil {
		t.Fatalf("native balance: %v", err)
	}
	if native.Raw != "1500000000" || native.Decimals != 18 || native.AssetID != evmAsset(identifiers.KindNative, "").String() {
		t.Errorf("native = %+v", native)
	}
	if err := native.Validate(); err != nil {
		t.Fatalf("native amount invalid: %v", err)
	}

	tokenID := evmAsset(identifiers.KindEVMToken, erc20Addr)
	token, err := prov.Balance(context.Background(), tokenID, holder)
	if err != nil {
		t.Fatalf("token balance: %v", err)
	}
	if token.Raw != "42" || token.Decimals != 6 || token.AssetID != tokenID.String() {
		t.Errorf("token = %+v", token)
	}
	if err := token.Validate(); err != nil {
		t.Fatalf("token amount invalid: %v", err)
	}
}

func TestAssetProviderBalanceErrors(t *testing.T) {
	n := testNetwork(t, newFakeClient(1))
	prov := n.AssetProvider()
	cases := []struct {
		name   string
		id     identifiers.AssetID
		holder identifiers.Address
		isErr  error
	}{
		{"foreign holder", evmAsset(identifiers.KindNative, ""), solAddr(t, "11111111111111111111111111111111"), cherrs.ErrInvalidArgument},
	}
	for _, tc := range cases {
		if _, err := prov.Balance(context.Background(), tc.id, tc.holder); !errors.Is(err, cherrs.ErrInvalidArgument) {
			t.Errorf("%s: err = %v, want invalid argument", tc.name, err)
		}
	}
	if _, err := prov.Balance(context.Background(), evmAsset("nft", erc20Addr), evmAddr(t, weiHolder)); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("unknown kind: %v", err)
	}
	if _, err := prov.Balance(context.Background(), identifiers.AssetID{Network: netID("base", 8453), Kind: identifiers.KindNative}, evmAddr(t, weiHolder)); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("foreign network: %v", err)
	}
	// No decimals means no expressible balance.
	down := testNetwork(t, newFakeClient(1).withCall(decimalsSel, []byte{0x01}))
	if _, err := down.AssetProvider().Balance(context.Background(), evmAsset(identifiers.KindEVMToken, erc20Addr), evmAddr(t, weiHolder)); !errors.Is(err, cherrs.ErrEventDecodeFailure) {
		t.Errorf("missing decimals: %v", err)
	}
}

func TestAssetProviderHealthy(t *testing.T) {
	if err := testNetwork(t, newFakeClient(1)).AssetProvider().Healthy(context.Background()); err != nil {
		t.Fatalf("Healthy: %v", err)
	}
	if err := testNetwork(t, newFakeClient(1).fail("chain_id",
		cherrs.New(cherrs.NetworkUnavailable, "x", "down"))).AssetProvider().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Fatalf("Healthy: %v", err)
	}
}
