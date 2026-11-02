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

## Entry points

- `cmd/chame` — the binary.
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
