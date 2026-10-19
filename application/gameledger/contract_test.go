package gameledger

import (
	"testing"

	"github.com/cuihairu/chame/core/identifiers"
)

func evmAsset(std Standard, id string) AssetIdentity {
	return AssetIdentity{Network: identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, Standard: std, Contract: "0x00000000000000000000000000000000000000AA", TokenID: id}
}

func TestDuplicateTransferEventIsAppliedExactlyOnce(t *testing.T) {
	ledger := NewLedger()
	e := Entry{Key: "evm:tx-1:log-4", Player: "player-1", Asset: evmAsset(StandardERC20, ""), Delta: 25, State: Final}
	inserted, err := ledger.Apply(e)
	if err != nil || !inserted {
		t.Fatalf("first delivery: inserted=%v err=%v", inserted, err)
	}
	inserted, err = ledger.Apply(e)
	if err != nil || inserted {
		t.Fatalf("duplicate delivery: inserted=%v err=%v", inserted, err)
	}
	got, ok := ledger.Entry(e.Key)
	if !ok || got.Delta != 25 {
		t.Fatalf("entry=%+v found=%v", got, ok)
	}
	if len(ledger.entries) != 1 {
		t.Fatalf("entries=%d, want exactly one", len(ledger.entries))
	}
}

func TestPendingEntryIsVisibleButNotSpendable(t *testing.T) {
	ledger := NewLedger()
	e := Entry{Key: "sol:sig-1:ix-2", Player: "p", Asset: evmAsset(StandardERC20, ""), Delta: 10, State: Pending}
	if inserted, err := ledger.Apply(e); err != nil || !inserted {
		t.Fatalf("Apply=%v,%v", inserted, err)
	}
	if got, ok := ledger.Entry(e.Key); !ok || got.State != Pending {
		t.Fatalf("pending entry not queryable: %+v, %v", got, ok)
	}
	if got, ok := ledger.SpendableDelta(e.Key); ok || got != 0 {
		t.Fatalf("pending delta spendable: %d, %v", got, ok)
	}
	if _, err := ledger.SetState(e.Key, Confirmed); err != nil {
		t.Fatal(err)
	}
	if got, ok := ledger.SpendableDelta(e.Key); !ok || got != 10 {
		t.Fatalf("confirmed delta=%d spendable=%v", got, ok)
	}
}

func TestReorgCompensatesOnce(t *testing.T) {
	ledger := NewLedger()
	e := Entry{Key: "evm:tx-2:log-0", Player: "p", Asset: evmAsset(StandardERC721, "0007"), Delta: 1, State: Confirmed}
	if _, err := ledger.Apply(e); err != nil {
		t.Fatal(err)
	}
	got, err := ledger.SetState(e.Key, Reorged)
	if err != nil || got != -1 {
		t.Fatalf("first reorg compensation=%d err=%v", got, err)
	}
	got, err = ledger.SetState(e.Key, Reorged)
	if err != nil || got != 0 {
		t.Fatalf("repeat reorg compensation=%d err=%v", got, err)
	}
	if _, ok := ledger.SpendableDelta(e.Key); ok {
		t.Fatal("reorged entry remains spendable")
	}
}

func TestAssetIdentityNormalizationPreservesStandardDifferences(t *testing.T) {
	erc721, err := evmAsset(StandardERC721, "0007").Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if want := "0x00000000000000000000000000000000000000aa"; erc721.Contract != want {
		t.Fatalf("contract=%s, want %s", erc721.Contract, want)
	}
	if erc721.TokenID != "7" {
		t.Fatalf("token id=%s, want canonical 7", erc721.TokenID)
	}
	erc1155, err := evmAsset(StandardERC1155, "7").Normalize()
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
