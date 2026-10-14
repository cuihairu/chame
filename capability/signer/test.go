package signer

import (
	"bytes"
	"context"
	"fmt"

	"golang.org/x/crypto/sha3"
)

// TestSigner is the deterministic test-only signer (spec §12.2: 测试专用
// 签名器). Its signatures are SHA3-256 digests over the request identity
// and payload: stable across runs, cheap to assert on, and different per
// account — but they are not valid signatures for any chain and verify
// nowhere. It exists so framework tests, examples, and wiring checks run
// without key material.
//
// It is Unsafe and must never be wired in production (spec §12.2,
// §12.4).
type TestSigner struct {
	name     string
	accounts AllowList
	desc     Descriptor
}

var _ Signer = (*TestSigner)(nil)

// NewTestSigner builds a signer over an explicit allow-list. The name
// appears in descriptors, signed results, and audit entries.
func NewTestSigner(name string, accounts ...AccountRef) (*TestSigner, error) {
	if name == "" {
		return nil, fmt.Errorf("signer: test signer name must not be empty")
	}
	list := AllowList(accounts)
	if err := list.Validate(); err != nil {
		return nil, fmt.Errorf("signer: test signer %q: %w", name, err)
	}
	return &TestSigner{
		name:     name,
		accounts: list,
		desc: Descriptor{
			Name:           name,
			SignatureTypes: []SignatureType{SigEVMMessage, SigEVMTransaction, SigSolanaMessage, SigSolanaTransaction},
			AddressSources: []AddressSource{AddressSourceTest},
			Unsafe:         true,
		},
	}, nil
}

// Descriptor declares the test identity: every scheme, test addresses,
// unsafe.
func (t *TestSigner) Descriptor() Descriptor { return t.desc }

// Accounts returns a copy of the allow-list.
func (t *TestSigner) Accounts() []AccountRef { return t.accounts.Accounts() }

// Sign authorizes against the allow-list and returns the deterministic
// test signature: SHA3-256 over network, account, scheme, and payload.
func (t *TestSigner) Sign(_ context.Context, req Request) (Signed, error) {
	if err := req.Validate(); err != nil {
		return Signed{}, err
	}
	if err := t.accounts.Authorize(req); err != nil {
		return Signed{}, err
	}
	var buf bytes.Buffer
	buf.WriteString(req.Network.String())
	buf.WriteByte(0)
	buf.WriteString(req.Account.Value)
	buf.WriteByte(0)
	buf.WriteString(string(req.Type))
	buf.WriteByte(0)
	buf.Write(req.Payload)
	h := sha3.NewLegacyKeccak256()
	h.Write(buf.Bytes())
	return Signed{Signature: h.Sum(nil), Signer: t.name}, nil
}
