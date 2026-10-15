package evm

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// throwaway key (anvil's well-known account #0 key): test material only.
const devKeyHex = "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

// The address that key derives to, EIP-55 checksummed.
const devKeyAddr = "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"

func devSigner(t *testing.T, env string, accounts ...signer.AccountRef) *LocalDevSigner {
	t.Helper()
	key, err := DevKeyFromHex(devKeyHex)
	if err != nil {
		t.Fatalf("dev key: %v", err)
	}
	s, err := NewLocalDevSigner(env, key, accounts...)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return s
}

func devAccountRef(t *testing.T) signer.AccountRef {
	t.Helper()
	addr, err := identifiers.NewEVMAddress(devKeyAddr)
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	ref, err := signer.NewAccountRef(identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, addr)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	return ref
}

func TestLocalDevSignerRefusesProduction(t *testing.T) {
	key, err := DevKeyFromHex(devKeyHex)
	if err != nil {
		t.Fatalf("dev key: %v", err)
	}
	for _, env := range []string{config.EnvProduction, "production"} {
		if _, err := NewLocalDevSigner(env, key, devAccountRef(t)); err == nil {
			t.Errorf("%q environment accepted a plaintext dev key", env)
		}
	}
	if _, err := NewLocalDevSigner(config.EnvDevelopment, nil, devAccountRef(t)); err == nil {
		t.Errorf("nil key accepted")
	}
	if _, err := DevKeyFromHex("zz"); err == nil {
		t.Errorf("bad hex accepted")
	}
}

func TestGenerateDevKey(t *testing.T) {
	key, err := GenerateDevKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	addr, err := DevKeyAddress(key)
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	if addr.Family != identifiers.FamilyEVM || addr.Value == "" {
		t.Fatalf("address = %+v", addr)
	}
	// The derived address revalidates under the EIP-55 rules and the
	// key signs under it through the full allow-list path.
	ref, err := signer.NewAccountRef(identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, addr)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	s, err := NewLocalDevSigner(config.EnvDevelopment, key, ref)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	signed, err := s.Sign(context.Background(), signer.Request{
		Network: ref.Network, Account: addr, Type: signer.SigEVMMessage, Payload: []byte("p"),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	ok, err := VerifyPersonalMessage(addr, []byte("p"), signed.Signature)
	if err != nil || !ok {
		t.Errorf("generated key does not self-verify (err %v, ok %v)", err, ok)
	}
	if _, err := DevKeyAddress(nil); err == nil {
		t.Errorf("nil key accepted")
	}
}

func TestLocalDevSignerRequiresOwnAccount(t *testing.T) {
	other, err := identifiers.NewEVMAddress("0x0000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	otherRef, err := signer.NewAccountRef(identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}, other)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	key, err := DevKeyFromHex(devKeyHex)
	if err != nil {
		t.Fatalf("dev key: %v", err)
	}
	if _, err := NewLocalDevSigner(config.EnvDevelopment, key, otherRef); err == nil {
		t.Errorf("allow list without the key address accepted")
	}
	// The key may sign the same address on several networks.
	multi := devAccountRef(t)
	multi.Network = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "base", ChainID: "8453"}
	devSigner(t, config.EnvDevelopment, devAccountRef(t), multi)
}

func TestLocalDevSignerDescriptor(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	d := s.Descriptor()
	if !d.Unsafe {
		t.Errorf("dev signer must be Unsafe")
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("descriptor invalid: %v", err)
	}
	if !d.SupportsType(signer.SigEVMMessage) || !d.SupportsType(signer.SigEVMTransaction) {
		t.Errorf("signature types = %v, want message and transaction signing", d.SignatureTypes)
	}
	if len(d.Formats) != 2 || !declaresFormat(d.Formats, signer.FormatEIP1559) || !declaresFormat(d.Formats, signer.FormatEIP155) {
		t.Errorf("formats = %v, want EIP-1559 and EIP-155", d.Formats)
	}
	if len(d.AddressSources) != 1 || d.AddressSources[0] != signer.AddressSourceHeldKey {
		t.Errorf("address sources = %v", d.AddressSources)
	}
	if got := len(s.Accounts()); got != 1 {
		t.Errorf("accounts = %d", got)
	}
}

func TestLocalDevSignerSignsAndVerifies(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	ref := devAccountRef(t)
	req := signer.Request{
		Network: ref.Network,
		Account: ref.Address,
		Type:    signer.SigEVMMessage,
		Payload: []byte("login nonce 12345"),
	}
	signed, err := s.Sign(context.Background(), req)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := signed.Validate(); err != nil {
		t.Fatalf("signed invalid: %v", err)
	}
	if len(signed.Signature) != 65 {
		t.Fatalf("signature = %d bytes, want 65", len(signed.Signature))
	}
	if signed.Signature[64] != 27 && signed.Signature[64] != 28 {
		t.Errorf("recovery parity = %d, want 27 or 28", signed.Signature[64])
	}
	ok, err := VerifyPersonalMessage(ref.Address, req.Payload, signed.Signature)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatalf("signature does not recover to the signing account")
	}
	// Signing is deterministic (RFC 6979 nonces): the same bytes twice
	// produce the same signature.
	again, err := s.Sign(context.Background(), req)
	if err != nil || !bytes.Equal(again.Signature, signed.Signature) {
		t.Errorf("signatures not deterministic")
	}
	// A tampered payload verifies false.
	tampered, err := VerifyPersonalMessage(ref.Address, []byte("login nonce 12346"), signed.Signature)
	if err != nil || tampered {
		t.Errorf("tampered payload verified %v (err %v)", tampered, err)
	}
	// A flipped bit in the signature either fails to recover (error) or
	// recovers to a different account (false) — never verifies true.
	corrupt := bytes.Clone(signed.Signature)
	corrupt[0] ^= 1
	ok, err = VerifyPersonalMessage(ref.Address, req.Payload, corrupt)
	if err == nil && ok {
		t.Errorf("corrupted signature verified")
	}
	// A wrong-length signature is an error, not a false.
	if _, err := VerifyPersonalMessage(ref.Address, req.Payload, signed.Signature[:64]); err == nil {
		t.Errorf("short signature accepted")
	}
	// Non-EVM accounts are rejected.
	sol, err := identifiers.NewSolanaAddress("4wBqpZM9xaSheZzJSMawUKKwhdpChKbZ5eu5ky4Vigw")
	if err != nil {
		t.Fatalf("solana address: %v", err)
	}
	if _, err := VerifyPersonalMessage(sol, req.Payload, signed.Signature); err == nil {
		t.Errorf("solana account accepted")
	}
}

func TestLocalDevSignerBoundaries(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	ref := devAccountRef(t)
	req := signer.Request{
		Network: ref.Network,
		Account: ref.Address,
		Type:    signer.SigEVMMessage,
		Payload: []byte("p"),
	}
	// A scheme outside the descriptor is rejected.
	wrongType := req
	wrongType.Type = signer.SigSolanaMessage
	if _, err := s.Sign(context.Background(), wrongType); !errors.Is(err, signer.ErrUnsupportedType) {
		t.Errorf("wrong type: %v", err)
	}
	// Outside the allow-list is rejected before any signing happens.
	other, err := identifiers.NewEVMAddress("0x0000000000000000000000000000000000000001")
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	wrongAccount := req
	wrongAccount.Account = other
	if _, err := s.Sign(context.Background(), wrongAccount); !errors.Is(err, signer.ErrUnauthorized) {
		t.Errorf("wrong account: %v", err)
	}
	wrongNet := req
	wrongNet.Network = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "base", ChainID: "8453"}
	if _, err := s.Sign(context.Background(), wrongNet); !errors.Is(err, signer.ErrUnauthorized) {
		t.Errorf("wrong network: %v", err)
	}
	// Malformed requests are rejected.
	empty := req
	empty.Payload = nil
	if _, err := s.Sign(context.Background(), empty); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("empty payload: %v", err)
	}
	// A canceled context is honored.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Sign(canceled, req); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled context: %v", err)
	}
	// The audit decorator composes: one entry per attempt, digests only.
	var entries []signer.AuditEntry
	wrapped := signer.WithAudit(s, func(e signer.AuditEntry) { entries = append(entries, e) }, nil)
	if _, err := wrapped.Sign(context.Background(), req); err != nil {
		t.Fatalf("wrapped sign: %v", err)
	}
	if len(entries) != 1 || entries[0].Digest != signer.PayloadDigest([]byte("p")) {
		t.Fatalf("audit entry wrong: %+v", entries)
	}
}

// declaresFormat reports whether one format is declared on the list.
func declaresFormat(formats []signer.Format, want signer.Format) bool {
	for _, f := range formats {
		if f == want {
			return true
		}
	}
	return false
}
