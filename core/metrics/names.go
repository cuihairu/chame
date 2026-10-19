package metrics

import "sort"

// The metric names the framework emits (spec §15.2). They are declared
// here so the metric list is code rather than prose, and so every module
// that reports the same quantity names it the same way.
const (
	// MetricRuntimeState is the runtime lifecycle state, 0 or 1.
	MetricRuntimeState = "runtime_state"
	// MetricModuleStartFailures counts module start failures.
	MetricModuleStartFailures = "module_start_failures_total"
	// MetricRPCRequests counts RPC requests; error rate is the share
	// with a non-empty error_category label.
	MetricRPCRequests = "rpc_requests_total"
	// MetricRPCDuration is the RPC latency in seconds.
	MetricRPCDuration = "rpc_request_duration_seconds"
	// MetricEndpointHealth is 1 when an endpoint answers, 0 otherwise.
	MetricEndpointHealth = "endpoint_health"
	// MetricTransactionState is the current count per transaction
	// state; the unknown-state count is the series with state=unknown.
	MetricTransactionState = "transaction_state"
	// MetricTransactionConfirmationDuration is the confirmation time in
	// seconds.
	MetricTransactionConfirmationDuration = "transaction_confirmation_duration_seconds"
	// MetricNonceConflicts counts nonce reconciliation conflicts.
	MetricNonceConflicts = "nonce_conflicts_total"
	// MetricEventPollLag is the event read lag in seconds.
	MetricEventPollLag = "event_poll_lag_seconds"
	// MetricEventDuplicates counts duplicate event deliveries.
	MetricEventDuplicates = "event_duplicates_total"
	// MetricEventHandlerFailures counts handler failures.
	MetricEventHandlerFailures = "event_handler_failures_total"
	// MetricFinalityWaitDuration is the finality wait time in seconds.
	MetricFinalityWaitDuration = "finality_wait_duration_seconds"
	// MetricReorgs counts reorganizations detected.
	MetricReorgs = "reorg_total"
	// MetricRecoveryBacklog is the recovery task backlog size.
	MetricRecoveryBacklog                = "recovery_backlog"
	MetricTransactionsSubmitted          = "transactions_submitted_total"
	MetricTransactionsConfirmed          = "transactions_confirmed_total"
	MetricTransactionsFailed             = "transactions_failed_total"
	MetricTransactionConfirmationSeconds = "transaction_confirmation_seconds"
	MetricTransactionTimeouts            = "transaction_timeouts_total"
	MetricRPCErrorCategories             = "rpc_errors_total"
)

// Names lists every metric name the framework declares, sorted.
func Names() []string {
	out := []string{
		MetricRuntimeState,
		MetricModuleStartFailures,
		MetricRPCRequests,
		MetricRPCDuration,
		MetricEndpointHealth,
		MetricTransactionState,
		MetricTransactionConfirmationDuration,
		MetricNonceConflicts,
		MetricEventPollLag,
		MetricEventDuplicates,
		MetricEventHandlerFailures,
		MetricFinalityWaitDuration,
		MetricReorgs,
		MetricRecoveryBacklog,
		MetricTransactionsSubmitted,
		MetricTransactionsConfirmed,
		MetricTransactionsFailed,
		MetricTransactionConfirmationSeconds,
		MetricTransactionTimeouts,
		MetricRPCErrorCategories,
	}
	sort.Strings(out)
	return out
}
