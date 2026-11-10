package asset

import (
	"testing"

	"github.com/cuihairu/chame/core/identifiers"
)

func evmIdentity(std Standard, id string) AssetIdentity {
	return AssetIdentity{Network: identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, Standard: std, Contract: "0x00000000000000000000000000000000000000AA", TokenID: id}
}

// TestAssetIdentityNormalizationPreservesStandardDifferences pins the
// canonical identity rules: EVM addresses lowercased, token ids canonical,
// and the standard space preserved so ERC-721, ERC-1155, and SPL identities
// are never collapsed into one key (design: game-asset-ownership-and-ledger-boundary).
func TestAssetIdentityNormalizationPreservesStandardDifferences(t *testing.T) {
	erc721, err := evmIdentity(StandardERC721, "0007").Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if want := "0x00000000000000000000000000000000000000aa"; erc721.Contract != want {
		t.Fatalf("contract=%s, want %s", erc721.Contract, want)
	}
	if erc721.TokenID != "7" {
		t.Fatalf("token id=%s, want canonical 7", erc721.TokenID)
	}
	erc1155, err := evmIdentity(StandardERC1155, "7").Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if erc721 == erc1155 {
		t.Fatal("ERC-721 and ERC-1155 identities were collapsed")
	}
	spl := AssetIdentity{Network: identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet", ChainID: "genesis"}, Standard: StandardSPL, Contract: "Vote111111111111111111111111111111111111111"}
	splID, err := spl.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if splID.Standard == StandardERC20 || splID.Network.Family == identifiers.FamilyEVM {
		t.Fatal("SPL identity was collapsed into EVM fungible identity")
	}
}

// TestAssetIdentityNormalizeRejectsInvalid walks every error branch of
// Normalize: family/standard mismatches, missing or malformed contracts,
// token ids on non-token-id standards, and unknown standards. Each case
// is a key-collision hazard: an accepted malformed identity could merge
// with a distinct asset key.
func TestAssetIdentityNormalizeRejectsInvalid(t *testing.T) {
	evm := identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}
	solana := identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet", ChainID: "genesis"}
	mint := "Vote111111111111111111111111111111111111111"
	cases := []struct {
		name string
		id   AssetIdentity
	}{
		{"native with contract", AssetIdentity{Network: evm, Standard: StandardNative, Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"}},
		{"native with token id", AssetIdentity{Network: evm, Standard: StandardNative, TokenID: "1"}},
		{"erc20 on solana network", AssetIdentity{Network: solana, Standard: StandardERC20, Contract: mint}},
		{"erc20 without contract", AssetIdentity{Network: evm, Standard: StandardERC20}},
		{"erc20 with malformed address", AssetIdentity{Network: evm, Standard: StandardERC20, Contract: "0x1234"}},
		{"erc20 with token id", AssetIdentity{Network: evm, Standard: StandardERC20, Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", TokenID: "1"}},
		{"erc721 without token id", AssetIdentity{Network: evm, Standard: StandardERC721, Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2"}},
		{"erc721 with non-numeric token id", AssetIdentity{Network: evm, Standard: StandardERC721, Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", TokenID: "0x10"}},
		{"erc1155 with negative token id", AssetIdentity{Network: evm, Standard: StandardERC1155, Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2", TokenID: "-1"}},
		{"spl on evm network", AssetIdentity{Network: evm, Standard: StandardSPL, Contract: mint}},
		{"spl without mint", AssetIdentity{Network: solana, Standard: StandardSPL}},
		{"spl with malformed mint", AssetIdentity{Network: solana, Standard: StandardSPL, Contract: "nope"}},
		{"spl with token id", AssetIdentity{Network: solana, Standard: StandardSPL, Contract: mint, TokenID: "1"}},
		{"spl nft without token id", AssetIdentity{Network: solana, Standard: StandardSPLNFT, Contract: mint}},
		{"spl nft with negative token id", AssetIdentity{Network: solana, Standard: StandardSPLNFT, Contract: mint, TokenID: "-3"}},
		{"unknown standard", AssetIdentity{Network: evm, Standard: "bep2"}},
	}
	for _, tc := range cases {
		got, err := tc.id.Normalize()
		if err == nil {
			t.Errorf("%s: accepted invalid identity %+v", tc.name, got)
		}
	}
}

// TestAssetIdentityNormalizeCanonicalizes pins the canonical form: EVM
// addresses lowercased, token ids canonical unsigned decimal (arbitrary
// width, no leading zeros), and Solana base58 case preserved.
func TestAssetIdentityNormalizeCanonicalizes(t *testing.T) {
	native := AssetIdentity{Network: identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, Standard: StandardNative}
	got, err := native.Normalize()
	if err != nil {
		t.Fatalf("minimal native identity rejected: %v", err)
	}
	if got != native {
		t.Fatalf("native identity changed: %+v", got)
	}
	erc20 := AssetIdentity{
		Network:  identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"},
		Standard: StandardERC20,
		Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
	}
	lowered, err := erc20.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if want := "0xc02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"; lowered.Contract != want {
		t.Fatalf("contract=%s, want lowercased %s", lowered.Contract, want)
	}
	// Token ids are big.Int-canonical decimal: leading zeros collapse and
	// values beyond uint64 round-trip exactly.
	erc1155 := AssetIdentity{
		Network:  identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"},
		Standard: StandardERC1155,
		Contract: "0xC02aaA39b223FE8D0A0e5C4F27eAD9083C756Cc2",
		TokenID:  "0007",
	}
	big, err := erc1155.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if big.TokenID != "7" {
		t.Fatalf("token id=%s, want canonical 7", big.TokenID)
	}
	erc1155.TokenID = "115792089237316195423570985008687907853269984665640564039457584007913129639998"
	wide, err := erc1155.Normalize()
	if err != nil {
		t.Fatalf("uint64-overflowing token id rejected: %v", err)
	}
	if wide.TokenID != erc1155.TokenID {
		t.Fatalf("token id=%s, want unchanged %s", wide.TokenID, erc1155.TokenID)
	}
	spl := AssetIdentity{
		Network:  identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet", ChainID: "genesis"},
		Standard: StandardSPLNFT,
		Contract: "Vote111111111111111111111111111111111111111",
		TokenID:  "0042",
	}
	caseKept, err := spl.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if caseKept.Contract != spl.Contract {
		t.Fatalf("mint=%s, want case-preserved %s", caseKept.Contract, spl.Contract)
	}
	if caseKept.TokenID != "42" {
		t.Fatalf("token id=%s, want canonical 42", caseKept.TokenID)
	}
}
