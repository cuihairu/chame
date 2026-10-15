package asset

import (
	"context"
	"fmt"

	"github.com/cuihairu/chame/core/identifiers"
)

// Detail is one chain-specific asset attribute (spec §13.2: 资产精度和
// 元数据查询). Adapters fill it with what their chain reports — mint
// authority presence on Solana, protocol version on EVM — and consumers
// read it opaquely: the framework never normalizes asset metadata
// across chains.
type Detail struct {
	Key   string
	Value string
}

// AssetInfo is the metadata an adapter reports for one asset
// (spec §13.2). Symbol and Name come from the chain when the chain
// provides them; an empty symbol is not an error — native assets and
// programs without metadata legitimately have none.
type AssetInfo struct {
	// Asset is the identifier the info describes.
	Asset identifiers.AssetID
	// Symbol is the chain-reported symbol, empty when there is none.
	Symbol string
	// Name is the chain-reported name, empty when there is none.
	Name string
	// Decimals is the asset's precision in the chain's smallest unit
	// (spec §6.4): assets do not share one precision, so every balance
	// carries its own.
	Decimals uint8
	// Details are the chain-specific attributes.
	Details []Detail
}

// Validate checks the asset identifier, the decimals bound, and any
// carried detail keys.
func (a AssetInfo) Validate() error {
	if err := a.Asset.Validate(); err != nil {
		return fmt.Errorf("asset: %w", err)
	}
	if a.Decimals > 36 {
		return fmt.Errorf("asset: decimals %d out of range", a.Decimals)
	}
	seen := map[string]bool{}
	for _, d := range a.Details {
		if d.Key == "" {
			return fmt.Errorf("asset: detail key must not be empty")
		}
		if seen[d.Key] {
			return fmt.Errorf("asset: detail %q is listed twice", d.Key)
		}
		seen[d.Key] = true
	}
	return nil
}

// Provider is the asset capability (spec §13.2): read-only metadata and
// balances over one connected network. It must not write or transfer
// anything on-chain; transfers go through the transaction capability
// (spec §13.2, §13.3).
type Provider interface {
	// Network returns the logical network identity.
	Network() identifiers.NetworkID
	// Asset reports one asset's precision and metadata
	// (§13.2: 资产精度和元数据查询, 资产标识与网络标识关联).
	Asset(ctx context.Context, id identifiers.AssetID) (AssetInfo, error)
	// Balance returns holder's balance of id, in the asset's smallest
	// unit with the asset's decimals (§13.2: 原生资产余额、EVM Token
	// 余额、Solana SPL Token 余额). For SPL tokens the holder address is
	// the token account: adapters do not resolve an owner wallet to an
	// associated token account.
	Balance(ctx context.Context, id identifiers.AssetID, holder identifiers.Address) (identifiers.Amount, error)
	// Healthy reports whether the network connection is usable.
	Healthy(ctx context.Context) error
}
