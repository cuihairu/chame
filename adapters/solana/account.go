package solana

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/core/identifiers"
)

// solDecimals is the native asset's precision in lamports (spec §6.4:
// assets do not share one precision, so every balance carries its own).
const solDecimals = 9

// systemProgramID owns accounts with no program controller: the classic
// externally-owned account (spec §13.1). Every other owner is a program
// that controls the account.
const systemProgramID = "11111111111111111111111111111111"

// nativeAssetID names the network's native asset (SOL) without an
// address: the kind field carries that meaning, never an empty address
// (spec §6.5).
func nativeAssetID(id identifiers.NetworkID) string {
	return identifiers.AssetID{Network: id, Kind: identifiers.KindNative}.String()
}

// AccountProvider implements capability/account.Provider over one
// Solana cluster (spec §13.1). Read-only: nothing here can produce an
// on-chain side effect.
type AccountProvider struct {
	net *Network
}

var _ account.Provider = (*AccountProvider)(nil)

// AccountProvider exposes the account capability for this network.
func (n *Network) AccountProvider() *AccountProvider { return &AccountProvider{net: n} }

// Network implements account.Provider.
func (p *AccountProvider) Network() identifiers.NetworkID { return p.net.id }

// Account reports one address as the chain sees it (spec §13.1). An
// account owned by the system program is externally owned; any other
// owner is a program controller, so OwnerProgram covers accounts whose
// address exists but whose key no one holds (associated-token accounts,
// program data accounts). An address the chain has never seen is not
// an account and is reported as ErrAccountNotFound, not as a record
// with nothing in it. Whether an address's key is held in this process
// is never inferred from the chain: it is answered through
// account.ManagedBySigner from the signer's own allow-list
// (spec §12.3, §13.1).
func (p *AccountProvider) Account(ctx context.Context, address identifiers.Address) (account.Account, error) {
	info, err := p.net.Account(ctx, address, "")
	if err != nil {
		return account.Account{}, err
	}
	if info == nil {
		return account.Account{}, fmt.Errorf("solana: account %s: %w", address.Value, account.ErrAccountNotFound)
	}
	owner := account.OwnerProgram
	if info.Owner == systemProgramID {
		owner = account.OwnerExternal
	}
	lamports := strconv.FormatUint(info.Lamports, 10)
	bal, err := identifiers.NewAmount(lamports, solDecimals, nativeAssetID(p.net.id))
	if err != nil {
		p.net.stats.countError()
		return account.Account{}, err
	}
	return account.Account{
		Network:       p.net.id,
		Address:       address,
		Owner:         owner,
		NativeBalance: &bal,
		Details: []account.Detail{
			{Key: "owner_program", Value: info.Owner},
			{Key: "data_length", Value: strconv.Itoa(len(info.Data))},
			{Key: "executable", Value: strconv.FormatBool(info.Executable)},
			{Key: "rent_epoch", Value: strconv.FormatUint(info.RentEpoch, 10)},
			{Key: "space", Value: strconv.FormatUint(info.Space, 10)},
		},
	}, nil
}

// Balance reports the native-asset balance in lamports (spec §13.1:
// 账户余额). Token balances belong to the asset capability (spec §13.2).
// The chain reports zero for an address that has never been funded
// rather than failing the query.
func (p *AccountProvider) Balance(ctx context.Context, address identifiers.Address) (identifiers.Amount, error) {
	lamports, err := p.net.Balance(ctx, address, "")
	if err != nil {
		return identifiers.Amount{}, err
	}
	return identifiers.NewAmount(strconv.FormatUint(lamports, 10), solDecimals, nativeAssetID(p.net.id))
}

// Healthy reports whether the network connection is usable.
func (p *AccountProvider) Healthy(ctx context.Context) error { return p.net.Healthy(ctx) }
