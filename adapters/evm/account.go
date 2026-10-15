package evm

import (
	"context"
	"strconv"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/core/identifiers"
)

// ethDecimals is the native asset's precision in wei (spec §6.4: assets
// do not share one precision, so every balance carries its own).
const ethDecimals = 18

// nativeAssetID names the network's native asset (ETH) without an
// address: the kind field carries that meaning, never an empty address
// (spec §6.5).
func nativeAssetID(id identifiers.NetworkID) string {
	return identifiers.AssetID{Network: id, Kind: identifiers.KindNative}.String()
}

// AccountProvider implements capability/account.Provider over one EVM
// network (spec §13.1).
type AccountProvider struct {
	net *Network
}

var _ account.Provider = (*AccountProvider)(nil)

// AccountProvider exposes the account capability for this network.
func (n *Network) AccountProvider() *AccountProvider { return &AccountProvider{net: n} }

// Network implements account.Provider.
func (p *AccountProvider) Network() identifiers.NetworkID { return p.net.id }

// Account reports one address as the chain sees it (spec §13.1).
// Code-bearing addresses are contracts — or delegated signers, which
// present the same way — and addresses without code are externally
// owned. EVM has no program-derived accounts, so OwnerProgram is never
// reported here. Whether an address's key is held in this process is
// never inferred from the chain: it is answered through
// account.ManagedBySigner from the signer's own allow-list
// (spec §12.3, §13.1).
func (p *AccountProvider) Account(ctx context.Context, address identifiers.Address) (account.Account, error) {
	if _, err := toCommon(p.net.opBase+".account", address); err != nil {
		p.net.stats.countError()
		return account.Account{}, err
	}
	wei, err := p.net.BalanceAt(ctx, address)
	if err != nil {
		return account.Account{}, err
	}
	code, err := p.net.CodeAt(ctx, address)
	if err != nil {
		return account.Account{}, err
	}
	bal, err := identifiers.NewAmount(wei.String(), ethDecimals, nativeAssetID(p.net.id))
	if err != nil {
		p.net.stats.countError()
		return account.Account{}, err
	}
	hasCode := len(code) > 0
	owner := account.OwnerExternal
	if hasCode {
		owner = account.OwnerContract
	}
	return account.Account{
		Network:       p.net.id,
		Address:       address,
		Owner:         owner,
		NativeBalance: &bal,
		Details: []account.Detail{
			{Key: "has_code", Value: strconv.FormatBool(hasCode)},
			{Key: "code_size", Value: strconv.Itoa(len(code))},
		},
	}, nil
}

// Balance reports the native-asset balance in wei (spec §13.1: 账户
// 余额). Token balances belong to the asset capability (spec §13.2).
func (p *AccountProvider) Balance(ctx context.Context, address identifiers.Address) (identifiers.Amount, error) {
	wei, err := p.net.BalanceAt(ctx, address)
	if err != nil {
		return identifiers.Amount{}, err
	}
	return identifiers.NewAmount(wei.String(), ethDecimals, nativeAssetID(p.net.id))
}

// Healthy reports whether the network connection is usable.
func (p *AccountProvider) Healthy(ctx context.Context) error { return p.net.Healthy(ctx) }
