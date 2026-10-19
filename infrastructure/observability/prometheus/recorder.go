package prometheus

import (
	"github.com/cuihairu/chame/core/metrics"
	"github.com/cuihairu/chame/infrastructure/observability/memory"
)

const (
	TransactionsSubmitted          = "transactions_submitted_total"
	TransactionsConfirmed          = "transactions_confirmed_total"
	TransactionsFailed             = "transactions_failed_total"
	TransactionConfirmationSeconds = "transaction_confirmation_seconds"
	EventPollLagSeconds            = "event_poll_lag_seconds"
	QueueBacklog                   = "queue_backlog"
	RPCErrors                      = "rpc_errors_total"
	TransactionTimeouts            = "transaction_timeouts_total"
)

// Recorder emits bounded-cardinality operational signals. Idempotency keys
// deliberately have no method accepting them: they are log/query correlation only.
type Recorder struct{ c *memory.Collector }

func NewRecorder(c *memory.Collector) *Recorder { return &Recorder{c: c} }
func (r *Recorder) TransactionSubmitted(network string) error {
	return r.c.Counter(TransactionsSubmitted).Add(metrics.Labels{"network": network}, 1)
}
func (r *Recorder) TransactionConfirmed(network string, seconds float64) error {
	if err := r.c.Counter(TransactionsConfirmed).Add(metrics.Labels{"network": network}, 1); err != nil {
		return err
	}
	return r.c.Histogram(TransactionConfirmationSeconds).Observe(metrics.Labels{"network": network}, seconds)
}
func (r *Recorder) TransactionFailed(network, category string) error {
	return r.c.Counter(TransactionsFailed).Add(metrics.Labels{"network": network, "error_category": category}, 1)
}
func (r *Recorder) TransactionTimeout(network string) error {
	return r.c.Counter(TransactionTimeouts).Add(metrics.Labels{"network": network}, 1)
}
func (r *Recorder) EventLag(network string, seconds float64) error {
	return r.c.Gauge(EventPollLagSeconds).Set(metrics.Labels{"network": network}, seconds)
}
func (r *Recorder) Backlog(queue string, size float64) error {
	return r.c.Gauge(QueueBacklog).Set(metrics.Labels{"handler": queue}, size)
}
func (r *Recorder) RPCError(network, category string) error {
	return r.c.Counter(RPCErrors).Add(metrics.Labels{"network": network, "error_category": category}, 1)
}
