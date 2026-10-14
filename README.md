# Chame

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

**Design stage.** The full spec lives in
[`docs/design/chame-spec.md`](docs/design/chame-spec.md) — 24 chapters covering
architecture, adapters, transaction state machine, idempotency, event handling,
signing, error model, observability, testing, CI, phased rollout, and a
Definition of Done.

No implementation code yet. The phased plan and open decisions are tracked in
[`todo.md`](todo.md).

## License

TBD — see [todo.md](todo.md).
