// Package adapter holds the adapter conformance suite (spec §16.2): one
// suite that every chain adapter must pass, so EVM and Solana behave
// identically on the semantics the public capability contracts define —
// network identity, family address validation, exact amounts, explicit
// unsupported capabilities, closed-adapter rejection, context
// cancellation, and the transaction-status seam the recovery worker
// drives. Chain-specific behavior stays in each adapter's own tests;
// semantics shared by every adapter live here exactly once.
package adapter

import (
	"context"
	"testing"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/capability/asset"
	"github.com/cuihairu/chame/capability/network"
	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
)

// Subject is one connected adapter network under test. The three views
// are the capability contracts the framework publishes; Stop is the
// adapter's shutdown path and Break is its failure path.
type Subject interface {
	// Net is the network capability view.
	Net() network.Provider
	// Accounts is the account capability view.
	Accounts() account.Provider
	// Assets is the asset capability view.
	Assets() asset.Provider
	// StatusOf reports the strongest state the chain currently shows for
	// one recorded transaction — the reconciliation seam the recovery
	// worker drives (spec §7.8).
	StatusOf(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error)
	// Stop shuts the adapter down. Requests after Stop must be rejected.
	Stop(ctx context.Context) error
	// Break makes the adapter's endpoints fail, so the suite can reach
	// the unreachable-network paths.
	Break()
}

// TokenFixture describes one token asset the fixture's endpoint serves.
type TokenFixture struct {
	// AssetID is the token asset identifier, pinned to the fixture's
	// network.
	AssetID identifiers.AssetID
	// Holder is the address the endpoint reports a balance for.
	Holder string
	// Decimals is the precision the chain reports for the token.
	Decimals uint8
	// Balance is the exact balance in the token's smallest unit.
	Balance string
}

// StatusFixture names the chain references the fixture's endpoint serves
// for the reconciliation seam (spec §7.8): the statuses each must map to
// are pinned per family, because the families observe depth differently.
type StatusFixture struct {
	// Settled is a chain reference whose execution the endpoint reports
	// as successful.
	Settled string
	// SettledStatus is the status the adapter must report for the
	// settled reference: the strongest state the family can observe for
	// a succeeded execution.
	SettledStatus transaction.TransactionStatus
	// Reverted is a chain reference whose execution the endpoint reports
	// as failed.
	Reverted string
	// Unobserved is a chain reference the endpoint holds no record of.
	Unobserved string
}

// Fixture is the per-adapter wiring the shared suite needs. Everything
// it names must be served by the fixture's fake endpoints: the suite
// never touches a real chain (spec §16.3).
type Fixture struct {
	// Name identifies the adapter under test, e.g. "evm".
	Name string
	// Family is the adapter's address family.
	Family identifiers.ChainFamily
	// New returns a fresh subject wired to fake endpoints.
	New func(t *testing.T) Subject
	// NativeDecimals is the family's native precision (spec §6.4).
	NativeDecimals uint8
	// NativeBalance is the exact native balance reported for
	// KnownAddress, in the smallest unit.
	NativeBalance string
	// KnownAddress is a resolvable address of the family.
	KnownAddress string
	// MissingAddress is a resolvable address of the family the endpoint
	// has never seen: the fixture must make the endpoint report it as
	// absent, so the suite can pin the ErrAccountNotFound contract.
	MissingAddress string
	// MalformedAddress is a string of the right family that must not
	// parse as an address.
	MalformedAddress string
	// ForeignAddress is a valid address of another family.
	ForeignAddress identifiers.Address
	// ForeignKindAsset is an asset id with a kind that belongs to the
	// other family but a network of this one: the adapter must answer
	// with an explicit unsupported error, not a chain call.
	ForeignKindAsset identifiers.AssetID
	// ReportsAccountNotFound: chains with an account-existence concept
	// (Solana) return account.ErrAccountNotFound for a never-seen
	// address; chains without one (EVM) report zero instead.
	ReportsAccountNotFound bool
	// NativeDetails are detail keys the account record must carry.
	NativeDetails []string
	// Token is the optional token fixture; a family without tokens
	// (none today) leaves it nil.
	Token *TokenFixture
	// Status is the optional reconciliation-seam fixture; nil leaves the
	// seam out of the run.
	Status *StatusFixture
}

// Run executes the full adapter suite for one fixture.
func Run(t *testing.T, f Fixture) {
	t.Helper()
	t.Run("NetworkIdentity", func(t *testing.T) { runNetworkIdentity(t, f) })
	t.Run("AddressFamilySemantics", func(t *testing.T) { runAddressFamily(t, f) })
	t.Run("NativeBalancePrecision", func(t *testing.T) { runNativeBalance(t, f) })
	t.Run("AccountRecord", func(t *testing.T) { runAccountRecord(t, f) })
	t.Run("MissingAccount", func(t *testing.T) { runMissingAccount(t, f) })
	t.Run("NativeAssetMetadata", func(t *testing.T) { runNativeAsset(t, f) })
	if f.Token != nil {
		t.Run("TokenSemantics", func(t *testing.T) { runToken(t, f) })
	}
	t.Run("UnsupportedAssetKind", func(t *testing.T) { runForeignKind(t, f) })
	if f.Status != nil {
		t.Run("StatusSeam", func(t *testing.T) { runStatusSeam(t, f) })
	}
	t.Run("HealthyReportsUnreachable", func(t *testing.T) { runUnhealthy(t, f) })
	t.Run("ClosedAdapterRejectsRequests", func(t *testing.T) { runClosed(t, f) })
	t.Run("CancelledContextPropagates", func(t *testing.T) { runCancelled(t, f) })
}

// nativeAssetID builds the native asset id of one network.
func nativeAssetID(id identifiers.NetworkID) identifiers.AssetID {
	return identifiers.AssetID{Network: id, Kind: identifiers.KindNative}
}

// mustAddress parses a fixture address of the adapter's family.
func mustAddress(t *testing.T, family identifiers.ChainFamily, value string) identifiers.Address {
	t.Helper()
	addr, err := identifiers.NewAddress(family, value)
	if err != nil {
		t.Fatalf("fixture address %q: %v", value, err)
	}
	return addr
}
