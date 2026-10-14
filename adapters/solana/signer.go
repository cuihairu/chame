package solana

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"

	"github.com/gagliardetto/solana-go/base58"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// LocalDevSigner is the Solana development signer (spec §12.2): it
// holds one ed25519 key in process memory and signs exact bytes for an
// explicit allow-list of (network, account) pairs. Solana signs messages
// and compiled transactions with the same operation — ed25519 over the
// exact bytes — so both schemes are declared, and nothing here
// understands transaction contents: message assembly stays with the
// transaction capability, authorization never derives from the payload
// (spec §12.3).
//
// It is an Unsafe signer (spec §12.4): plaintext keys are development
// and test only, and production environments are refused at
// construction.
type LocalDevSigner struct {
	priv     ed25519.PrivateKey
	account  identifiers.Address
	accounts signer.AllowList
	desc     signer.Descriptor
}

var _ signer.Signer = (*LocalDevSigner)(nil)

// NewLocalDevSigner builds a development signer over one ed25519
// keypair. env is the application environment: production is refused
// (spec §12.4). The allow-list must contain the key's own address — a
// held key that signs for no account is a wiring mistake.
func NewLocalDevSigner(env string, key ed25519.PrivateKey, accounts ...signer.AccountRef) (*LocalDevSigner, error) {
	if env == config.EnvProduction {
		return nil, fmt.Errorf("solana: local development signer refuses environment %q: plaintext keys are development only", env)
	}
	if len(key) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("solana: local development signer wants a %d-byte key, got %d",
			ed25519.PrivateKeySize, len(key))
	}
	list := signer.AllowList(accounts)
	if err := list.Validate(); err != nil {
		return nil, fmt.Errorf("solana: local development signer: %w", err)
	}
	account, err := identifiers.NewSolanaAddress(base58.Encode(key.Public().(ed25519.PublicKey)))
	if err != nil {
		return nil, fmt.Errorf("solana: local development signer: %w", err)
	}
	allowed := false
	for _, ref := range list {
		if ref.Address.Equal(account) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("solana: local development signer: allow list must include the key address %s", account)
	}
	return &LocalDevSigner{
		priv:     key,
		account:  account,
		accounts: list,
		desc: signer.Descriptor{
			Name:           "solana-dev",
			SignatureTypes: []signer.SignatureType{signer.SigSolanaMessage, signer.SigSolanaTransaction},
			Formats:        []signer.Format{signer.FormatSolanaV0},
			AddressSources: []signer.AddressSource{signer.AddressSourceHeldKey},
			Unsafe:         true,
		},
	}, nil
}

// DevKeyFromBase58 decodes a development keypair: the base58 of the
// 64-byte keypair form (an id.json file) or of a 32-byte seed. The
// key material must come from the process environment or a secret
// manager, never from config strings (spec §12.3).
func DevKeyFromBase58(encoded string) (ed25519.PrivateKey, error) {
	raw, err := base58.Decode(encoded)
	if err != nil {
		return nil, fmt.Errorf("solana: dev key: %w", err)
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		priv := ed25519.PrivateKey(raw)
		// The 64-byte form carries seed||pubkey; a mismatch is corruption.
		pub := priv.Public().(ed25519.PublicKey)
		if !bytes.Equal(pub, ed25519.NewKeyFromSeed(raw[:32]).Public().(ed25519.PublicKey)) {
			return nil, fmt.Errorf("solana: dev key: keypair bytes disagree with the seed")
		}
		return priv, nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, fmt.Errorf("solana: dev key: want %d or %d bytes, got %d",
			ed25519.PrivateKeySize, ed25519.SeedSize, len(raw))
	}
}

// Descriptor declares ed25519 message and transaction signing over a
// held key, unsafe.
func (s *LocalDevSigner) Descriptor() signer.Descriptor { return s.desc }

// Accounts returns a copy of the allow-list.
func (s *LocalDevSigner) Accounts() []signer.AccountRef { return s.accounts.Accounts() }

// Sign signs the exact payload bytes with ed25519 for an allowed
// (network, account) pair. The signature is 64 bytes; the payload is
// not decoded and is never an authorization (spec §12.3).
func (s *LocalDevSigner) Sign(ctx context.Context, req signer.Request) (signer.Signed, error) {
	if err := ctx.Err(); err != nil {
		return signer.Signed{}, err
	}
	if err := req.Validate(); err != nil {
		return signer.Signed{}, err
	}
	if !s.desc.SupportsType(req.Type) {
		return signer.Signed{}, fmt.Errorf("%w: %s", signer.ErrUnsupportedType, req.Type)
	}
	if err := s.accounts.Authorize(req); err != nil {
		return signer.Signed{}, err
	}
	sig := ed25519.Sign(s.priv, req.Payload)
	return signer.Signed{Signature: sig, Signer: s.desc.Name}, nil
}

// VerifySignature reports whether signature is a valid ed25519
// signature of message by the account — login-style verification, the
// Solana counterpart of the EVM personal-message check.
func VerifySignature(account identifiers.Address, message, signature []byte) (bool, error) {
	if account.Family != identifiers.FamilySolana {
		return false, fmt.Errorf("solana: verify needs a solana account, got %s", account.Family)
	}
	if err := account.Validate(); err != nil {
		return false, fmt.Errorf("solana: verify: %w", err)
	}
	pub, err := base58.Decode(account.Value)
	if err != nil {
		return false, fmt.Errorf("solana: verify: %w", err)
	}
	if len(signature) != ed25519.SignatureSize {
		return false, fmt.Errorf("solana: verify: want a %d-byte signature, got %d bytes",
			ed25519.SignatureSize, len(signature))
	}
	return ed25519.Verify(ed25519.PublicKey(pub), message, signature), nil
}
