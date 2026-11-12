package config

import (
	"bytes"
	"testing"
)

// getenvStub answers a fixed table, standing in for the environment.
func getenvStub(table map[string]string) func(string) string {
	return func(name string) string { return table[name] }
}

// A value that itself contains a reference is written verbatim: a
// self-referencing or mutually referencing variable would re-enter the
// scan and expand forever. Values land in the config as they are, and
// the validation layer rejects an unusable result with its normal
// message.
func TestExpandEnvRefsDoesNotReenterValues(t *testing.T) {
	getenv := getenvStub(map[string]string{
		"A":   "${A}",
		"B":   "${C}",
		"C":   "${B}",
		"URL": "http://example.test/rpc",
	})
	for _, tc := range []struct{ in, want string }{
		{"url: ${URL}", "url: http://example.test/rpc"},
		{"${A}", "${A}"},
		{"${B}", "${C}"},
		{"missing: ${NOPE}!", "missing: !"},
		{"${}", ""},
		{"no closing ${brace", "no closing ${brace"},
		{"${X}${Y}z", "z"},
	} {
		if got := expandEnvRefs([]byte(tc.in), getenv); !bytes.Equal(got, []byte(tc.want)) {
			t.Errorf("expand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
