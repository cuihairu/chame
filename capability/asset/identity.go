package asset

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/cuihairu/chame/core/identifiers"
)

// Standard preserves the token standard in the identity. Similar-looking
// assets under different standards must never be collapsed into one key.
type Standard string

const (
	StandardNative  Standard = "native"
	StandardERC20   Standard = "erc20"
	StandardERC721  Standard = "erc721"
	StandardERC1155 Standard = "erc1155"
	StandardSPL     Standard = "spl-token"
	StandardSPLNFT  Standard = "spl-nft"
)

// AssetIdentity is chain-qualified. Contract is an EVM contract or Solana
// mint; TokenID is required only for token-id standards. TokenID is a
// canonical unsigned decimal string, avoiding integer-width assumptions.
//
// It is the asset identity both adapters and the game-ledger reference
// contract share (design: game-asset-ownership-and-ledger-boundary). It
// lives in the capability layer because adapters depend on it; a host
// ledger uses it as its asset key.
type AssetIdentity struct {
	Network  identifiers.NetworkID `json:"network"`
	Standard Standard              `json:"standard"`
	Contract string                `json:"contract,omitempty"`
	TokenID  string                `json:"token_id,omitempty"`
}

// Normalize validates and returns a canonical identity. EVM addresses are
// lowercased; Solana base58 values remain case-sensitive and unchanged.
func (a AssetIdentity) Normalize() (AssetIdentity, error) {
	if err := a.Network.Validate(); err != nil {
		return AssetIdentity{}, err
	}
	switch a.Standard {
	case StandardNative:
		if a.Contract != "" || a.TokenID != "" {
			return AssetIdentity{}, errors.New("native identity must not carry contract or token id")
		}
	case StandardERC20, StandardERC721, StandardERC1155:
		if a.Network.Family != identifiers.FamilyEVM {
			return AssetIdentity{}, errors.New("EVM standard requires EVM network")
		}
		if a.Contract == "" {
			return AssetIdentity{}, errors.New("contract is required")
		}
		addr, err := identifiers.NewEVMAddress(a.Contract)
		if err != nil {
			return AssetIdentity{}, err
		}
		a.Contract = "0x" + strings.ToLower(strings.TrimPrefix(addr.Value, "0x"))
		if a.Standard == StandardERC20 && a.TokenID != "" {
			return AssetIdentity{}, errors.New("ERC-20 has no token id")
		}
		if a.Standard == StandardERC721 || a.Standard == StandardERC1155 {
			a.TokenID, err = canonicalUint(a.TokenID)
			if err != nil {
				return AssetIdentity{}, err
			}
		}
	case StandardSPL, StandardSPLNFT:
		if a.Network.Family != identifiers.FamilySolana {
			return AssetIdentity{}, errors.New("SPL standard requires Solana network")
		}
		if a.Contract == "" {
			return AssetIdentity{}, errors.New("mint is required")
		}
		if _, err := identifiers.NewSolanaAddress(a.Contract); err != nil {
			return AssetIdentity{}, err
		}
		if a.Standard == StandardSPL && a.TokenID != "" {
			return AssetIdentity{}, errors.New("fungible SPL identity has no token id")
		}
		if a.Standard == StandardSPLNFT {
			var err error
			a.TokenID, err = canonicalUint(a.TokenID)
			if err != nil {
				return AssetIdentity{}, err
			}
		}
	default:
		return AssetIdentity{}, fmt.Errorf("unsupported asset standard %q", a.Standard)
	}
	return a, nil
}

func canonicalUint(s string) (string, error) {
	if s == "" {
		return "", errors.New("token id is required")
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return "", fmt.Errorf("token id must be an unsigned decimal integer")
	}
	return n.String(), nil
}
