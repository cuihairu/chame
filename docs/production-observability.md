# Production observability

`infrastructure/observability/prometheus.Handler` serves `/metrics` in Prometheus text exposition 0.0.4. Prometheus text was chosen because a scrape endpoint needs no client library or collector process in Chame and Prometheus can ingest it directly. OTLP is not the default: it requires an OTLP collector/export pipeline dependency and deployment configuration. No telemetry is sent externally by default; scraping and any remote storage are operator configured. There is no cloud backup.

## Signals

| Metric | Type | Meaning / alert input |
|---|---|---|
| `transactions_submitted_total` | counter | Submitted transactions by network |
| `transactions_confirmed_total` | counter | Confirmed transactions by network |
| `transactions_failed_total` | counter | Failures by network and bounded error category |
| `transaction_confirmation_seconds` | histogram | Confirmation latency |
| `event_poll_lag_seconds` | gauge | Event source polling lag |
| `queue_backlog` | gauge | Recovery or handler queue depth |
| `rpc_errors_total` | counter | RPC errors by network and bounded category |
| `transaction_timeouts_total` | counter | Transaction timeouts by network |

Only bounded labels such as network, handler, status and error category are allowed. The business idempotency key is written to transaction lifecycle logs as `idempotency_key` for internal correlation and used by the cost query; it is never a metric label. Do not attach player IDs, transaction IDs, keys, or arbitrary business strings to metrics.

## Recommended alerts

Tune windows and thresholds to workload; these are starting points, not an embedded alert manager:

```promql
sum(rate(transactions_failed_total[5m])) / clamp_min(sum(rate(transactions_failed_total[5m])) + sum(rate(transactions_confirmed_total[5m])), 1) > 0.02
queue_backlog > 100
histogram_quantile(0.95, sum by (le) (rate(transaction_confirmation_seconds_bucket[10m]))) > 60
max(event_poll_lag_seconds) > 30
increase(transaction_timeouts_total[5m]) > 0
```

The first rule is a failure ratio, the second backlog, the third latency, the fourth event lag, and the fifth timeout presence. Chame exposes signals and does not connect an external alerting system.

## Cost lookup

Record observed gas and fee via `CostRecorder.Record`; fee is a decimal string in the chain's smallest unit to preserve integer precision. `CostQuery.AggregateByIdempotencyKey(ctx, key)` returns transaction count, total gas, and total fee grouped by network so denominations are not mixed. Production applications should provide a durable implementation of these interfaces; the included store is in-memory reference storage and fake environments should use deterministic cost values. Cost records and idempotency keys remain internal; no cloud export or backup is enabled by Chame.
