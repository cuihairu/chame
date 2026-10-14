# Chame

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8.svg)](https://go.dev)
[![CI](https://github.com/cuihairu/chame/actions/workflows/ci.yml/badge.svg)](https://github.com/cuihairu/chame/actions/workflows/ci.yml)

> **English** | [中文](README.zh.md)

**Chame** is a Go game backend framework that abstracts blockchains behind one
runtime. A game server talks to a single API; EVM and Solana are adapters
behind it.

## Why

Integrating one chain means one SDK, one key scheme, one event model. Adding a
second chain usually means rewriting the integration. Chame puts the chain
behind an adapter so the game side keeps one contract.

## Scope (from the spec)

| Area | Content |
|---|---|
| Runtime | lifecycle, scheduler, recovery |
| Adapters | EVM, Solana |
| Transaction model | shared state machine across chains |
| Reliability | idempotency keys, persistence, recovery worker |
| Events | cursor-based replay, reorg handling |
| Security | pluggable signer, permission boundary, dev keys |
| Providers | `AccountProvider`, `AssetProvider` |
| Ops | error taxonomy, logging, metrics, testing, CI |

## Status

**Phases 1–5 landed.** The full spec lives in
[`docs/design/chame-spec.md`](docs/design/chame-spec.md) — 24 chapters covering
architecture, adapters, transaction state machine, idempotency, event handling,
signing, error model, observability, testing, CI, phased rollout, and a
Definition of Done.

Shipped so far: the runtime lifecycle container (module registry, dependency
graph, graceful stop), the layered config system, the chain-agnostic identity
model (`NetworkID`, `Address`, `Amount`, `AssetID`), the shared transaction
model with its status state machine (`capability/transaction` — uncertainty
expressed via explicit unknown/reorged statuses, terminal states frozen), the
read-only EVM adapter (`adapters/evm` — multiple network instances, endpoint
failover, per-endpoint chain ID verification, health checks, native and ERC-20
balances, `eth_call`, log reads), and the read-only Solana adapter
(`adapters/solana` — independent cluster instances, per-endpoint genesis hash
verification, account / native / SPL token queries, transaction and signature
history, commitment configuration, explicit unsupported-capability errors),
the full `core/errors` taxonomy, and the persistence layer (§10 — repository
contract with idempotency uniqueness, conditional status updates and
reconciliation leases; in-memory store for tests; PostgreSQL store behind the
same conformance suite; idempotency resolution service; recovery worker with
lease-based multi-instance dedup). Architecture decisions live in
[`docs/design/decisions.md`](docs/design/decisions.md) with detailed ADRs in
[`docs/decisions/`](docs/decisions/). Phased implementation is tracked in
[`todo.md`](todo.md) — next: on-chain event handling.

## Development

Requires Go 1.26+.

```sh
make ci        # gofmt check + vet + build + test
make test-race
make lint      # golangci-lint
```

CI runs the same checks on Go 1.26.x and 1.27.x, plus golangci-lint and a
dependency vulnerability check.

## License

[Apache-2.0](LICENSE).
