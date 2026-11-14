# Config / API

## Config loading

Config is layered (`core/config`, spec §5): code defaults, then a file, then
environment variables, then explicit overrides from the composition root.
Validation runs before the runtime starts and rejects unsafe shapes.

```sh
go doc github.com/cuihairu/chame/core/config
```

Key sections:

| Key | Type | Notes |
|---|---|---|
| `app.name` | string | Application name, used in logs and health reports. |
| `app.environment` | string | `development` / `staging` / `production`. |
| `app.shutdown_timeout` | duration | Bounds the runtime stop sequence. |
| `logging.level` | string | Log level. |
| `logging.format` | string | Log format (e.g. `json`). |
| `chains.evm[].name` | string | Logical EVM network name (unique across families). |
| `chains.evm[].chain_id` | string | Chain ID as a string (common layer avoids integer width). |
| `chains.evm[].rpc_urls` | string[] | Endpoints; failover across them. |
| `chains.evm[].timeout` | duration | Per-request timeout. |
| `chains.evm[].read_only` | bool | First release supports read-only EVM only. |
| `chains.solana[].name` | string | Logical Solana cluster name. |
| `chains.solana[].cluster` | string | Cluster identifier. |
| `chains.solana[].rpc_urls` | string[] | Endpoints; per-endpoint genesis hash verified. |
| `chains.solana[].commitment` | string | `processed` / `confirmed` / `finalized`. |
| `chains.solana[].timeout` | duration | Per-request timeout. |
| `chains.solana[].read_only` | bool | First release supports read-only Solana only. |
| `transactions.enabled` | bool | Feature gate; rejected at validation until the phase lands. |
| `events.enabled` | bool | Event pipeline feature gate. |

## Host-side tuning (Go code, not config keys)

These knobs are set from the composition root in Go, not in the YAML config
file. Zero values take the built-in defaults.

| Knob | Where | Default | Notes |
|---|---|---|---|
| `LogSourceConfig.Depth` | `adapters/evm/eventsource.go:45` | `1` (`adapters/evm/eventsource.go:24`) | Confirmation window: only blocks at least this deep are polled. |
| `LogSourceConfig.FinalityDepth` | `adapters/evm/eventsource.go:45` | `12` (`adapters/evm/eventsource.go:26`) | Block depth at which an event is final (ADR-0006). |
| `LogSourceConfig.FirstWindow` | `adapters/evm/eventsource.go:45` | `1000` (`adapters/evm/eventsource.go:31`) | Caps the block range the first poll after a fresh (zero-height) cursor scans — live subscription, not historical backfill. |
| `SignatureSourceConfig.Commitment` | `adapters/solana/eventsource.go:30` | the network's configured commitment (`finalized`, ADR-0006) | Zero takes the network default. |
| `SignatureSourceConfig.Limit` | `adapters/solana/eventsource.go:30` | `1000` (`adapters/solana/eventsource.go:26`) | Signature-history page size. The RPC rejects a literal `0`, so a non-positive limit omits the parameter and uses the node default page. |
| `PoolConfig.MaxOpen` / `MaxIdle` / `ConnMaxLifetime` | `infrastructure/persistence/sql/store.go:43` | `16` / matches `MaxOpen` / unset (`infrastructure/persistence/sql/store.go:39`) | database/sql pool behind the PostgreSQL store; an explicitly negative `MaxIdle` disables idle retention. |

## Host-side tuning (Go code surface, not config keys)

The tuning knobs below are set by the composition root in Go code; they are not YAML keys. Zero values take the built-in defaults.

| Knob | Location | Default | Notes |
|---|---|---|---|
| `LogSourceConfig.Depth` | `adapters/evm/eventsource.go:45` | `1` (`adapters/evm/eventsource.go:24`) | Confirmation window: poll only blocks at least this deep. |
| `LogSourceConfig.FinalityDepth` | `adapters/evm/eventsource.go:45` | `12` (`adapters/evm/eventsource.go:26`) | Block depth at which an event is final (ADR-0006). |
| `LogSourceConfig.FirstWindow` | `adapters/evm/eventsource.go:45` | `1000` (`adapters/evm/eventsource.go:31`) | Bounds the first poll scan when the cursor starts at zero height — live subscription, not a historical backfill. |
| `SignatureSourceConfig.Commitment` | `adapters/solana/eventsource.go:30` | the network's configured commitment (`finalized`, ADR-0006) | Zero value takes the network default. |
| `SignatureSourceConfig.Limit` | `adapters/solana/eventsource.go:30` | `1000` (`adapters/solana/eventsource.go:26`) | Signature-history page size. RPC rejects a literal `0`, so non-positive values omit the parameter and use the node default. |
| `PoolConfig.MaxOpen` / `MaxIdle` / `ConnMaxLifetime` | `infrastructure/persistence/sql/store.go:43` | `16` / matches `MaxOpen` / unset (`infrastructure/persistence/sql/store.go:39`) | database/sql pool for the PostgreSQL store; an explicitly negative `MaxIdle` disables idle retention. |

## Entry points

- `cmd/chame` — the binary.
- `cmd/chame-txload` — transaction load driver: pushes simulated or live traffic through the real pipeline (EVM endpoint pool, transaction state machine, idempotency flow, recovery worker) with fault injection and an embedded HTTP dashboard. With no `-endpoints` it hosts a local mock dev chain, so the whole loop runs with zero external dependencies.
- `core/runtime` — lifecycle container (module registry, dependency graph,
  graceful stop).
- `core/module` — module interface and registry.
- `core/identifiers` — chain-agnostic identity model (`NetworkID`, `Address`,
  `Amount`, `AssetID`).
- `capability/*` — capability contracts (transaction, event, signer, account,
  asset, nft, finality, network).
- `adapters/evm`, `adapters/solana` — read-only adapters.
- `application/*` — use cases (transaction, eventing, idempotency, gameledger,
  projection).
- `infrastructure/persistence/*` — memory and PostgreSQL stores.
- `infrastructure/signer/remote` — remote signer HTTP client
  (`infrastructure/signer/local` is a skeleton placeholder; the development
  signers `LocalDevSigner` live in `adapters/evm` and `adapters/solana`).
- `infrastructure/observability/*` — metrics and cost recording.

See the [spec](https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md) for the full contract set.
