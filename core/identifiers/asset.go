package identifiers

import "fmt"

// Asset kinds (spec §6.5). Kind distinguishes native assets from token
// contracts and programs; the empty address is never overloaded to mean
// native — that is what KindNative is for.
const (
	// KindNative is the chain's native asset (ETH, SOL).
	KindNative = "native"
	// KindEVMToken is an EVM token contract (ERC-20/721/1155).
	KindEVMToken = "evm-token"
	// KindSPLToken is a Solana SPL token mint.
	KindSPLToken = "spl-token"
)

// AssetID locates one on-chain asset (spec §6.5). Native assets use
// KindNative with an empty Address; token kinds require an address whose
// family rules apply.
type AssetID struct {
	Network NetworkID `json:"network"`
	Kind    string    `json:"kind"`
	Address string    `json:"address"`
}

// Validate checks the network, kind, and address rules. Token addresses
// are validated by family; native assets carry no address.
func (a AssetID) Validate() error {
	if err := a.Network.Validate(); err != nil {
		return err
	}
	switch a.Kind {
	case KindNative:
		if a.Address != "" {
			return fmt.Errorf("asset %s: native asset must not carry an address", a.Network)
		}
	case KindEVMToken, KindSPLToken:
		if a.Address == "" {
			return fmt.Errorf("asset %s: %s requires an address", a.Network, a.Kind)
		}
		if _, err := NewAddress(a.Network.Family, a.Address); err != nil {
			return fmt.Errorf("asset %s: %w", a.Network, err)
		}
	default:
		return fmt.Errorf("asset %s: unknown kind %q", a.Network, a.Kind)
	}
	return nil
}

// String returns a compact form for logs.
func (a AssetID) String() string {
	if a.Address == "" {
		return fmt.Sprintf("%s:%s", a.Network, a.Kind)
	}
	return fmt.Sprintf("%s:%s:%s", a.Network, a.Kind, a.Address)
}

// Equal reports whether two asset identifiers locate the same asset.
func (a AssetID) Equal(o AssetID) bool {
	return a.Network.Equal(o.Network) && a.Kind == o.Kind && a.Address == o.Address
}
