package evm

import (
	"context"
	"crypto/ecdsa"
	"fmt"

	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// LocalDevSigner is the EVM development signer (spec §12.2): it holds
// one secp256k1 key in process memory and signs EIP-191 personal
// messages for an explicit allow-list of (network, account) pairs.
//
// It is an Unsafe signer (spec §12.4): plaintext keys are development
// and test only, production environments are refused at construction,
// and it signs no transaction formats — built transactions land with the
// transaction capability. Games needing off-chain login verification use
// VerifyPersonalMessage on the results.
type LocalDevSigner struct {
	key      *ecdsa.PrivateKey
	account  identifiers.Address
	accounts signer.AllowList
	desc     signer.Descriptor
}

var _ signer.Signer = (*LocalDevSigner)(nil)

// NewLocalDevSigner builds a development signer over one plaintext
// secp256k1 key. env is the application environment: production is
// refused (spec §12.4: 生产环境默认禁止明文私钥签名). The allow-list
// must contain the key's own address — a held key that signs for no
// account is a wiring mistake, not a usable signer.
func NewLocalDevSigner(env string, key *ecdsa.PrivateKey, accounts ...signer.AccountRef) (*LocalDevSigner, error) {
	if env == config.EnvProduction {
		return nil, fmt.Errorf("evm: local development signer refuses environment %q: plaintext keys are development only", env)
	}
	if key == nil {
		return nil, fmt.Errorf("evm: local development signer needs a key")
	}
	list := signer.AllowList(accounts)
	if err := list.Validate(); err != nil {
		return nil, fmt.Errorf("evm: local development signer: %w", err)
	}
	account, err := identifiers.NewEVMAddress(crypto.PubkeyToAddress(key.PublicKey).Hex())
	if err != nil {
		return nil, fmt.Errorf("evm: local development signer: %w", err)
	}
	allowed := false
	for _, ref := range list {
		if ref.Address.Equal(account) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, fmt.Errorf("evm: local development signer: allow list must include the key address %s", account)
	}
	return &LocalDevSigner{
		key:      key,
		account:  account,
		accounts: list,
		desc: signer.Descriptor{
			Name:           "evm-dev",
			SignatureTypes: []signer.SignatureType{signer.SigEVMMessage},
			AddressSources: []signer.AddressSource{signer.AddressSourceHeldKey},
			Unsafe:         true,
		},
	}, nil
}

// DevKeyFromHex decodes a 64-hex-character secp256k1 private key. Tests
// and wiring use it once at startup; the hex form must come from the
// process environment or a secret manager, never from config strings
// (spec §12.3).
func DevKeyFromHex(hexKey string) (*ecdsa.PrivateKey, error) {
	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("evm: dev key: %w", err)
	}
	return key, nil
}

// Descriptor declares EIP-191 message signing over a held key, unsafe.
func (s *LocalDevSigner) Descriptor() signer.Descriptor { return s.desc }

// Accounts returns a copy of the allow-list.
func (s *LocalDevSigner) Accounts() []signer.AccountRef { return s.accounts.Accounts() }

// Sign signs one EIP-191 personal message for an allowed (network,
// account) pair: keccak256 over the length-prefixed payload, 65-byte
// R||S||V with the personal-sign recovery parity (27/28) in the last
// byte. The payload is not decoded and is never an authorization
// (spec §12.3).
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
	sig, err := crypto.Sign(accounts.TextHash(req.Payload), s.key)
	if err != nil {
		return signer.Signed{}, cherrs.New(cherrs.InternalError, "evm.signer.sign", "sign: %v", err)
	}
	sig[64] += 27 // personal-sign parity
	return signer.Signed{Signature: sig, Signer: s.desc.Name}, nil
}

// VerifyPersonalMessage recovers the signer of an EIP-191 personal
// message signature and reports whether it is the given account. A
// malformed signature is an error; a valid signature from another
// account is a false, not an error.
func VerifyPersonalMessage(account identifiers.Address, message, signature []byte) (bool, error) {
	if account.Family != identifiers.FamilyEVM {
		return false, fmt.Errorf("evm: verify needs an evm account, got %s", account.Family)
	}
	if err := account.Validate(); err != nil {
		return false, fmt.Errorf("evm: verify: %w", err)
	}
	if len(signature) != 65 {
		return false, fmt.Errorf("evm: verify: want a 65-byte signature, got %d bytes", len(signature))
	}
	recoverable := make([]byte, 65)
	copy(recoverable, signature)
	if recoverable[64] == 27 || recoverable[64] == 28 {
		recoverable[64] -= 27
	}
	pub, err := crypto.SigToPub(accounts.TextHash(message), recoverable)
	if err != nil {
		return false, fmt.Errorf("evm: verify: %w", err)
	}
	return crypto.PubkeyToAddress(*pub) == common.HexToAddress(account.Value), nil
}
