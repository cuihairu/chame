// Package nft defines read-only ownership queries while preserving each
// chain standard's native result shape.
package nft

import (
	"context"
	"math/big"

	"github.com/cuihairu/chame/application/gameledger"
	"github.com/cuihairu/chame/core/identifiers"
)

type EVM721Ownership struct {
	Owner identifiers.Address
	// False for eth_call at latest; ledger writes still require the host's
	// configured finality/event policy.
	FinalitySatisfied bool
}
type EVM1155Ownership struct {
	RawQuantity       *big.Int
	FinalitySatisfied bool
}
type SolanaOwnership struct {
	Owner          identifiers.Address
	TokenAccount   identifiers.Address
	Held           bool
	Classification SolanaMintClass
	// Account reads do not prove a finalized transfer for the game ledger.
	FinalitySatisfied bool
}
type SolanaMintClass string

const (
	SolanaMetaplexNFT SolanaMintClass = "metaplex-nft"
	SolanaFungibleSPL SolanaMintClass = "fungible-spl"
)

// Provider exposes standard-specific chain observations. These are facts,
// not ledger entries; consumers must apply their configured finality policy.
type EVMProvider interface {
	ERC721Owner(context.Context, gameledger.AssetIdentity) (EVM721Ownership, error)
	ERC1155Balance(context.Context, gameledger.AssetIdentity, identifiers.Address) (EVM1155Ownership, error)
}
type SolanaProvider interface {
	SolanaHolding(context.Context, gameledger.AssetIdentity, identifiers.Address) (SolanaOwnership, error)
}
