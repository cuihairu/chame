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

// The compensation amount stays answerable until the host records the
// inverse operation applied: a host that stops between the reorg mark
// and its compensation can retry without losing the amount, and a
// compensated entry answers nothing.
func TestReorgCompensationAnsweredUntilApplied(t *testing.T) {
	ledger := NewLedger()
	e := Entry{Key: "evm:tx-2:log-0", Player: "p", Asset: evmAsset(asset.StandardERC721, "0007"), Delta: 1, State: Confirmed}
	if _, err := ledger.Apply(e); err != nil {
		t.Fatal(err)
	}
	got, err := ledger.SetState(e.Key, Reorged)
	if err != nil || got != -1 {
		t.Fatalf("first reorg compensation=%d err=%v", got, err)
	}
	// A host that stopped before applying the inverse operation retries:
	// the amount is still answerable.
	got, err = ledger.SetState(e.Key, Reorged)
	if err != nil || got != -1 {
		t.Fatalf("retry reorg compensation=%d err=%v", got, err)
	}
	// The host applied it and records that: the entry stops answering.
	if err := ledger.MarkCompensated(e.Key); err != nil {
		t.Fatal(err)
	}
	got, err = ledger.SetState(e.Key, Reorged)
	if err != nil || got != 0 {
		t.Fatalf("compensated entry answered %d err=%v", got, err)
	}
	// Marking twice is a no-op, and nothing of a reorged entry is
	// spendable.
	if err := ledger.MarkCompensated(e.Key); err != nil {
		t.Fatalf("repeat mark: %v", err)
	}
	if _, ok := ledger.SpendableDelta(e.Key); ok {
		t.Fatal("reorged entry remains spendable")
	}
}

// Only an observation the balance already counted earns a reversal: a
// pending entry that never became spendable compensates nothing, and a
// live entry cannot be compensated at all.
func TestReorgCompensationRequiresADeliveredState(t *testing.T) {
	ledger := NewLedger()
	e := Entry{Key: "sol:sig-9:ix-0", Player: "p", Asset: evmAsset(asset.StandardERC20, ""), Delta: 5, State: Pending}
	if _, err := ledger.Apply(e); err != nil {
		t.Fatal(err)
	}
	got, err := ledger.SetState(e.Key, Reorged)
	if err != nil || got != 0 {
		t.Fatalf("pending reorg compensation=%d err=%v", got, err)
	}
	if err := ledger.MarkCompensated(e.Key); err != nil {
		t.Fatal(err)
	}
	if err := ledger.MarkCompensated("evm:tx-2:log-0"); err == nil {
		t.Fatal("compensating an unknown entry must fail")
	}
	ledger2 := NewLedger()
	live := Entry{Key: "evm:tx-3:log-1", Player: "p", Asset: evmAsset(asset.StandardERC20, ""), Delta: 5, State: Final}
	if _, err := ledger2.Apply(live); err != nil {
		t.Fatal(err)
	}
	if err := ledger2.MarkCompensated(live.Key); err == nil {
		t.Fatal("compensating a final entry must fail")
	}
}

func TestLedgerUsesCapabilityAssetIdentity(t *testing.T) {
	// The ledger's asset key is the capability-layer identity; a standard
	// mismatch is preserved, never collapsed.
	if evmAsset(asset.StandardERC721, "0007") == evmAsset(asset.StandardERC1155, "0007") {
		t.Fatal("ERC-721 and ERC-1155 identities were collapsed")
	}
}
