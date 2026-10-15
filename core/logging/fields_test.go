package logging

import (
	"log/slog"
	"testing"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// TestFieldKeys pins the spec §15.1 field names. The set is a contract:
// collectors key on these strings, so renaming one is a breaking change.
func TestFieldKeys(t *testing.T) {
	want := map[string]string{
		"app":            FieldApp,
		"module":         FieldModule,
		"network":        FieldNetwork,
		"op_id":          FieldOpID,
		"tx_id":          FieldTxID,
		"event_id":       FieldEventID,
		"stage":          FieldStage,
		"error_category": FieldErrorCategory,
		"retry_count":    FieldRetryCount,
	}
	if len(want) != 9 {
		t.Fatalf("field set = %d, want 9", len(want))
	}
	for spec, got := range want {
		if got != spec {
			t.Errorf("field %q renders as %q", spec, got)
		}
	}
}

// TestFieldHelpers pins that every helper emits its documented key, so a
// caller cannot accidentally log under a different name.
func TestFieldHelpers(t *testing.T) {
	cases := []struct {
		attr slog.Attr
		key  string
		want string
	}{
		{App("game"), FieldApp, "game"},
		{Module("evm"), FieldModule, "evm"},
		{Network("evm/mainnet/1"), FieldNetwork, "evm/mainnet/1"},
		{OpID("op-1"), FieldOpID, "op-1"},
		{TxID("0xabc"), FieldTxID, "0xabc"},
		{EventID("ev-1"), FieldEventID, "ev-1"},
		{Stage("submit"), FieldStage, "submit"},
		{RetryCount(3), FieldRetryCount, "3"},
	}
	for _, tc := range cases {
		if tc.attr.Key != tc.key {
			t.Errorf("key = %q, want %q", tc.attr.Key, tc.key)
		}
		if got := tc.attr.Value.String(); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
		}
	}
}

// TestErrorCategoryFieldFromClassifiedError pins the intended use: a line
// carries the taxonomy category, not the message.
func TestErrorCategoryFieldFromClassifiedError(t *testing.T) {
	err := cherrs.New(cherrs.RateLimited, "evm.balance_at", "429 from upstream")
	category, ok := cherrs.CategoryOf(err)
	if !ok {
		t.Fatal("a classified error must yield its category")
	}
	attr := ErrorCategory(string(category))
	if attr.Value.String() != "rate_limited" {
		t.Errorf("category = %q, want rate_limited", attr.Value.String())
	}
}
