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
