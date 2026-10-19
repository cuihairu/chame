package evm

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// LocalDevSigner is the EVM development signer (spec §12.2): it holds
// one secp256k1 key in process memory and signs EIP-191 personal
// messages and unsigned transactions for an explicit allow-list of
// (network, account) pairs.
//
// It is an Unsafe signer (spec §12.4): plaintext keys are development
// and test only, and production environments are refused at
// construction.
type LocalDevSigner struct {
	key      *ecdsa.PrivateKey
	account  identifiers.Address
	accounts signer.AllowList
	desc     signer.Descriptor
	locks    []*os.File
	closeMu  sync.Mutex
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
	account, err := DevKeyAddress(key)
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
	s := &LocalDevSigner{
		key:      key,
		account:  account,
		accounts: list,
		desc: signer.Descriptor{
			Name:           "evm-dev",
			SignatureTypes: []signer.SignatureType{signer.SigEVMMessage, signer.SigEVMTransaction},
			Formats:        []signer.Format{signer.FormatEIP1559, signer.FormatEIP155},
			AddressSources: []signer.AddressSource{signer.AddressSourceHeldKey},
			Unsafe:         true,
		},
	}
	for _, ref := range list {
		if ref.Network.Family != identifiers.FamilyEVM {
			continue
		}
		name := strings.NewReplacer("/", "_", ":", "_").Replace(ref.Network.String() + "-" + strings.ToLower(ref.Address.Value))
		path := filepath.Join(os.TempDir(), "chame-evm-writer-"+name+".lock")
		f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			return nil, errors.Join(fmt.Errorf("evm: open signer lease for %s: %w", ref.Address, e), s.Close())
		}
		if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
			return nil, errors.Join(
				fmt.Errorf("evm: signer account %s on %s already has an active writer in this host; shared-account multi-instance writes are unsupported: %w", ref.Address, ref.Network, e),
				f.Close(), s.Close(),
			)
		}
		s.locks = append(s.locks, f)
	}
	return s, nil
}

// Close releases this process's per-account writer leases. Hosts must call
// it during shutdown; OS process exit also releases the locks.
func (s *LocalDevSigner) Close() error {
	s.closeMu.Lock()
	defer s.closeMu.Unlock()
	var first error
	for _, f := range s.locks {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil && first == nil {
			first = err
		}
		if err := f.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.locks = nil
	return first
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

// DevKeyAddress derives the account address of a development key.
// Wiring uses it to build the allow-list before constructing the
// signer, whose constructor requires the list.
func DevKeyAddress(key *ecdsa.PrivateKey) (identifiers.Address, error) {
	if key == nil {
		return identifiers.Address{}, fmt.Errorf("evm: dev key address: key must not be nil")
	}
	return identifiers.NewEVMAddress(crypto.PubkeyToAddress(key.PublicKey).Hex())
}

// GenerateDevKey creates a fresh throwaway keypair for examples and
// scratch scripts. Production deployments never use generated local
// keys; they wire an implementation of the signer contract instead
// (spec §12.2).
func GenerateDevKey() (*ecdsa.PrivateKey, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return nil, fmt.Errorf("evm: generate dev key: %w", err)
	}
	return key, nil
}

// Descriptor declares EIP-191 message and unsigned-transaction signing
// over a held key, unsafe.
func (s *LocalDevSigner) Descriptor() signer.Descriptor { return s.desc }

// Accounts returns a copy of the allow-list.
func (s *LocalDevSigner) Accounts() []signer.AccountRef { return s.accounts.Accounts() }

// Sign signs one payload for an allowed (network, account) pair. EIP-191
// personal messages are keccak256 over the length-prefixed payload and
// carry 65-byte R||S||V with the personal-sign parity (27/28) in the
// last byte; transactions are signed over the exact unsigned bytes
// the request carries and the signed encoding comes back. Neither
// payload is decoded, and neither is ever an authorization (spec
// §12.3).
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
	switch req.Type {
	case signer.SigEVMMessage:
		sig, err := crypto.Sign(accounts.TextHash(req.Payload), s.key)
		if err != nil {
			return signer.Signed{}, cherrs.New(cherrs.InternalError, "evm.signer.sign", "sign: %v", err)
		}
		sig[64] += 27 // personal-sign parity
		return signer.Signed{Signature: sig, Signer: s.desc.Name}, nil
	default:
		return s.signTransaction(req)
	}
}

// signTransaction signs one unsigned transaction (spec §12.1). The
// payload is the transaction's canonical unsigned encoding — RLP for a
// legacy tx, the typed envelope for EIP-1559 — exactly as the builder
// produced it. The signature is computed per the tx type, and the
// signed encoding is returned for broadcast.
//
// The transaction is verified against the request before signing: the
// chain id must match the network the request names, so a transaction
// built for another chain is refused rather than delivered somewhere
// unexpected. A transaction that already carries a signature is
// refused too — a signature is produced once over the unsigned bytes,
// never renewed. The sender is the key itself: one held key signs only
// for its own address (spec §7.6), even when the allow-list names other
// accounts for message signing.
func (s *LocalDevSigner) signTransaction(req signer.Request) (signer.Signed, error) {
	const op = "evm.signer.tx"
	if !req.Account.Equal(s.account) {
		return signer.Signed{}, fmt.Errorf("%w: the key signs only for %s",
			signer.ErrUnauthorized, s.account.Value)
	}
	chainID, err := strconv.ParseUint(req.Network.ChainID, 10, 64)
	if err != nil || chainID == 0 {
		return signer.Signed{}, fmt.Errorf("%w: request network %q has no valid chain id",
			signer.ErrInvalidRequest, req.Network.Name)
	}

	var unsigned types.Transaction
	if err := unsigned.UnmarshalBinary(req.Payload); err != nil {
		return signer.Signed{}, fmt.Errorf("%w: payload is not an EVM transaction: %v",
			signer.ErrInvalidRequest, err)
	}
	switch unsigned.Type() {
	case types.LegacyTxType, types.DynamicFeeTxType:
		// Declared formats only (spec §12.1).
	default:
		return signer.Signed{}, fmt.Errorf("%w: transaction type %d",
			signer.ErrUnsupportedType, unsigned.Type())
	}
	// A typed encoding carries its chain id, so it must match the
	// network the request names. A legacy encoding has no chain id
	// field at all — the id is bound at signing time, from the
	// network the request names (spec §7.6).
	if unsigned.Type() != types.LegacyTxType {
		declared := unsigned.ChainId()
		if declared == nil || declared.Uint64() != chainID {
			how := "<none>"
			if declared != nil {
				how = declared.String()
			}
			return signer.Signed{}, fmt.Errorf("%w: transaction chain id %s does not match the request network chain id %d",
				signer.ErrInvalidRequest, how, chainID)
		}
	}
	_, r, sv := unsigned.RawSignatureValues()
	if r != nil && r.Sign() != 0 || sv != nil && sv.Sign() != 0 {
		return signer.Signed{}, fmt.Errorf("%w: payload already carries a signature",
			signer.ErrInvalidRequest)
	}

	signed, err := types.SignTx(&unsigned, types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), s.key)
	if err != nil {
		return signer.Signed{}, cherrs.New(cherrs.InternalError, op, "sign: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return signer.Signed{}, cherrs.New(cherrs.InternalError, op, "encode signed tx: %v", err)
	}
	return signer.Signed{Signature: raw, Signer: s.desc.Name}, nil
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
