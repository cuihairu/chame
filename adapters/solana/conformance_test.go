package solana

import (
	"context"
	"math/big"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/capability/asset"
	"github.com/cuihairu/chame/capability/network"
	"github.com/cuihairu/chame/core/identifiers"
	conformance "github.com/cuihairu/chame/tests/conformance/adapter"
)

// foreignEVMHolder is a valid EVM address, used as the foreign-family
// address the Solana adapter must reject.
const foreignEVMHolder = "0x1234567890123456789012345678901234567890"

// solanaSubject adapts one Solana cluster to the shared conformance
// suite.
type solanaSubject struct {
	net  *Network
	fake *fakeClient
}

func (s *solanaSubject) Net() network.Provider      { return s.net }
func (s *solanaSubject) Accounts() account.Provider { return s.net.AccountProvider() }
func (s *solanaSubject) Assets() asset.Provider     { return s.net.AssetProvider() }

func (s *solanaSubject) Stop(_ context.Context) error {
	s.net.close()
	return nil
}

func (s *solanaSubject) Break() {
	s.fake.fail("genesis_hash", netErr{msg: "connection refused"})
}

// TestAdapterConformance runs the shared adapter suite (spec §16.2) over
// the Solana adapter. The suite defines the semantics every adapter must
// share; chain-specific behavior stays in this package's own tests.
func TestAdapterConformance(t *testing.T) {
	conformance.Run(t, solanaFixture())
}

func solanaFixture() conformance.Fixture {
	return conformance.Fixture{
		Name:             "solana",
		Family:           identifiers.FamilySolana,
		New:              newSolanaSubject,
		NativeDecimals:   9,
		NativeBalance:    "2500000",
		KnownAddress:     voteAccount,
		MissingAddress:   sysAccount,
		MalformedAddress: "nope",
		ForeignAddress:   identifiers.Address{Family: identifiers.FamilyEVM, Value: foreignEVMHolder},
		ForeignKindAsset: identifiers.AssetID{
			Network: clusterID("mainnet", testGenesis(0x10)),
			Kind:    identifiers.KindEVMToken,
			Address: sysAccount,
		},
		ReportsAccountNotFound: true,
		NativeDetails:          []string{"owner_program", "data_length", "executable", "rent_epoch", "space"},
		Token: &conformance.TokenFixture{
			AssetID:  solAsset(identifiers.KindSPLToken, mintAddress),
			Holder:   voteAccount,
			Decimals: 6,
			Balance:  "42",
		},
	}
}

func newSolanaSubject(t *testing.T) conformance.Subject {
	t.Helper()
	fake := newFakeClient(solanaHashFor(0x10))
	fake.lamports = 2_500_000
	// The node serves one account for the queried address; the fixture
	// reports an SPL mint so the token subtests read real mint data.
	fake.account = &accountResult{Value: &rpc.Account{
		Lamports:   2_500_000,
		Owner:      solana.MPK(tokenProgram),
		Data:       rpc.DataBytesOrJSONFromBytes(mintData(6, 1, 1_000_000, true)),
		Executable: false,
		RentEpoch:  new(big.Int).SetInt64(3),
		Space:      mintLayoutSize,
	}}
	fake.tokenBalance = &tokenBalanceResult{Value: &rpc.UiTokenAmount{Amount: "42", Decimals: 6}}
	fake.withMissing(solana.MustPublicKeyFromBase58(sysAccount))
	return &solanaSubject{net: newTestNetwork(fake), fake: fake}
}
