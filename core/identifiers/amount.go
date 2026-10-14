package identifiers

import (
	"fmt"
	"math/big"
	"strings"
)

// Amount is an on-chain asset quantity (spec §6.4). Raw is the decimal
// integer string in the asset's smallest unit — never a float, never a
// human-readable formatted value. Decimals comes from trusted asset
// metadata; assets are not assumed to share one precision.
type Amount struct {
	Raw      string `json:"raw"`
	Decimals uint8  `json:"decimals"`
	AssetID  string `json:"asset_id"`
}

// NewAmount constructs and validates an amount. Raw must be a canonical
// decimal integer string: optional '-', then digits with no leading zero
// unless the value is exactly "0", and "-0" is rejected as
// non-canonical.
func NewAmount(raw string, decimals uint8, assetID string) (Amount, error) {
	a := Amount{Raw: raw, Decimals: decimals, AssetID: assetID}
	if err := a.Validate(); err != nil {
		return Amount{}, err
	}
	return a, nil
}

// Validate checks the raw string form. Float forms, empty strings, and
// non-canonical zeros are rejected (spec §6.4).
func (a Amount) Validate() error {
	if a.AssetID == "" {
		return fmt.Errorf("amount: asset id must not be empty")
	}
	if !validDecimal(a.Raw) {
		return fmt.Errorf("amount: %q is not a canonical decimal integer string", a.Raw)
	}
	return nil
}

// validDecimal enforces the canonical integer-string form.
func validDecimal(s string) bool {
	body := s
	if strings.HasPrefix(s, "-") {
		body = s[1:]
	}
	if body == "" {
		return false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return false
		}
	}
	if body == "0" && strings.HasPrefix(s, "-") {
		return false // -0
	}
	if len(body) > 1 && body[0] == '0' {
		return false // leading zero
	}
	return true
}

// big returns the value as a big integer; Validate must have passed.
func (a Amount) big() *big.Int {
	v, _ := new(big.Int).SetString(a.Raw, 10)
	return v
}

// sameAsset reports whether two amounts denote the same asset with the
// same precision; only then may they combine (spec §6.4: 不同资产的金额
// 不可直接相加).
func (a Amount) sameAsset(o Amount) bool {
	return a.AssetID == o.AssetID && a.Decimals == o.Decimals
}

// Add returns a + o. Mixing assets or precisions is an error, not an
// implicit conversion.
func (a Amount) Add(o Amount) (Amount, error) {
	if !a.sameAsset(o) {
		return Amount{}, fmt.Errorf("amount: cannot add different assets %q and %q", a.AssetID, o.AssetID)
	}
	sum := new(big.Int).Add(a.big(), o.big())
	return Amount{Raw: sum.String(), Decimals: a.Decimals, AssetID: a.AssetID}, nil
}

// Sub returns a - o. A negative result is an error: the shared model
// carries unsigned balances by default, and business cases that need
// signed quantities must opt in explicitly (spec §6.4).
func (a Amount) Sub(o Amount) (Amount, error) {
	if !a.sameAsset(o) {
		return Amount{}, fmt.Errorf("amount: cannot subtract different assets %q and %q", a.AssetID, o.AssetID)
	}
	d := new(big.Int).Sub(a.big(), o.big())
	if d.Sign() < 0 {
		return Amount{}, fmt.Errorf("amount: negative result subtracting %q from %q of asset %q", o.Raw, a.Raw, a.AssetID)
	}
	return Amount{Raw: d.String(), Decimals: a.Decimals, AssetID: a.AssetID}, nil
}

// Cmp compares two amounts of the same asset; different assets are an
// error.
func (a Amount) Cmp(o Amount) (int, error) {
	if !a.sameAsset(o) {
		return 0, fmt.Errorf("amount: cannot compare different assets %q and %q", a.AssetID, o.AssetID)
	}
	return a.big().Cmp(o.big()), nil
}
