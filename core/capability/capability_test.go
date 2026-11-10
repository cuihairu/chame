package capability

import "testing"

// TestNameValid pins the declaration rules: lowercase letters, digits,
// dash and dot, non-empty, at most 63 bytes (spec §6.1). Names are the
// cross-layer contract vocabulary, so anything that could be confused
// with another name or break registry rendering must be rejected.
func TestNameValid(t *testing.T) {
	cases := []struct {
		name  Name
		valid bool
	}{
		{"", false},
		{"network", true},
		{"account", true},
		{"asset", true},
		{"transaction", true},
		{"event", true},
		{"finality", true},
		{"signer", true},
		{"contract", true},
		{"a", true},
		{"0123456789", true},
		{"evm-mainnet.finality", true},
		{Name(string(make63())), true},
		{Name(string(make64())), false},
		{"Network", false},
		{"NETWORK", false},
		{"net work", false},
		{"net_work", false},
		{"net+work", false},
		{"网络", false},
		{"net\x00work", false},
	}
	for _, tc := range cases {
		if got := tc.name.Valid(); got != tc.valid {
			t.Errorf("%q: Valid()=%v, want %v", string(tc.name), got, tc.valid)
		}
	}
}

func make63() []byte {
	b := make([]byte, 63)
	for i := range b {
		b[i] = 'a'
	}
	return b
}

func make64() []byte {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	return b
}
