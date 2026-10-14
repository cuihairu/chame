package signer

import (
	"context"
	"errors"
	"fmt"

	"github.com/cuihairu/chame/core/identifiers"
)

// SignatureType names a signing scheme (spec §12.1: 适配器明确它支持
// 的签名类型). The set is open: the constants below are the well-known
// schemes, and adapters may declare their own following the same shape
// rules (lowercase, digits, dashes; at most 63 characters).
type SignatureType string

const (
	// SigEVMMessage signs an EIP-191 personal message: keccak256 over
	// the length-prefixed payload, producing a 65-byte R||S||V
	// signature with the recovery parity in the last byte.
	SigEVMMessage SignatureType = "evm-message"
	// SigEVMTransaction signs a built EVM transaction payload. It lands
	// with the transaction capability (spec §19 Phase 5); message-only
	// signers must not declare it.
	SigEVMTransaction SignatureType = "evm-transaction"
	// SigSolanaMessage signs exact bytes with ed25519 — an arbitrary
	// message for off-chain verification.
	SigSolanaMessage SignatureType = "solana-message"
	// SigSolanaTransaction signs a compiled Solana message with
	// ed25519, the operation behind every Solana transaction.
	SigSolanaTransaction SignatureType = "solana-transaction"
)

// Valid reports whether the name follows the shared shape rules.
func (t SignatureType) Valid() bool { return validName(string(t)) }

// Format names a transaction encoding a signer understands (spec §12.1:
// 交易格式). A signer that cannot sign transactions lists none.
type Format string

const (
	// FormatEIP1559 is an EIP-1559 typed transaction.
	FormatEIP1559 Format = "eip-1559"
	// FormatEIP155 is the legacy signed transaction with chain replay
	// protection.
	FormatEIP155 Format = "eip-155"
	// FormatSolanaV0 is a Solana v0 message.
	FormatSolanaV0 Format = "solana-v0"
)

// Valid reports whether the name follows the shared shape rules.
func (f Format) Valid() bool { return validName(string(f)) }

// AddressSource says where a signer's addresses come from (spec §12.1:
// 地址来源). Callers use it to tell a key held by this process from an
// identity an external service owns.
type AddressSource string

const (
	// AddressSourceHeldKey marks addresses derived from a key the
	// signer itself holds (development signers).
	AddressSourceHeldKey AddressSource = "held-key"
	// AddressSourceTest marks the fixed throwaway identity of a test
	// signer.
	AddressSourceTest AddressSource = "test"
	// AddressSourceExternal marks identities owned by an external
	// signing service; this process never sees the key.
	AddressSourceExternal AddressSource = "external"
)

// Valid reports whether the name follows the shared shape rules.
func (a AddressSource) Valid() bool { return validName(string(a)) }

// Descriptor declares what one signer supports (spec §12.1). Callers
// route by it; a request the descriptor does not cover is rejected, not
// improvised. The name is stable and appears in audit entries.
type Descriptor struct {
	// Name identifies the signer instance in logs and audits.
	Name string
	// SignatureTypes lists the signing schemes it supports.
	SignatureTypes []SignatureType
	// Formats lists the transaction encodings it supports.
	Formats []Format
	// AddressSources lists where its addresses come from.
	AddressSources []AddressSource
	// Unsafe marks signers that must never run in production:
	// deterministic test identities and plaintext development keys
	// (spec §12.2, §12.4).
	Unsafe bool
}

// Validate rejects an empty name and malformed or duplicated entries in
// the declaration lists.
func (d Descriptor) Validate() error {
	if d.Name == "" {
		return fmt.Errorf("signer: descriptor name must not be empty")
	}
	if err := validateList("signature type", len(d.SignatureTypes), func(i int) string { return string(d.SignatureTypes[i]) }); err != nil {
		return err
	}
	if err := validateList("format", len(d.Formats), func(i int) string { return string(d.Formats[i]) }); err != nil {
		return err
	}
	if err := validateList("address source", len(d.AddressSources), func(i int) string { return string(d.AddressSources[i]) }); err != nil {
		return err
	}
	return nil
}

// SupportsType reports whether the descriptor lists the scheme.
func (d Descriptor) SupportsType(t SignatureType) bool {
	for _, have := range d.SignatureTypes {
		if have == t {
			return true
		}
	}
	return false
}

// validateList checks shape and uniqueness of one declaration list.
func validateList(field string, n int, at func(int) string) error {
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		v := at(i)
		if !validName(v) {
			return fmt.Errorf("signer: %s %q is not a valid name", field, v)
		}
		if seen[v] {
			return fmt.Errorf("signer: %s %q is listed twice", field, v)
		}
		seen[v] = true
	}
	return nil
}

// validName enforces the shared lowercase shape for open enumerations.
func validName(s string) bool {
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

// Errors reported by signers. Match with errors.Is.
var (
	// ErrUnauthorized marks a request outside the signer's allow-list
	// (spec §12.3: 签名器应能限制可用网络和账户).
	ErrUnauthorized = errors.New("signer: request outside the allowed networks and accounts")
	// ErrUnsupportedType marks a signature type the descriptor does not
	// list.
	ErrUnsupportedType = errors.New("signer: signature type not supported by this signer")
	// ErrInvalidRequest marks a malformed request.
	ErrInvalidRequest = errors.New("signer: invalid signing request")
)

// Request asks one signer for one signature (spec §12.3: 交易发起应
// 明确目标网络和目标账户). The target network and account are explicit
// on every request: nothing is inferred from the payload, and a payload
// alone is never an authorization (spec §12.3: 不得仅凭调用方提供的
// 任意数据就被认为授权成功).
type Request struct {
	// Network is the explicit target network.
	Network identifiers.NetworkID
	// Account is the explicit signing account.
	Account identifiers.Address
	// Type names the signing scheme the payload is encoded for.
	Type SignatureType
	// Payload is the exact bytes to sign. The signer does not decode
	// them; authorization never derives from the payload.
	Payload []byte
}

// Validate checks the identifiers, their family agreement, the scheme,
// and a non-empty payload.
func (r Request) Validate() error {
	if err := r.Network.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if err := r.Account.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if r.Account.Family != r.Network.Family {
		return fmt.Errorf("%w: account family %s does not match network family %s",
			ErrInvalidRequest, r.Account.Family, r.Network.Family)
	}
	if !r.Type.Valid() {
		return fmt.Errorf("%w: signature type %q", ErrInvalidRequest, r.Type)
	}
	if len(r.Payload) == 0 {
		return fmt.Errorf("%w: payload must not be empty", ErrInvalidRequest)
	}
	return nil
}

// Signed is one signature over a request payload.
type Signed struct {
	// Signature is the scheme-defined signature bytes.
	Signature []byte
	// Signer is the descriptor name that produced it, for audit trails.
	Signer string
}

// Validate rejects an empty signature or missing signer name.
func (s Signed) Validate() error {
	if len(s.Signature) == 0 {
		return fmt.Errorf("signer: signature must not be empty")
	}
	if s.Signer == "" {
		return fmt.Errorf("signer: signed result must name its signer")
	}
	return nil
}

// Signer is the signing capability (spec §12.1). It is independent of
// chain RPC and business modules: adapters and external signing services
// implement it, business code receives it through wiring and can only
// sign for the accounts it exposes. Implementations must enforce their
// allow-list, reject uncovered schemes, and never log key material
// (spec §12.3).
type Signer interface {
	// Descriptor declares supported schemes, formats, and address
	// sources (spec §12.1).
	Descriptor() Descriptor
	// Accounts lists every (network, account) this signer may sign for.
	Accounts() []AccountRef
	// Sign signs one payload. A request outside the allow-list, with an
	// uncovered scheme, or malformed is rejected without side effects.
	Sign(ctx context.Context, req Request) (Signed, error)
}
