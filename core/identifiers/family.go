// Package identifiers defines the chain-agnostic identity types (spec
// §6): ChainFamily, NetworkID, Address, Amount, and AssetID. All
// validation and comparison is family-aware; values from different chain
// families never mix silently.
package identifiers

import "fmt"

// ChainFamily names a protocol family (spec §6.2). Families abstract
// over concrete networks: evm covers every EVM chain, solana covers every
// Solana cluster.
type ChainFamily string

const (
	// FamilyEVM is the EVM chain family.
	FamilyEVM ChainFamily = "evm"
	// FamilySolana is the Solana chain family.
	FamilySolana ChainFamily = "solana"
)

// Valid reports whether f is a known family.
func (f ChainFamily) Valid() bool {
	switch f {
	case FamilyEVM, FamilySolana:
		return true
	default:
		return false
	}
}

// String returns the family name.
func (f ChainFamily) String() string { return string(f) }

// NetworkID identifies one logical network (spec §6.2). Name is the
// stable logical name from the application config; ChainID is the
// in-family protocol identifier as a string, so the common layer never
// depends on one integer width. Name alone is not a cryptographic
// identity and must not be used to verify assets or signature targets.
type NetworkID struct {
	Family  ChainFamily `json:"family"`
	Name    string      `json:"name"`
	ChainID string      `json:"chain_id"`
}

// Validate checks family, name, and chain ID presence. The name follows
// the same pattern as other identifiers: lowercase letters, digits, and
// dashes, so config references stay unambiguous.
func (n NetworkID) Validate() error {
	if !n.Family.Valid() {
		return fmt.Errorf("network %q: unknown chain family %q", n.Name, n.Family)
	}
	if !isValidName(n.Name) {
		return fmt.Errorf("network: invalid logical name %q", n.Name)
	}
	if n.ChainID == "" {
		return fmt.Errorf("network %q: chain id must not be empty", n.Name)
	}
	return nil
}

// String returns family/name/chainID for logs and errors.
func (n NetworkID) String() string {
	return fmt.Sprintf("%s/%s/%s", n.Family, n.Name, n.ChainID)
}

// Equal reports whether two network identifiers are the same network.
func (n NetworkID) Equal(o NetworkID) bool {
	return n.Family == o.Family && n.Name == o.Name && n.ChainID == o.ChainID
}

// isValidName enforces the shared logical-name pattern.
func isValidName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return false
		}
	}
	return true
}
