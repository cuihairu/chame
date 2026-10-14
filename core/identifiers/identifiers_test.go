package identifiers

import (
	"strings"
	"testing"
)

func TestChainFamilyValid(t *testing.T) {
	for _, f := range []ChainFamily{FamilyEVM, FamilySolana} {
		if !f.Valid() {
			t.Errorf("%s should be valid", f)
		}
	}
	for _, f := range []ChainFamily{"", "cosmos", "EVM"} {
		if f.Valid() {
			t.Errorf("%q should be invalid", f)
		}
	}
}

func TestNetworkIDValidate(t *testing.T) {
	ok := NetworkID{Family: FamilyEVM, Name: "ethereum-mainnet", ChainID: "1"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid network rejected: %v", err)
	}
	if ok.String() != "evm/ethereum-mainnet/1" {
		t.Errorf("String() = %q", ok.String())
	}
	bad := []NetworkID{
		{Family: "cosmos", Name: "x", ChainID: "1"},
		{Family: FamilyEVM, Name: "", ChainID: "1"},
		{Family: FamilyEVM, Name: "Mainnet", ChainID: "1"},   // uppercase
		{Family: FamilyEVM, Name: "main net", ChainID: "1"},  // space
		{Family: FamilySolana, Name: "mainnet", ChainID: ""}, // empty chain id
	}
	for _, n := range bad {
		if err := n.Validate(); err == nil {
			t.Errorf("%+v should be rejected", n)
		}
	}
	if !(NetworkID{Family: FamilyEVM, Name: "a", ChainID: "1"}).Equal(NetworkID{Family: FamilyEVM, Name: "a", ChainID: "1"}) {
		t.Error("equal networks should compare equal")
	}
}

// EIP-55 test vectors from the EIP itself.
func TestEVMAddressEIP55(t *testing.T) {
	valid := []string{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed",
		"0xfB6916095ca1df60bB79Ce92cE3Ea74c37c5d359",
		"0xdbF03B407c01E7cD3CBea99509d93f8DDDC8C6FB",
		"0xD1220A0cf47c7B9Be7A2E6BA89F429762e7b9aDb",
	}
	for _, v := range valid {
		if _, err := NewEVMAddress(v); err != nil {
			t.Errorf("%s should be valid: %v", v, err)
		}
	}
	// Corrupt the checksum by swapping one case bit.
	corrupted := "0x5aaeb6053F3E94C9b9A09f33669435E7Ef1BeAed" // first hex lowercased
	if _, err := NewEVMAddress(corrupted); err == nil {
		t.Error("corrupted checksum should be rejected")
	}
}

func TestEVMAddressForms(t *testing.T) {
	lower := "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"
	upper := "0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED"
	for _, v := range []string{lower, upper} {
		if _, err := NewEVMAddress(v); err != nil {
			t.Errorf("%s should be accepted unchecksummed: %v", v, err)
		}
	}
	bad := []string{
		"",                               // empty
		"0x1234",                         // too short
		strings.Repeat("a", 41),          // too long
		"0xzz" + strings.Repeat("a", 38), // not hex
	}
	for _, v := range bad {
		if _, err := NewEVMAddress(v); err == nil {
			t.Errorf("%q should be rejected", v)
		}
	}
}

func TestSolanaAddress(t *testing.T) {
	// Well-known 32-byte base58 public keys.
	valid := []string{
		"11111111111111111111111111111111",            // system program
		"So11111111111111111111111111111111111111112", // wrapped SOL mint
	}
	for _, v := range valid {
		if _, err := NewSolanaAddress(v); err != nil {
			t.Errorf("%s should be valid: %v", v, err)
		}
	}
	bad := []string{
		"",                                   // empty
		"0l11111111111111111111111111111111", // '0' not in base58
		"III11111111111111111111111111111",   // 'I' not in base58
		"111111111111111111111111111111111",  // 33 bytes (too long)
		"1111111111111111111111111111111",    // 31 bytes (too short)
	}
	for _, v := range bad {
		if _, err := NewSolanaAddress(v); err == nil {
			t.Errorf("%q should be rejected", v)
		}
	}
}

func TestAddressFamilyMixing(t *testing.T) {
	evm, _ := NewEVMAddress("0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed")
	sol, _ := NewSolanaAddress("11111111111111111111111111111111")
	if evm.Equal(sol) {
		t.Error("addresses of different families must never be equal")
	}
	// A solana value under the evm family is rejected at validation.
	mixed := Address{Family: FamilyEVM, Value: sol.Value}
	if err := mixed.Validate(); err == nil {
		t.Error("solana bytes under evm family should be rejected")
	}
}

func TestAmountValidAndInvalid(t *testing.T) {
	if _, err := NewAmount("1234500000000000000", 18, "asset-1"); err != nil {
		t.Fatalf("valid amount rejected: %v", err)
	}
	bad := []string{
		"",     // empty
		"1.5",  // float
		"1e18", // exponent
		"0x10", // hex
		"01",   // leading zero
		"-0",   // negative zero
		" ",    // blank
		"1 ",   // trailing space
		"abc",  // junk
	}
	for _, raw := range bad {
		if _, err := NewAmount(raw, 18, "asset-1"); err == nil {
			t.Errorf("%q should be rejected", raw)
		}
	}
	if _, err := NewAmount("1", 18, ""); err == nil {
		t.Error("empty asset id should be rejected")
	}
	// Negative values parse; carrying signed quantities is a business
	// decision made explicit by the caller.
	if _, err := NewAmount("-5", 8, "asset-1"); err != nil {
		t.Errorf("signed amount rejected: %v", err)
	}
}

func TestAmountArithmetic(t *testing.T) {
	a, _ := NewAmount("100", 6, "asset-1")
	b, _ := NewAmount("23", 6, "asset-1")
	sum, err := a.Add(b)
	if err != nil || sum.Raw != "123" {
		t.Errorf("add = %v, %v", sum, err)
	}
	d, err := a.Sub(b)
	if err != nil || d.Raw != "77" {
		t.Errorf("sub = %v, %v", d, err)
	}
	if _, err := a.Sub(sum); err == nil {
		t.Error("negative result should be rejected")
	}
	// Same asset id but different precision does not combine silently.
	c, _ := NewAmount("1", 8, "asset-1")
	if _, err := a.Add(c); err == nil {
		t.Error("precision mismatch should be rejected")
	}
	// Different assets never combine (spec §6.4).
	other, _ := NewAmount("1", 6, "asset-2")
	if _, err := a.Add(other); err == nil {
		t.Error("cross-asset add should be rejected")
	}
	if _, err := a.Cmp(other); err == nil {
		t.Error("cross-asset compare should be rejected")
	}
	if n, err := a.Cmp(b); err != nil || n <= 0 {
		t.Errorf("cmp(a,b) = %d, %v, want > 0", n, err)
	}
	// Big values stay exact (no float path anywhere).
	big1, _ := NewAmount("123456789012345678901234567890", 18, "big")
	big2, _ := NewAmount("1", 18, "big")
	bs, _ := big1.Add(big2)
	if bs.Raw != "123456789012345678901234567891" {
		t.Errorf("big add = %s", bs.Raw)
	}
}

func TestAssetIDValidate(t *testing.T) {
	net := NetworkID{Family: FamilyEVM, Name: "mainnet", ChainID: "1"}
	native := AssetID{Network: net, Kind: KindNative}
	if err := native.Validate(); err != nil {
		t.Fatalf("native asset rejected: %v", err)
	}
	if native.String() != "evm/mainnet/1:native" {
		t.Errorf("String() = %q", native.String())
	}
	token := AssetID{Network: net, Kind: KindEVMToken, Address: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"}
	if err := token.Validate(); err != nil {
		t.Fatalf("evm token rejected: %v", err)
	}
	bad := []AssetID{
		{Network: net, Kind: KindNative, Address: "0x1"},            // native with address
		{Network: net, Kind: KindEVMToken},                          // token without address
		{Network: net, Kind: "nft"},                                 // unknown kind
		{Network: net, Kind: KindSPLToken, Address: "not-base58!!"}, // wrong family encoding
		{Network: NetworkID{Family: "x", Name: "n", ChainID: "1"}, Kind: KindNative},
	}
	for _, a := range bad {
		if err := a.Validate(); err == nil {
			t.Errorf("%+v should be rejected", a)
		}
	}
	if !token.Equal(AssetID{Network: net, Kind: KindEVMToken, Address: "0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed"}) {
		t.Error("identical assets should compare equal")
	}
	if token.Equal(native) {
		t.Error("different assets should not compare equal")
	}
}

func TestBase58Decode(t *testing.T) {
	// 32 zero bytes encode as 32 '1's.
	if b, err := base58Decode("11111111111111111111111111111111"); err != nil || len(b) != 32 {
		t.Errorf("zero decode = %d bytes, %v", len(b), err)
	}
	if _, err := base58Decode(""); err == nil {
		t.Error("empty string should be rejected")
	}
	if _, err := base58Decode("0OIl"); err == nil {
		t.Error("ambiguous characters should be rejected")
	}
}
