package solana

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go/base58"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// throwaway 32-byte seed for tests; the key pair is derived in-test.
const devSeedHex = "aa2447c21526d8b5f0a5a4c20d749b2908e52d792986c9d3e7b1f2d6e0c1a9b4"

func devKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	seed, err := hex.DecodeString(devSeedHex)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func devAddress(t *testing.T) identifiers.Address {
	t.Helper()
	pub := devKey(t).Public().(ed25519.PublicKey)
	addr, err := identifiers.NewSolanaAddress(base58.Encode(pub))
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	return addr
}

func devAccountRef(t *testing.T) signer.AccountRef {
	t.Helper()
	ref, err := signer.NewAccountRef(
		identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet-beta", ChainID: "solana"},
		devAddress(t),
	)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	return ref
}

func TestLocalDevSignerRefusesProduction(t *testing.T) {
	key := devKey(t)
	if _, err := NewLocalDevSigner(config.EnvProduction, key, devAccountRef(t)); err == nil {
		t.Errorf("production environment accepted a plaintext dev key")
	}
	if _, err := NewLocalDevSigner(config.EnvDevelopment, ed25519.PrivateKey{0, 1, 2}, devAccountRef(t)); err == nil {
		t.Errorf("short key accepted")
	}
}

func TestDevKeyFromBase58(t *testing.T) {
	key := devKey(t)
	seed := key.Seed()
	pub := key.Public().(ed25519.PublicKey)

	// The 32-byte seed form.
	fromSeed, err := DevKeyFromBase58(base58.Encode(seed))
	if err != nil {
		t.Fatalf("seed form: %v", err)
	}
	if !bytes.Equal(fromSeed, key) {
		t.Errorf("seed form round trip mismatch")
	}
	// The 64-byte keypair form (id.json: seed||pubkey).
	fromPair, err := DevKeyFromBase58(base58.Encode(key))
	if err != nil {
		t.Fatalf("keypair form: %v", err)
	}
	if !bytes.Equal(fromPair, key) {
		t.Errorf("keypair form round trip mismatch")
	}
	// Corruption: a 64-byte value whose second half is not the seed's pubkey.
	corrupt := append(bytes.Clone(seed), pub[:]...)
	corrupt[len(corrupt)-1] ^= 1
	if _, err := DevKeyFromBase58(base58.Encode(corrupt)); err == nil {
		t.Errorf("corrupt keypair accepted")
	}
	// Wrong lengths and bad base58 are rejected.
	for _, in := range []string{
		base58.Encode([]byte("short")),
		base58.Encode(make([]byte, 100)),
		"not-base58!!",
	} {
		if _, err := DevKeyFromBase58(in); err == nil {
			t.Errorf("bad key form accepted: %s", in)
		}
	}
}

func TestLocalDevSignerRequiresOwnAccount(t *testing.T) {
	other, err := identifiers.NewSolanaAddress(base58.Encode([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}))
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	otherRef, err := signer.NewAccountRef(
		identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet-beta", ChainID: "solana"},
		other,
	)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	if _, err := NewLocalDevSigner(config.EnvDevelopment, devKey(t), otherRef); err == nil {
		t.Errorf("allow list without the key address accepted")
	}
}

func TestLocalDevSignerDescriptor(t *testing.T) {
	s, err := NewLocalDevSigner(config.EnvDevelopment, devKey(t), devAccountRef(t))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	d := s.Descriptor()
	if !d.Unsafe {
		t.Errorf("dev signer must be Unsafe")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("descriptor invalid: %v", err)
	}
	for _, want := range []signer.SignatureType{signer.SigSolanaMessage, signer.SigSolanaTransaction} {
		if !d.SupportsType(want) {
			t.Errorf("descriptor misses %s", want)
		}
	}
	if d.SupportsType(signer.SigEVMMessage) {
		t.Errorf("descriptor declares an EVM scheme")
	}
	if len(d.Formats) != 1 || d.Formats[0] != signer.FormatSolanaV0 {
		t.Errorf("formats = %v", d.Formats)
	}
	if d.AddressSources[0] != signer.AddressSourceHeldKey {
		t.Errorf("address sources = %v", d.AddressSources)
	}
	if len(s.Accounts()) != 1 {
		t.Errorf("accounts = %d", len(s.Accounts()))
	}
}

func TestLocalDevSignerSignsAndVerifies(t *testing.T) {
	s, err := NewLocalDevSigner(config.EnvDevelopment, devKey(t), devAccountRef(t))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ref := devAccountRef(t)
	for _, typ := range []signer.SignatureType{signer.SigSolanaMessage, signer.SigSolanaTransaction} {
		req := signer.Request{
			Network: ref.Network,
			Account: ref.Address,
			Type:    typ,
			Payload: []byte("login nonce 12345"),
		}
		signed, err := s.Sign(context.Background(), req)
		if err != nil {
			t.Fatalf("sign %s: %v", typ, err)
		}
		if err := signed.Validate(); err != nil {
			t.Fatalf("signed invalid: %v", err)
		}
		if len(signed.Signature) != ed25519.SignatureSize {
			t.Fatalf("%s signature = %d bytes", typ, len(signed.Signature))
		}
		if signed.Signer != "solana-dev" {
			t.Errorf("Signer = %q", signed.Signer)
		}
		// ed25519 is deterministic: the same bytes sign twice the same.
		again, err := s.Sign(context.Background(), req)
		if err != nil || !bytes.Equal(again.Signature, signed.Signature) {
			t.Errorf("%s signature not deterministic", typ)
		}
		ok, err := VerifySignature(ref.Address, req.Payload, signed.Signature)
		if err != nil || !ok {
			t.Errorf("%s signature does not verify for the signing account (err %v)", typ, err)
		}
		// A different account verifies false; a wrong message verifies
		// false; a truncated signature is an error.
		if ok, err := VerifySignature(ref.Address, []byte("login nonce 12346"), signed.Signature); err != nil || ok {
			t.Errorf("tampered message verified %v (err %v)", ok, err)
		}
		if _, err := VerifySignature(ref.Address, req.Payload, signed.Signature[:32]); err == nil {
			t.Errorf("short signature accepted")
		}
		other, err := identifiers.NewSolanaAddress(base58.Encode([]byte{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
			9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}))
		if err != nil {
			t.Fatalf("address: %v", err)
		}
		if ok, err := VerifySignature(other, req.Payload, signed.Signature); err != nil || ok {
			t.Errorf("other account verified %v (err %v)", ok, err)
		}
	}
	// Non-Solana accounts are rejected.
	evm, err := identifiers.NewEVMAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	if _, err := VerifySignature(evm, []byte("p"), make([]byte, 64)); err == nil {
		t.Errorf("evm account accepted")
	}
}

func TestLocalDevSignerBoundaries(t *testing.T) {
	s, err := NewLocalDevSigner(config.EnvDevelopment, devKey(t), devAccountRef(t))
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ref := devAccountRef(t)
	req := signer.Request{
		Network: ref.Network,
		Account: ref.Address,
		Type:    signer.SigSolanaMessage,
		Payload: []byte("p"),
	}
	// A scheme outside the descriptor is rejected.
	wrongType := req
	wrongType.Type = signer.SigEVMTransaction
	if _, err := s.Sign(context.Background(), wrongType); !errors.Is(err, signer.ErrUnsupportedType) {
		t.Errorf("wrong type: %v", err)
	}
	// Outside the allow-list is rejected: account and network, both.
	other, err := identifiers.NewSolanaAddress(base58.Encode([]byte{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
		7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}))
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	wrongAccount := req
	wrongAccount.Account = other
	if _, err := s.Sign(context.Background(), wrongAccount); !errors.Is(err, signer.ErrUnauthorized) {
		t.Errorf("wrong account: %v", err)
	}
	wrongNet := req
	wrongNet.Network = identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "devnet", ChainID: "solana"}
	if _, err := s.Sign(context.Background(), wrongNet); !errors.Is(err, signer.ErrUnauthorized) {
		t.Errorf("wrong network: %v", err)
	}
	// Malformed requests are rejected.
	empty := req
	empty.Payload = nil
	if _, err := s.Sign(context.Background(), empty); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("empty payload: %v", err)
	}
	// A canceled context is honored before any signing work.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Sign(canceled, req); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: %v", err)
	}
	// The audit decorator composes: one entry per attempt, digest only.
	var entries []signer.AuditEntry
	wrapped := signer.WithAudit(s, func(e signer.AuditEntry) { entries = append(entries, e) }, nil)
	if _, err := wrapped.Sign(context.Background(), req); err != nil {
		t.Fatalf("wrapped sign: %v", err)
	}
	if len(entries) != 1 || entries[0].Digest != signer.PayloadDigest([]byte("p")) || entries[0].Denied != "" {
		t.Fatalf("audit entry wrong: %+v", entries)
	}
}
