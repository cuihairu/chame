package account

import (
	"context"
	"fmt"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/identifiers"
)

// OwnerKind says what controls an address (spec §13.1: 不要把"地址"与
// "私钥账户"视为同一个概念；某些链上账户可以是外部拥有账户、程序控制
// 账户或合约账户). OwnerUnknown is explicit: an adapter that cannot
// tell keeps the uncertainty instead of guessing.
type OwnerKind string

const (
	// OwnerExternal marks an account whose keys live outside this
	// process: a wallet, a KMS, an MPC or hosted signing service.
	OwnerExternal OwnerKind = "external"
	// OwnerHeldKey marks an account whose key this process holds
	// (development signers).
	OwnerHeldKey OwnerKind = "held-key"
	// OwnerContract marks an account controlled by a smart contract or a
	// delegated signer rather than a key.
	OwnerContract OwnerKind = "contract"
	// OwnerProgram marks a program-derived account such as a Solana
	// associated-token account: the address exists, no key does.
	OwnerProgram OwnerKind = "program"
	// OwnerUnknown marks an address whose kind the adapter cannot
	// determine from the chain.
	OwnerUnknown OwnerKind = "unknown"
)

// Valid reports whether k is a known owner kind.
func (k OwnerKind) Valid() bool {
	switch k {
	case OwnerExternal, OwnerHeldKey, OwnerContract, OwnerProgram, OwnerUnknown:
		return true
	default:
		return false
	}
}

// Detail is one chain-specific account attribute (spec §13.1: 链特有的
// 账户信息). Adapters fill it with what their chain reports — data
// length and owner program on Solana, code presence on EVM — and
// consumers read it opaquely: the framework never normalizes it.
type Detail struct {
	Key   string
	Value string
}

// Account is one on-chain account as an adapter reports it (spec §13.1).
type Account struct {
	// Network pins the record to one logical network.
	Network identifiers.NetworkID
	// Address is the account address.
	Address identifiers.Address
	// Owner classifies what controls the address.
	Owner OwnerKind
	// NativeBalance is the native-asset balance when the adapter reports
	// it with the account (§13.1: 账户余额). Nil when the adapter has no
	// balance to report for this address; callers use Balance.
	NativeBalance *identifiers.Amount
	// Details are the chain-specific attributes.
	Details []Detail
}

// Validate checks the identifiers, their family agreement, the owner
// kind, and any carried balance.
func (a Account) Validate() error {
	if err := a.Network.Validate(); err != nil {
		return fmt.Errorf("account: network: %w", err)
	}
	if err := a.Address.Validate(); err != nil {
		return fmt.Errorf("account: address: %w", err)
	}
	if a.Address.Family != a.Network.Family {
		return fmt.Errorf("account: address family %s does not match network family %s",
			a.Address.Family, a.Network.Family)
	}
	if !a.Owner.Valid() {
		return fmt.Errorf("account: unknown owner kind %q", a.Owner)
	}
	if a.NativeBalance != nil {
		if err := a.NativeBalance.Validate(); err != nil {
			return fmt.Errorf("account: native balance: %w", err)
		}
	}
	seen := map[string]bool{}
	for _, d := range a.Details {
		if d.Key == "" {
			return fmt.Errorf("account: detail key must not be empty")
		}
		if seen[d.Key] {
			return fmt.Errorf("account: detail %q is listed twice", d.Key)
		}
		seen[d.Key] = true
	}
	return nil
}

// Provider is the account capability (spec §13.1): read-only account
// queries over one connected network. It reports addresses and balances;
// it never holds keys, and asking whether an address is signer-managed
// is answered through ManagedBySigner, from the signer's allow-list.
type Provider interface {
	// Network returns the logical network identity.
	Network() identifiers.NetworkID
	// Account queries one address (§13.1: 账户查询), including chain-
	// specific details.
	Account(ctx context.Context, address identifiers.Address) (Account, error)
	// Balance returns the native-asset balance of one address
	// (§13.1: 账户余额). Token balances belong to the asset capability
	// (spec §13.2).
	Balance(ctx context.Context, address identifiers.Address) (identifiers.Amount, error)
	// Healthy reports whether the network connection is usable.
	Healthy(ctx context.Context) error
}

// ManagedBySigner reports whether the given signer manages the address's
// keys (spec §13.1: 账户是否由某个 Signer 管理). The answer comes from
// the signer's own allow-list (spec §12.3), not from any claim in an
// Account record: an address is signer-managed exactly when it appears
// in that signer's permitted accounts.
func ManagedBySigner(s signer.Signer, address identifiers.Address) (bool, error) {
	if s == nil {
		return false, fmt.Errorf("account: signer must not be nil")
	}
	if err := address.Validate(); err != nil {
		return false, fmt.Errorf("account: %w", err)
	}
	for _, ref := range s.Accounts() {
		if ref.Address.Equal(address) {
			return true, nil
		}
	}
	return false, nil
}
