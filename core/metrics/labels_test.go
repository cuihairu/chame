package metrics

import "testing"

func TestAllowedLabels(t *testing.T) {
	got := AllowedLabels()
	want := []string{LabelEndpoint, LabelErrorCategory, LabelHandler, LabelMethod, LabelModule, LabelNetwork, LabelState}
	if len(got) != len(want) {
		t.Fatalf("allowed labels = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("allowed[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestValidateLabelsRejectsHighCardinality pins spec §15.2: a player id,
// transaction id, or any other arbitrary business string may not be a
// metric label.
func TestValidateLabelsRejectsHighCardinality(t *testing.T) {
	for _, tc := range []struct {
		name   string
		labels Labels
	}{
		{"player id", Labels{LabelModule: "evm", "player_id": "p123"}},
		{"transaction id", Labels{"tx_hash": "0xabc"}},
		{"arbitrary business string", Labels{"campaign": "spring-sale"}},
		{"comma in value", Labels{LabelNetwork: "evm,mainnet"}},
	} {
		if err := ValidateLabels(tc.labels); err == nil {
			t.Errorf("%s: %v must be rejected", tc.name, tc.labels)
		}
	}
	for _, tc := range []struct {
		name   string
		labels Labels
	}{
		{"empty", Labels{}},
		{"single", Labels{LabelModule: "evm"}},
		{"full", Labels{LabelModule: "evm", LabelNetwork: "evm/mainnet/1", LabelMethod: "eth_getBalance"}},
	} {
		if err := ValidateLabels(tc.labels); err != nil {
			t.Errorf("%s: %v must be allowed", tc.name, err)
		}
	}
}

func TestKeyIsStable(t *testing.T) {
	a := Labels{LabelModule: "evm", LabelNetwork: "evm/mainnet/1"}
	b := Labels{LabelNetwork: "evm/mainnet/1", LabelModule: "evm"}
	if Key(a) != Key(b) {
		t.Error("key must not depend on map order")
	}
	if Key(Labels{}) != "" {
		t.Error("empty labels must have an empty key")
	}
	if Key(a) == Key(Labels{LabelModule: "solana", LabelNetwork: "evm/mainnet/1"}) {
		t.Error("different label sets must not collide")
	}
}

func TestNames(t *testing.T) {
	got := Names()
	if len(got) != 14 {
		t.Fatalf("metric names = %d, want 14", len(got))
	}
	seen := make(map[string]bool)
	for _, n := range got {
		if seen[n] {
			t.Errorf("duplicate metric name %q", n)
		}
		seen[n] = true
	}
	for _, want := range []string{
		MetricRuntimeState, MetricModuleStartFailures, MetricRPCRequests,
		MetricRPCDuration, MetricEndpointHealth, MetricTransactionState,
		MetricTransactionConfirmationDuration, MetricNonceConflicts,
		MetricEventPollLag, MetricEventDuplicates, MetricEventHandlerFailures,
		MetricFinalityWaitDuration, MetricReorgs, MetricRecoveryBacklog,
	} {
		if !seen[want] {
			t.Errorf("metric %q missing", want)
		}
	}
}
