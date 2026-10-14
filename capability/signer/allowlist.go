package signer

import (
	"fmt"

	"github.com/cuihairu/chame/core/identifiers"
)

// AccountRef is one allowed (network, account) pair (spec §12.3: 签名器
// 应能限制可用网络和账户). The address family must match the network
// family: an EVM key never signs for a Solana network.
type AccountRef struct {
	Network identifiers.NetworkID
	Address identifiers.Address
}

// NewAccountRef builds and validates one reference.
func NewAccountRef(net identifiers.NetworkID, addr identifiers.Address) (AccountRef, error) {
	ref := AccountRef{Network: net, Address: addr}
	if err := ref.Validate(); err != nil {
		return AccountRef{}, err
	}
	return ref, nil
}

// Validate checks both identifiers and their family agreement.
func (a AccountRef) Validate() error {
	if err := a.Network.Validate(); err != nil {
		return fmt.Errorf("signer: account ref network: %w", err)
	}
	if err := a.Address.Validate(); err != nil {
		return fmt.Errorf("signer: account ref address: %w", err)
	}
	if a.Address.Family != a.Network.Family {
		return fmt.Errorf("signer: account ref family %s does not match network family %s",
			a.Address.Family, a.Network.Family)
	}
	return nil
}

// AllowList is the set of (network, account) pairs one signer may sign
// for (spec §12.3). An empty list allows nothing: a signer without
// configured accounts refuses every request rather than signing whatever
// it is asked — business modules never gain signing rights implicitly
// (spec §12.3: 游戏业务模块不应自动获得所有签名权限).
type AllowList []AccountRef

// Validate rejects malformed references and duplicates.
func (l AllowList) Validate() error {
	seen := map[AccountRef]bool{}
	for i, ref := range l {
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("signer: allow list entry %d: %w", i, err)
		}
		if seen[ref] {
			return fmt.Errorf("signer: allow list entry %d duplicates %s %s", i, ref.Network, ref.Address)
		}
		seen[ref] = true
	}
	return nil
}

// Authorize checks the request against the list: the exact network and
// the exact account must both appear. Anything else is ErrUnauthorized.
func (l AllowList) Authorize(req Request) error {
	for _, ref := range l {
		if ref.Network.Equal(req.Network) && ref.Address.Equal(req.Account) {
			return nil
		}
	}
	return fmt.Errorf("%w: network %s account %s", ErrUnauthorized, req.Network, req.Account)
}

// Accounts returns a copy of the list, for the Signer interface.
func (l AllowList) Accounts() []AccountRef {
	out := make([]AccountRef, len(l))
	copy(out, l)
	return out
}
