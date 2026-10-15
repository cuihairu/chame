package evm

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/capability/asset"
	"github.com/cuihairu/chame/capability/network"
	"github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
	conformance "github.com/cuihairu/chame/tests/conformance/adapter"
)

// emptyHolder is an address the fixture's endpoint reports as unfunded,
// so the suite can pin the zero-balance contract alongside the funded
// holder.
const emptyHolder = "0x00000000000000000000000000000000000000bb"

// The status seam's chain references: one settled execution, one failed
// execution, one the node holds no record of.
const (
	settledTxHash    = "0x2222222222222222222222222222222222222222222222222222222222222222"
	revertedTxHash   = "0x3333333333333333333333333333333333333333333333333333333333333333"
	unobservedTxHash = "0x4444444444444444444444444444444444444444444444444444444444444444"
)

// solVoteAccount is a valid Solana address, used as the foreign-family
// address the EVM adapter must reject.
const solVoteAccount = "Vote111111111111111111111111111111111111111"

// evmSubject adapts one EVM network to the shared conformance suite.
type evmSubject struct {
	net  *Network
	fake *fakeClient
}

func (s *evmSubject) Net() network.Provider      { return s.net }
func (s *evmSubject) Accounts() account.Provider { return s.net.AccountProvider() }
func (s *evmSubject) Assets() asset.Provider     { return s.net.AssetProvider() }

func (s *evmSubject) StatusOf(ctx context.Context, tx transaction.Transaction) (transaction.TransactionStatus, error) {
	return s.net.StatusOf(ctx, tx)
}

func (s *evmSubject) Stop(_ context.Context) error {
	s.net.close()
	return nil
}

func (s *evmSubject) Break() {
	s.fake.breakEndpoints()
}

// TestAdapterConformance runs the shared adapter suite (spec §16.2) over
// the EVM adapter. The suite defines the semantics every adapter must
// share; chain-specific behavior stays in this package's own tests.
func TestAdapterConformance(t *testing.T) {
	conformance.Run(t, evmFixture())
}

func evmFixture() conformance.Fixture {
	return conformance.Fixture{
		Name:             "evm",
		Family:           identifiers.FamilyEVM,
		New:              newEVMSubject,
		NativeDecimals:   18,
		NativeBalance:    "2500000",
		KnownAddress:     weiHolder,
		MissingAddress:   emptyHolder,
		MalformedAddress: "0x",
		ForeignAddress:   identifiers.Address{Family: identifiers.FamilySolana, Value: solVoteAccount},
		ForeignKindAsset: identifiers.AssetID{
			Network: netID("mainnet", 1),
			Kind:    identifiers.KindSPLToken,
			Address: erc20Addr,
		},
		ReportsAccountNotFound: false,
		NativeDetails:          []string{"has_code", "code_size"},
		Token: &conformance.TokenFixture{
			AssetID:  evmAsset(identifiers.KindEVMToken, erc20Addr),
			Holder:   weiHolder,
			Decimals: 6,
			Balance:  "42",
		},
		Status: &conformance.StatusFixture{
			// A receipt the node holds is as far as the family observes:
			// inclusion, not confirmation.
			Settled:       settledTxHash,
			SettledStatus: transaction.TxIncluded,
			Reverted:      revertedTxHash,
			Unobserved:    unobservedTxHash,
		},
	}
}

func newEVMSubject(t *testing.T) conformance.Subject {
	t.Helper()
	fake := newFakeClient(1)
	fake.balance = big.NewInt(2_500_000)
	fake.withBalance(common.HexToAddress(emptyHolder), big.NewInt(0))
	fake.withCall(decimalsSel, word32(6))
	fake.callReply = word32(42)
	fake.withReceipt(settledTxHash, &types.Receipt{
		Status:      types.ReceiptStatusSuccessful,
		BlockNumber: big.NewInt(42),
	})
	fake.withReceipt(revertedTxHash, &types.Receipt{
		Status:      types.ReceiptStatusFailed,
		BlockNumber: big.NewInt(42),
	})
	return &evmSubject{net: testNetwork(t, fake), fake: fake}
}
