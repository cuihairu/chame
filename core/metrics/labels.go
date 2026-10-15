package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// The allowed metric label names (spec §15.2). The set is closed: every
// label a caller may attach is listed here, and anything else is
// rejected. The rule the set encodes is that metric labels must not carry
// high-cardinality player ids, transaction ids, or arbitrary business
// strings — those belong in logs, not in metric series.
const (
	// LabelModule names the module or adapter, e.g. "evm", "solana".
	LabelModule = "module"
	// LabelNetwork names the logical network, e.g. "evm/mainnet/1".
	LabelNetwork = "network"
	// LabelMethod names the RPC or operation, e.g. "eth_getBalance".
	LabelMethod = "method"
	// LabelState names a lifecycle state, e.g. a transaction status.
	LabelState = "state"
	// LabelErrorCategory names an error taxonomy category.
	LabelErrorCategory = "error_category"
	// LabelEndpoint names an endpoint by its redacted URL. The count of
	// endpoints is bounded by configuration, not by traffic.
	LabelEndpoint = "endpoint"
	// LabelHandler names an event handler by its normalized type.
	LabelHandler = "handler"
)

// allowedLabels is the closed label-name set.
var allowedLabels = map[string]bool{
	LabelModule:        true,
	LabelNetwork:       true,
	LabelMethod:        true,
	LabelState:         true,
	LabelErrorCategory: true,
	LabelEndpoint:      true,
	LabelHandler:       true,
}

// AllowedLabels lists the label names any metric may carry, sorted.
func AllowedLabels() []string {
	out := make([]string, 0, len(allowedLabels))
	for name := range allowedLabels {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ValidateLabels rejects a label set that uses a name outside the
// allowlist, or a value that breaks series keys: commas would make two
// label sets collide on one series, so a single comma-free key stays a
// bijection. An empty label set is valid: an unlabelled series is the
// lowest-cardinality series there is.
func ValidateLabels(labels Labels) error {
	for name, value := range labels {
		if !allowedLabels[name] {
			return fmt.Errorf("metrics: label %q is not allowed; allowed labels: %s", name, strings.Join(AllowedLabels(), ", "))
		}
		if strings.Contains(value, ",") {
			return fmt.Errorf("metrics: label value for %q must not contain a comma", name)
		}
	}
	return nil
}

// Key renders a label set as a stable series identifier. Two label sets
// with the same content in a different order produce the same key.
func Key(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}
	parts := make([]string, 0, len(labels))
	for name, value := range labels {
		parts = append(parts, name+"="+value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}
