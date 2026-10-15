// Package metrics defines the narrow metrics contract (spec §15.2,
// ADR-0007). The contract is three instruments and a label rule; the
// official Prometheus exporter is deferred to a later phase, and an
// in-memory collector lives in infrastructure/observability/memory.
//
// The contract sits in core/ because adapters and application services emit
// measurements, and adapters may not import infrastructure (ADR-0001). The
// implementations sit in infrastructure/ so a project can swap the backend
// without touching framework code.
package metrics

// Labels are the label set attached to one measurement. Label names are
// restricted to a fixed allowlist (see ValidateLabels) so no metric can
// carry a high-cardinality player id, transaction id, or arbitrary
// business string (spec §15.2).
type Labels map[string]string

// Counter accumulates a sum over matching label sets.
type Counter interface {
	// Add adds delta to the series identified by labels. It returns an
	// error when the labels are not allowed or the series limit is
	// reached.
	Add(labels Labels, delta float64) error
}

// Histogram records observations into fixed buckets.
type Histogram interface {
	// Observe records one value in the series identified by labels.
	Observe(labels Labels, value float64) error
}

// Gauge holds the current value of a series.
type Gauge interface {
	// Set stores value in the series identified by labels.
	Set(labels Labels, value float64) error
}

// Collector resolves metric instruments by name. Implementations must
// validate labels against the allowlist and must bound the number of
// distinct label sets per metric, so a runaway label cannot grow the
// series count without limit.
type Collector interface {
	Counter(name string) Counter
	Histogram(name string) Histogram
	Gauge(name string) Gauge
}
