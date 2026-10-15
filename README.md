> **English** | [中文](README.zh.md)

<p align="center"><img src="assets/logo.jpg" alt="Chame logo" width="160" height="160"></p>

<h1 align="center">Chame</h1>

<p align="center">
  <a href="https://github.com/cuihairu/chame/actions"><img src="https://github.com/cuihairu/chame/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License"></a>
  <a href="https://go.dev/"><img src="https://img.shields.io/badge/go-1.26%2B-00ADD8?logo=go&logoColor=white" alt="Go"></a>
  <img src="https://img.shields.io/badge/status-design-orange" alt="Status">
</p>

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

**Phases 1–9 landed.** The full spec lives in
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
the full `core/errors` taxonomy, the persistence layer (§10 — repository
contract with idempotency uniqueness, conditional status updates and
reconciliation leases; in-memory store for tests; PostgreSQL store behind the
same conformance suite; idempotency resolution service; recovery worker with
lease-based multi-instance dedup), and on-chain event handling (§11 — the
shared event model with chain-aware position identities, the event repository
with cursor persistence and reorg invalidation, the delivery dispatcher with
at-least-once semantics, finality gating, retry-to-poison and reorg rollback,
plus registry slots for background tasks and event handlers), and signing and key
safety (§12 — the `capability/signer` contract with explicit target
network and account per request, allow-list authorization, payload-digest
audit entries, and a redacting secret type; a deterministic test signer;
the EVM and Solana development signers that refuse production
environments; `examples/signing`), and accounts and assets (§13 — the
`capability/account` and `capability/asset` contracts with per-owner-kind
records, signer-managed lookups answered from the signer's own allow-list,
native and token balances with per-asset precision, asset metadata, and
explicit chain-specific details, wired into both adapters so every EVM and
Solana network serves them as module capabilities), and observability
(§14–§15 — the retry policy that answers from the error category and the
operation's semantics rather than from a message; the §15.1 log field
contract with a redacting handler that scrubs secret-named fields and URL
credentials; the metrics contract with its closed label allowlist, the
framework's metric names, and an in-process collector; and health-check
scopes, so an unavailable optional Solana module leaves a service that
never touches Solana ready). Architecture decisions
live in [`docs/design/decisions.md`](docs/design/decisions.md) with detailed
ADRs in [`docs/decisions/`](docs/decisions/). Phased implementation is
tracked in [`todo.md`](todo.md) — next: the test system and CI.

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
