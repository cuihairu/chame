package asset

import (
	"testing"

	"github.com/cuihairu/chame/core/identifiers"
)

var evmNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}

func TestAssetInfoValidate(t *testing.T) {
	native := identifiers.AssetID{Network: evmNet, Kind: identifiers.KindNative}
	tokenAddr := "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"
	base := AssetInfo{
		Asset:    identifiers.AssetID{Network: evmNet, Kind: identifiers.KindEVMToken, Address: tokenAddr},
		Symbol:   "USDT",
		Name:     "Tether USD",
		Decimals: 6,
		Details:  []Detail{{Key: "protocol_version", Value: "1"}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid asset info rejected: %v", err)
	}
	// Native assets carry no address and may carry no symbol: neither is
	// an error, and no business rule is applied to either.
	info := base
	info.Asset = native
	info.Symbol = ""
	info.Name = ""
	info.Details = nil
	if err := info.Validate(); err != nil {
		t.Fatalf("native asset info rejected: %v", err)
	}
	if err := (AssetInfo{Asset: native, Decimals: 36}).Validate(); err != nil {
		t.Fatalf("max decimals rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*AssetInfo)
	}{
		{"bad network", func(a *AssetInfo) {
			a.Asset.Network = identifiers.NetworkID{Family: "cosmos", Name: "x", ChainID: "1"}
		}},
		{"native with address", func(a *AssetInfo) {
			a.Asset = identifiers.AssetID{Network: evmNet, Kind: identifiers.KindNative, Address: "0x1"}
		}},
		{"token without address", func(a *AssetInfo) {
			a.Asset = identifiers.AssetID{Network: evmNet, Kind: identifiers.KindEVMToken}
		}},
		{"unknown kind", func(a *AssetInfo) {
			a.Asset = identifiers.AssetID{Network: evmNet, Kind: "nft", Address: tokenAddr}
		}},
		{"bad token address", func(a *AssetInfo) {
			a.Asset = identifiers.AssetID{Network: evmNet, Kind: identifiers.KindEVMToken, Address: "nope"}
		}},
		{"decimals out of range", func(a *AssetInfo) { a.Decimals = 37 }},
		{"empty detail key", func(a *AssetInfo) { a.Details = append(a.Details, Detail{Value: "x"}) }},
		{"duplicate detail key", func(a *AssetInfo) { a.Details = append(a.Details, Detail{Key: "protocol_version", Value: "2"}) }},
	}
	for _, tc := range cases {
		a := base
		tc.mut(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: accepted invalid asset info", tc.name)
		}
	}
}
