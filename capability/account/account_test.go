package account

import (
	"context"
	"errors"
	"testing"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/identifiers"
)

var evmNet = identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"}

func evmAddr(t *testing.T, value string) identifiers.Address {
	t.Helper()
	addr, err := identifiers.NewEVMAddress(value)
	if err != nil {
		t.Fatalf("address: %v", err)
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

func solNet() identifiers.NetworkID {
	return identifiers.NetworkID{Family: identifiers.FamilySolana, Name: "mainnet-beta", ChainID: "solana"}
}

func TestOwnerKindValid(t *testing.T) {
	valid := []OwnerKind{OwnerExternal, OwnerHeldKey, OwnerContract, OwnerProgram, OwnerUnknown}
	for _, k := range valid {
		if !k.Valid() {
			t.Errorf("%s not valid", k)
		}
	}
	if (OwnerKind("delegated")).Valid() {
		t.Errorf("unknown owner kind accepted")
	}
}

func TestAccountValidate(t *testing.T) {
	balance, err := identifiers.NewAmount("1000000000", 9, "solana/mainnet-beta/solana:native")
	if err != nil {
		t.Fatalf("amount: %v", err)
	}
	base := Account{
		Network: evmNet,
		Address: evmAddr(t, "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"),
		Owner:   OwnerExternal,
		Details: []Detail{{Key: "has_code", Value: "false"}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid account rejected: %v", err)
	}
	// A program-derived account legitimately carries no balance and no key.
	program := base
	program.Owner = OwnerProgram
	program.NativeBalance = nil
	if err := program.Validate(); err != nil {
		t.Fatalf("program account rejected: %v", err)
	}
	program.NativeBalance = &balance
	if err := program.Validate(); err != nil {
		t.Fatalf("program account with balance rejected: %v", err)
	}
	bad := identifiers.Amount{Raw: "0x10", Decimals: 18}
	cases := []struct {
		name string
		mut  func(*Account)
	}{
		{"bad network", func(a *Account) { a.Network = identifiers.NetworkID{Family: "cosmos", Name: "x", ChainID: "1"} }},
		{"bad address", func(a *Account) { a.Address = identifiers.Address{Family: identifiers.FamilyEVM, Value: "nope"} }},
		{"family mismatch", func(a *Account) {
			a.Address = solAddr(t, "4wBqpZM9xaSheZzJSMawUKKwhdpChKbZ5eu5ky4Vigw")
		}},
		{"unknown owner", func(a *Account) { a.Owner = OwnerKind("delegated") }},
		{"bad balance", func(a *Account) { b := bad; a.NativeBalance = &b }},
		{"empty detail key", func(a *Account) { a.Details = append(a.Details, Detail{Value: "x"}) }},
		{"duplicate detail key", func(a *Account) { a.Details = append(a.Details, Detail{Key: "has_code", Value: "true"}) }},
	}
	for _, tc := range cases {
		a := base
		tc.mut(&a)
		if err := a.Validate(); err == nil {
			t.Errorf("%s: accepted an invalid account", tc.name)
		}
	}
}

// fakeSigner is a minimal signer.Signer for the allow-list question.
type fakeSigner struct{ accounts []signer.AccountRef }

func (f fakeSigner) Descriptor() signer.Descriptor {
	return signer.Descriptor{Name: "fake", SignatureTypes: []signer.SignatureType{signer.SigEVMMessage}}
}
func (f fakeSigner) Accounts() []signer.AccountRef { return f.accounts }
func (f fakeSigner) Sign(context.Context, signer.Request) (signer.Signed, error) {
	return signer.Signed{}, errors.New("fake signer does not sign")
}

func TestManagedBySigner(t *testing.T) {
	addr := evmAddr(t, "0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	other := evmAddr(t, "0x0000000000000000000000000000000000000001")
	ref, err := signer.NewAccountRef(evmNet, addr)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	s := fakeSigner{accounts: []signer.AccountRef{ref}}

	if _, err := ManagedBySigner(nil, addr); err == nil {
		t.Errorf("nil signer accepted")
	}
	if _, err := ManagedBySigner(s, identifiers.Address{Family: identifiers.FamilyEVM, Value: "nope"}); err == nil {
		t.Errorf("invalid address accepted")
	}
	if ok, err := ManagedBySigner(s, addr); err != nil || !ok {
		t.Errorf("listed address not managed (ok=%v, err=%v)", ok, err)
	}
	if ok, err := ManagedBySigner(s, other); err != nil || ok {
		t.Errorf("unlisted address reported managed (ok=%v, err=%v)", ok, err)
	}
	// An empty allow-list manages nothing.
	if ok, err := ManagedBySigner(fakeSigner{}, addr); err != nil || ok {
		t.Errorf("empty allow-list reported managed (ok=%v, err=%v)", ok, err)
	}
	// A signer that manages only a Solana account never manages an
	// EVM address, regardless of how the two addresses read.
	solRef, err := signer.NewAccountRef(solNet(), solAddr(t, "4wBqpZM9xaSheZzJSMawUKKwhdpChKbZ5eu5ky4Vigw"))
	if err != nil {
		t.Fatalf("solana ref: %v", err)
	}
	if ok, err := ManagedBySigner(fakeSigner{accounts: []signer.AccountRef{solRef}}, addr); err != nil || ok {
		t.Errorf("cross-family match (ok=%v, err=%v)", ok, err)
	}
}
