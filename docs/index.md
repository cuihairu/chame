---
layout: home

hero:
  name: Chame
  text: Go game backend framework abstracting blockchains behind one runtime
  tagline: A game server talks to a single API; EVM and Solana are adapters behind it
  actions:
    - theme: brand
      text: Quick Start
      link: /quickstart
    - theme: alt
      text: Spec (24 chapters)
      link: https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md

features:
  - title: One Contract
    details: The game side keeps a single chain-agnostic contract; adapters hold each chain's SDK and key scheme behind it.
  - title: Transaction Model
    details: Shared status state machine across chains, with explicit unknown/reorged statuses and frozen terminal states.
  - title: Reliability
    details: Idempotency keys, persistence, a lease-based recovery worker, cursor-based event replay and reorg handling.
  - title: Security
    details: Pluggable signer, allow-list authorization, payload-digest audit, redacting secrets and logs, production guards.
---

![Chame game backend framework architecture](/images/architecture-en.svg)

## What is Chame

Chame is a **Go game backend framework** that abstracts blockchains behind one
runtime. A game server talks to a single API; EVM and Solana are adapters
behind it. Integrating one chain means one SDK, one key scheme, one event
model; adding a second chain usually means rewriting the integration. Chame
puts the chain behind an adapter so the game side keeps one contract.

The full spec lives in [`docs/design/chame-spec.md`](https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md) —
24 chapters covering architecture, adapters, transaction state machine,
idempotency, event handling, signing, error model, observability, testing, CI,
phased rollout, and a Definition of Done. Architecture decisions are summarized
in [`docs/design/decisions.md`](https://github.com/cuihairu/chame/blob/main/docs/design/decisions.md) with detailed ADRs under
[`docs/decisions/`](https://github.com/cuihairu/chame/tree/main/docs/decisions).

## Development

Requires Go 1.26+.

```sh
make ci        # gofmt check + vet + build + test
make test-race
make lint      # golangci-lint
```

CI runs the same checks on Go 1.26.x and 1.27.x, plus golangci-lint and a
dependency vulnerability check.
