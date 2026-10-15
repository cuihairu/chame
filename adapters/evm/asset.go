package evm

import (
	"context"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/cuihairu/chame/capability/asset"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// ERC-20 metadata selectors, computed from the function signatures so a
// typo cannot silently produce a wrong selector.
var (
	decimalsSelector = selectorCall(keccak4("decimals()"))
	symbolSelector   = selectorCall(keccak4("symbol()"))
	nameSelector     = selectorCall(keccak4("name()"))
)

func keccak4(sig string) [4]byte {
	sum := crypto.Keccak256([]byte(sig))
	var out [4]byte
	copy(out[:], sum[:4])
	return out
}

// AssetProvider implements capability/asset.Provider over one EVM
// network (spec §13.2). Every method is read-only: no call here can
// produce an on-chain side effect.
type AssetProvider struct {
	net *Network
}

var _ asset.Provider = (*AssetProvider)(nil)

// AssetProvider exposes the asset capability for this network.
func (n *Network) AssetProvider() *AssetProvider { return &AssetProvider{net: n} }

// Network implements asset.Provider.
func (p *AssetProvider) Network() identifiers.NetworkID { return p.net.id }

// Asset reports one asset's precision and metadata (spec §13.2). The
// native asset carries no symbol or name — neither is an error. For an
// ERC-20 token, decimals is required: without precision no balance can
// be expressed (spec §6.4). symbol and name are optional in ERC-20, so
// a contract that does not implement them reports an empty value rather
// than failing the query.
func (p *AssetProvider) Asset(ctx context.Context, id identifiers.AssetID) (asset.AssetInfo, error) {
	if err := p.checkAsset(id); err != nil {
		return asset.AssetInfo{}, err
	}
	if id.Kind == identifiers.KindNative {
		return asset.AssetInfo{
			Asset:    id,
			Decimals: ethDecimals,
			Details:  []asset.Detail{{Key: "base_unit", Value: "wei"}},
		}, nil
	}
	op := p.net.opBase + ".asset"
	decimals, err := p.tokenUint8(ctx, id.Address, decimalsSelector, op+".decimals")
	if err != nil {
		return asset.AssetInfo{}, err
	}
	return asset.AssetInfo{
		Asset:    id,
		Symbol:   p.tokenString(ctx, id.Address, symbolSelector, op+".symbol"),
		Name:     p.tokenString(ctx, id.Address, nameSelector, op+".name"),
		Decimals: decimals,
	}, nil
}

// Balance reports holder's balance of id in the asset's smallest unit,
// stamped with that asset's own precision (spec §13.2, §6.4). For an
// ERC-20 token the holder is the token holder address the caller names;
// the adapter performs no approval or transfer.
func (p *AssetProvider) Balance(ctx context.Context, id identifiers.AssetID, holder identifiers.Address) (identifiers.Amount, error) {
	if err := p.checkAsset(id); err != nil {
		return identifiers.Amount{}, err
	}
	if _, err := toCommon(p.net.opBase+".asset_balance", holder); err != nil {
		p.net.stats.countError()
		return identifiers.Amount{}, err
	}
	if id.Kind == identifiers.KindNative {
		wei, err := p.net.BalanceAt(ctx, holder)
		if err != nil {
			return identifiers.Amount{}, err
		}
		return identifiers.NewAmount(wei.String(), ethDecimals, id.String())
	}
	token, err := identifiers.NewEVMAddress(id.Address)
	if err != nil {
		p.net.stats.countError()
		return identifiers.Amount{}, cherrs.New(cherrs.InvalidArgument, p.net.opBase+".asset_balance", "%v", err)
	}
	op := p.net.opBase + ".asset_balance"
	decimals, err := p.tokenUint8(ctx, id.Address, decimalsSelector, op+".decimals")
	if err != nil {
		return identifiers.Amount{}, err
	}
	raw, err := p.net.TokenBalance(ctx, token, holder)
	if err != nil {
		return identifiers.Amount{}, err
	}
	return identifiers.NewAmount(raw.String(), decimals, id.String())
}

// Healthy reports whether the network connection is usable.
func (p *AssetProvider) Healthy(ctx context.Context) error { return p.net.Healthy(ctx) }

// checkAsset validates the asset identifier and pins it to this network.
// The kind is part of the asset identity: a kind this family does not
// serve is an explicit unsupported error on every path, never a chain
// call (spec §16.2).
func (p *AssetProvider) checkAsset(id identifiers.AssetID) error {
	if err := id.Validate(); err != nil {
		return cherrs.New(cherrs.InvalidArgument, p.net.opBase+".asset", "%v", err)
	}
	if !id.Network.Equal(p.net.id) {
		return cherrs.New(cherrs.InvalidArgument, p.net.opBase+".asset",
			"asset network %s is not this network %s", id.Network, p.net.id)
	}
	if id.Kind != identifiers.KindNative && id.Kind != identifiers.KindEVMToken {
		return cherrs.New(cherrs.UnsupportedCapability, p.net.opBase+".asset",
			"asset kind %q is not served by the evm adapter", id.Kind)
	}
	return nil
}

// tokenCall performs one no-argument call against a token contract.
func (p *AssetProvider) tokenCall(ctx context.Context, token string, sel []byte, op string) ([]byte, error) {
	addr, err := identifiers.NewEVMAddress(token)
	if err != nil {
		p.net.stats.countError()
		return nil, cherrs.New(cherrs.InvalidArgument, op, "%v", err)
	}
	return p.net.Call(ctx, CallQuery{To: addr, Data: sel})
}

// tokenUint8 decodes a token call that returns one uint8-sized value.
func (p *AssetProvider) tokenUint8(ctx context.Context, token string, sel []byte, op string) (uint8, error) {
	out, err := p.tokenCall(ctx, token, sel, op)
	if err != nil {
		return 0, err
	}
	v, err := abiUint8(op, out)
	if err != nil {
		p.net.stats.countError()
		return 0, err
	}
	return v, nil
}

// tokenString decodes a token call that returns one string. A contract
// that does not implement the function reports no value, not an error.
func (p *AssetProvider) tokenString(ctx context.Context, token string, sel []byte, op string) string {
	out, err := p.tokenCall(ctx, token, sel, op)
	if err != nil {
		return ""
	}
	s, err := abiString(op, out)
	if err != nil {
		return ""
	}
	return s
}
