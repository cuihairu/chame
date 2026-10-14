package identifiers

import (
	"fmt"
	"math/big"
)

// base58Alphabet is the Bitcoin/Solana base58 alphabet.
const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index [256]int8

func init() {
	for i := range base58Index {
		base58Index[i] = -1
	}
	for i := 0; i < len(base58Alphabet); i++ {
		base58Index[base58Alphabet[i]] = int8(i)
	}
}

// base58Decode decodes a base58 string into bytes. It rejects the empty
// string and any character outside the alphabet; a leading '1' counts as
// a leading zero byte.
func base58Decode(s string) ([]byte, error) {
	if s == "" {
		return nil, fmt.Errorf("base58: empty string")
	}
	zeros := 0
	for zeros < len(s) && s[zeros] == '1' {
		zeros++
	}
	num := new(big.Int)
	radix := big.NewInt(58)
	for i := 0; i < len(s); i++ {
		d := base58Index[s[i]]
		if d < 0 {
			return nil, fmt.Errorf("base58: invalid character %q at %d", string(s[i]), i)
		}
		num.Mul(num, radix)
		num.Add(num, big.NewInt(int64(d)))
	}
	body := num.Bytes()
	out := make([]byte, zeros+len(body))
	copy(out[zeros:], body)
	return out, nil
}
