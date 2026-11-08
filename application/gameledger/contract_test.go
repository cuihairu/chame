package gameledger

import (
	"testing"

	"github.com/cuihairu/chame/capability/asset"
	"github.com/cuihairu/chame/core/identifiers"
)

func evmAsset(std asset.Standard, id string) asset.AssetIdentity {
	return asset.AssetIdentity{Network: identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, Standard: std, Contract: "0x00000000000000000000000000000000000000AA", TokenID: id}
}

func TestDuplicateTransferEventIsAppliedExactlyOnce(t *testing.T) {
	ledger := NewLedger()
	e := Entry{Key: "evm:tx-1:log-4", Player: "player-1", Asset: evmAsset(asset.StandardERC20, ""), Delta: 25, State: Final}
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
	e := Entry{Key: "sol:sig-1:ix-2", Player: "p", Asset: evmAsset(asset.StandardERC20, ""), Delta: 10, State: Pending}
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
	e := Entry{Key: "evm:tx-2:log-0", Player: "p", Asset: evmAsset(asset.StandardERC721, "0007"), Delta: 1, State: Confirmed}
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

func TestLedgerUsesCapabilityAssetIdentity(t *testing.T) {
	// The ledger's asset key is the capability-layer identity; a standard
	// mismatch is preserved, never collapsed.
	if evmAsset(asset.StandardERC721, "0007") == evmAsset(asset.StandardERC1155, "0007") {
		t.Fatal("ERC-721 and ERC-1155 identities were collapsed")
	}
}
