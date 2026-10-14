package identifiers

import (
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/sha3"
)

// Address is a family-aware account address (spec §6.3). The value is
// the canonical representation for its family; validation rules differ
// per family and are enforced at construction.
type Address struct {
	Family ChainFamily `json:"family"`
	Value  string      `json:"value"`
}

// NewAddress constructs and validates an address for a family.
func NewAddress(family ChainFamily, value string) (Address, error) {
	a := Address{Family: family, Value: value}
	if err := a.Validate(); err != nil {
		return Address{}, err
	}
	return a, nil
}

// NewEVMAddress validates an EVM address: 20 bytes in hex with optional
// 0x prefix. Mixed-case values must carry a correct EIP-55 checksum;
// all-lowercase and all-uppercase values are accepted as unchecksummed
// forms (spec §6.3: 在适用时校验 EIP-55 格式).
func NewEVMAddress(value string) (Address, error) {
	a := Address{Family: FamilyEVM, Value: value}
	if err := a.Validate(); err != nil {
		return Address{}, err
	}
	return a, nil
}

// NewSolanaAddress validates a Solana address: a base58 string decoding
// to exactly 32 bytes (spec §6.3: 按其编码规则校验公钥表示).
func NewSolanaAddress(value string) (Address, error) {
	a := Address{Family: FamilySolana, Value: value}
	if err := a.Validate(); err != nil {
		return Address{}, err
	}
	return a, nil
}

// Validate checks the address against its family rules.
func (a Address) Validate() error {
	switch a.Family {
	case FamilyEVM:
		return validateEVM(a.Value)
	case FamilySolana:
		return validateSolana(a.Value)
	default:
		return fmt.Errorf("address: unknown chain family %q", a.Family)
	}
}

// Equal reports whether two addresses are the same account on the same
// family. Addresses of different families are never equal, even with
// identical values.
func (a Address) Equal(o Address) bool {
	return a.Family == o.Family && a.Value == o.Value
}

// String returns the raw value; the family travels alongside it.
func (a Address) String() string { return a.Value }

// validateEVM enforces hex length and, when the casing is mixed, the
// EIP-55 checksum.
func validateEVM(value string) error {
	hexPart := strings.TrimPrefix(value, "0x")
	if len(hexPart) != 40 {
		return fmt.Errorf("evm address: want 40 hex characters, got %d", len(hexPart))
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return fmt.Errorf("evm address: invalid hex: %w", err)
	}
	lower := strings.ToLower(hexPart)
	upper := strings.ToUpper(hexPart)
	if hexPart == lower || hexPart == upper {
		// Unchecksummed form; casing carries no information to check.
		return nil
	}
	sum := keccak256Hex(lower)
	for i := 0; i < len(hexPart); i++ {
		want := lower[i]
		if sum[i] >= '8' {
			want = upper[i]
		}
		if hexPart[i] != want {
			return fmt.Errorf("evm address: EIP-55 checksum mismatch")
		}
	}
	return nil
}

// keccak256Hex returns the keccak256 hash of s as lowercase hex, per
// EIP-55.
func keccak256Hex(s string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// validateSolana enforces base58 decoding to a 32-byte public key.
func validateSolana(value string) error {
	raw, err := base58Decode(value)
	if err != nil {
		return fmt.Errorf("solana address: %w", err)
	}
	if len(raw) != 32 {
		return fmt.Errorf("solana address: want 32 bytes, got %d", len(raw))
	}
	return nil
}
