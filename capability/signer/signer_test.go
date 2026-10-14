package signer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/chame/core/identifiers"
)

var (
	evmNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}
)

func evmAddr(t *testing.T, value string) identifiers.Address {
	t.Helper()
	addr, err := identifiers.NewEVMAddress(value)
	if err != nil {
		t.Fatalf("evm address: %v", err)
	}
	return addr
}

func solAddr(t *testing.T, value string) identifiers.Address {
	t.Helper()
	addr, err := identifiers.NewSolanaAddress(value)
	if err != nil {
		t.Fatalf("solana address: %v", err)
	}
	return addr
}

// The reference address is the canonical mixed-case EIP-55 form of a
// fixed key; all-lowercase spellings are accepted as unchecksummed
// equivalents of the same account.
const refEVM = "0x71562b71999873DB5b286dF957af199Ec94617F7"

func TestRequestValidate(t *testing.T) {
	addr := evmAddr(t, strings.ToLower(refEVM))
	base := Request{
		Network: evmNet,
		Account: addr,
		Type:    SigEVMMessage,
		Payload: []byte("payload"),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	badNetwork := identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "", ChainID: "1"}
	cases := []struct {
		name string
		mut  func(*Request)
	}{
		{"bad network", func(r *Request) { r.Network = badNetwork }},
		{"unknown family", func(r *Request) {
			r.Network = identifiers.NetworkID{Family: "cosmos", Name: "x", ChainID: "1"}
		}},
		{"bad address", func(r *Request) { r.Account = identifiers.Address{Family: identifiers.FamilyEVM, Value: "nope"} }},
		{"family mismatch", func(r *Request) { r.Account = solAddr(t, "4wBqpZM9xaSheZzJSMawUKKwhdpChKbZ5eu5ky4Vigw") }},
		{"bad type", func(r *Request) { r.Type = SignatureType("EVM MESSAGE") }},
		{"empty payload", func(r *Request) { r.Payload = nil }},
	}
	for _, tc := range cases {
		r := base
		tc.mut(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: got %v, want ErrInvalidRequest", tc.name, err)
		}
	}
}

func TestDescriptorValidate(t *testing.T) {
	good := Descriptor{
		Name:           "evm-dev-1",
		SignatureTypes: []SignatureType{SigEVMMessage},
		AddressSources: []AddressSource{AddressSourceHeldKey},
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid descriptor rejected: %v", err)
	}
	cases := []struct {
		name string
		mut  func(*Descriptor)
	}{
		{"empty name", func(d *Descriptor) { d.Name = "" }},
		{"bad type", func(d *Descriptor) { d.SignatureTypes = []SignatureType{"EVM MESSAGE"} }},
		{"duplicate type", func(d *Descriptor) { d.SignatureTypes = []SignatureType{SigEVMMessage, SigEVMMessage} }},
		{"bad format", func(d *Descriptor) { d.Formats = []Format{"eip_1559"} }},
		{"duplicate format", func(d *Descriptor) { d.Formats = []Format{FormatEIP1559, FormatEIP1559} }},
		{"bad source", func(d *Descriptor) { d.AddressSources = []AddressSource{"unknown_source"} }},
		{"duplicate source", func(d *Descriptor) { d.AddressSources = []AddressSource{AddressSourceTest, AddressSourceTest} }},
	}
	for _, tc := range cases {
		d := good
		tc.mut(&d)
		if err := d.Validate(); err == nil {
			t.Errorf("%s: accepted invalid descriptor", tc.name)
		}
	}
	if !good.SupportsType(SigEVMMessage) || good.SupportsType(SigSolanaMessage) {
		t.Errorf("SupportsType disagrees with the declaration list")
	}
}

func TestSignedValidate(t *testing.T) {
	if err := (Signed{Signature: []byte("s"), Signer: "x"}).Validate(); err != nil {
		t.Fatalf("valid signed rejected: %v", err)
	}
	if err := (Signed{Signer: "x"}).Validate(); err == nil {
		t.Errorf("empty signature accepted")
	}
	if err := (Signed{Signature: []byte("s")}).Validate(); err == nil {
		t.Errorf("missing signer accepted")
	}
}

func TestAccountRefAndAllowList(t *testing.T) {
	addr := evmAddr(t, strings.ToLower(refEVM))
	ref := AccountRef{Network: evmNet, Address: addr}
	if err := ref.Validate(); err != nil {
		t.Fatalf("valid ref rejected: %v", err)
	}
	if _, err := NewAccountRef(evmNet, solAddr(t, "4wBqpZM9xaSheZzJSMawUKKwhdpChKbZ5eu5ky4Vigw")); err == nil {
		t.Errorf("cross-family ref accepted")
	}
	otherNet := identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "base", ChainID: "8453"}
	otherAddr := evmAddr(t, strings.ToLower("0x0000000000000000000000000000000000000001"))

	l := AllowList{ref}
	if err := l.Validate(); err != nil {
		t.Fatalf("valid list rejected: %v", err)
	}
	if err := (AllowList{ref, ref}).Validate(); err == nil {
		t.Errorf("duplicate entry accepted")
	}
	if err := (AllowList{{Network: evmNet, Address: identifiers.Address{Family: identifiers.FamilyEVM, Value: "nope"}}}).Validate(); err == nil {
		t.Errorf("bad address accepted")
	}

	req := Request{Network: evmNet, Account: addr, Type: SigEVMMessage, Payload: []byte("p")}
	if err := l.Authorize(req); err != nil {
		t.Fatalf("allowed request denied: %v", err)
	}
	// An empty list allows nothing (§12.3: no implicit signing rights).
	if err := (AllowList{}).Authorize(req); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("empty list: got %v, want ErrUnauthorized", err)
	}
	for name, mut := range map[string]func(*Request){
		"other network": func(r *Request) { r.Network = otherNet },
		"other account": func(r *Request) { r.Account = otherAddr },
		"other family":  func(r *Request) { r.Account = solAddr(t, "4wBqpZM9xaSheZzJSMawUKKwhdpChKbZ5eu5ky4Vigw") },
	} {
		r := req
		mut(&r)
		if err := l.Authorize(r); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%s: got %v, want ErrUnauthorized", name, err)
		}
	}
	// Accounts returns a copy: mutating it never widens the list.
	accounts := l.Accounts()
	accounts[0] = AccountRef{Network: evmNet, Address: otherAddr}
	if err := l.Authorize(req); err != nil {
		t.Errorf("mutating the returned copy changed the list")
	}
}

func TestSentinelsDistinct(t *testing.T) {
	sentinels := []error{ErrUnauthorized, ErrUnsupportedType, ErrInvalidRequest}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("%v wraps %v", a, b)
			}
		}
	}
}

func TestSecretRedaction(t *testing.T) {
	s := NewSecret([]byte("supersecretkey"))
	if s.IsZero() {
		t.Fatalf("secret reports zero")
	}
	if s.Len() != len("supersecretkey") {
		t.Errorf("Len = %d", s.Len())
	}
	if got := s.String(); got != "[redacted]" {
		t.Errorf("String = %q", got)
	}
	if got := fmt.Sprintf("%v %#v", s, s); got != "[redacted] [redacted]" {
		t.Errorf("fmt renders %q", got)
	}
	blob, err := json.Marshal(struct{ Key Secret }{Key: s})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "supersecret") {
		t.Errorf("json leaks material: %s", blob)
	}
	if !strings.Contains(string(blob), "[redacted]") {
		t.Errorf("json misses redaction marker: %s", blob)
	}
	// The material is only reachable through Bytes.
	if string(s.Bytes()) != "supersecretkey" {
		t.Errorf("Bytes lost the material")
	}
	// The secret copies on construction.
	raw := []byte("material")
	copied := NewSecret(raw)
	raw[0] = 'M'
	if string(copied.Bytes()) != "material" {
		t.Errorf("construction did not copy")
	}
	var zero Secret
	if !zero.IsZero() || zero.Len() != 0 || zero.Bytes() != nil {
		t.Errorf("zero secret misbehaves")
	}
}

func TestPayloadDigest(t *testing.T) {
	// SHA-256("abc"), hex encoded.
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := PayloadDigest([]byte("abc")); got != want {
		t.Errorf("digest = %s, want %s", got, want)
	}
	if PayloadDigest([]byte("abc")) == PayloadDigest([]byte("abd")) {
		t.Errorf("collision on near inputs")
	}
}

// fakeClock is a fixed clock for audit entries.
type fakeClock struct{ at time.Time }

func (f fakeClock) Now() time.Time { return f.at }

func TestWithAudit(t *testing.T) {
	at := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	addr := evmAddr(t, strings.ToLower(refEVM))
	inner, err := NewTestSigner("audit-test", AccountRef{Network: evmNet, Address: addr})
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	var entries []AuditEntry
	s := WithAudit(inner, func(e AuditEntry) { entries = append(entries, e) }, fakeClock{at})

	req := Request{Network: evmNet, Account: addr, Type: SigEVMMessage, Payload: []byte("hello")}
	if _, err := s.Sign(context.Background(), req); err != nil {
		t.Fatalf("sign: %v", err)
	}
	denied := req
	denied.Account = evmAddr(t, strings.ToLower("0x0000000000000000000000000000000000000009"))
	if _, err := s.Sign(context.Background(), denied); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("denied sign: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("audit entries = %d, want 2", len(entries))
	}
	allowedEntry, deniedEntry := entries[0], entries[1]
	if allowedEntry.Denied != "" || deniedEntry.Denied == "" {
		t.Errorf("denied fields wrong: %+v %+v", allowedEntry, deniedEntry)
	}
	for _, e := range entries {
		if e.At != at {
			t.Errorf("At = %s, want %s", e.At, at)
		}
		if e.Signer != "audit-test" {
			t.Errorf("Signer = %q", e.Signer)
		}
		if e.Network != evmNet {
			t.Errorf("Network = %v", e.Network)
		}
		if e.Type != SigEVMMessage {
			t.Errorf("Type = %q", e.Type)
		}
		if e.Digest != PayloadDigest([]byte("hello")) {
			t.Errorf("Digest = %q", e.Digest)
		}
	}
	// A nil hook returns the signer unchanged.
	if WithAudit(inner, nil, nil) != inner {
		t.Errorf("nil hook wrapped the signer")
	}
	// The decorator forwards descriptor and accounts.
	if s.Descriptor().Name != inner.Descriptor().Name || s.Descriptor().Unsafe != inner.Descriptor().Unsafe {
		t.Errorf("descriptor not forwarded")
	}
	if len(s.Accounts()) != 1 {
		t.Errorf("accounts not forwarded")
	}
}

func TestTestSigner(t *testing.T) {
	addr := evmAddr(t, strings.ToLower(refEVM))
	otherAddr := evmAddr(t, strings.ToLower("0x0000000000000000000000000000000000000001"))
	s, err := NewTestSigner("unit", AccountRef{Network: evmNet, Address: addr})
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	if _, err := NewTestSigner(""); err == nil {
		t.Errorf("empty name accepted")
	}
	if _, err := NewTestSigner("x", AccountRef{Network: evmNet, Address: otherAddr}, AccountRef{Network: evmNet, Address: otherAddr}); err == nil {
		t.Errorf("duplicate account accepted")
	}
	if _, err := NewTestSigner("x", AccountRef{}); err == nil {
		t.Errorf("empty account ref accepted")
	}

	d := s.Descriptor()
	if !d.Unsafe || d.AddressSources[0] != AddressSourceTest {
		t.Errorf("descriptor must declare the test identity, got %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("descriptor invalid: %v", err)
	}

	req := Request{Network: evmNet, Account: addr, Type: SigSolanaMessage, Payload: []byte("p")}
	first, err := s.Sign(context.Background(), req)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("signed invalid: %v", err)
	}
	if first.Signer != "unit" {
		t.Errorf("Signer = %q", first.Signer)
	}
	again, err := s.Sign(context.Background(), req)
	if err != nil || string(again.Signature) != string(first.Signature) {
		t.Errorf("signatures not deterministic")
	}
	other := req
	other.Account = otherAddr
	if _, err := s.Sign(context.Background(), other); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("unlisted account: %v", err)
	}
	bad := req
	bad.Payload = nil
	if _, err := s.Sign(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("invalid request: %v", err)
	}
	// Signatures differ per account: the digest includes the target.
	otherRef, err := NewTestSigner("unit2", AccountRef{Network: evmNet, Address: otherAddr})
	if err != nil {
		t.Fatalf("signer2: %v", err)
	}
	o, err := otherRef.Sign(context.Background(), other)
	if err != nil {
		t.Fatalf("sign2: %v", err)
	}
	if string(o.Signature) == string(first.Signature) {
		t.Errorf("different accounts produced identical signatures")
	}
}
