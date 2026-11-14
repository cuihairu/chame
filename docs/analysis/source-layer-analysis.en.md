# Source Layer Analysis

- [中文](/zh/analysis/source-layer-analysis)
- Glossary: [English](/glossary.en) | [中文](/zh/glossary)

Evidence in this page is pinned to repository commit
`c700e30d45106e905fd22f364fc4616e74454654` or newer (anchors are re-checked when a cited line moves). The six layers below describe
runtime responsibilities and dependency direction, not directory labels.

```text
Host / tools -> Runtime kernel -> Application workflows -> Chain adapters -> Chain APIs
                    |                    |
                    +--> Contracts <-----+
                    +-> Infrastructure -> PostgreSQL / external services
```

## 1. Runtime Kernel

**Responsibility and implementation.** `Runtime` owns process lifecycle,
dependency ordering, module registration, capability lookup, background-task
cancellation, and aggregate health (`core/runtime/runtime.go:51`). The state
machine progresses `New -> Configuring -> Initializing -> Starting ->
Running -> Stopping -> Stopped`; pre-running failures may enter stopping, and
stopped is terminal (`core/lifecycle/lifecycle.go:10`,
`core/lifecycle/lifecycle.go:59`). The graph resolves required and optional
module dependencies without a reflection container
(`core/runtime/graph.go:12`, `core/module/module.go:19`).

**State.** Runtime state, start order, and task cancellation are process
memory (`core/runtime/runtime.go:51`). Module registration is also in-memory,
but deliberately single-owner and runtime-scoped: capabilities, health checks,
tasks, and event handlers live in one `Store` (`core/module/module.go:88`).
No Redis, database, or file-backed runtime coordination exists.

**Call surface.** The composition root registers modules; the runtime checks
the graph, starts dependencies first, starts tasks after all modules, stops
tasks before modules, and stops modules in reverse order
(`core/runtime/runtime.go:131`, `core/runtime/runtime.go:297`,
`core/runtime/runtime.go:382`). Consumers query capabilities by name and
assert the interface themselves (`core/runtime/runtime.go:341`).

**Configuration and copy.** Shared options are logger, clock, and shutdown
timeout (`core/runtime/runtime.go:64`); process configuration comes from
layer 7 and is represented at `core/config/config.go:74`. User-facing state
names live with the lifecycle model (`core/lifecycle/lifecycle.go:28`);
error rendering lives with the classified error model
(`core/errors/errors.go:135`).

## 2. Chain-Neutral Contracts

**Responsibility and implementation.** These packages define what a chain
means to the host: account, asset, event, finality, network, NFT, signer, and
transaction (`capability/account/account.go:115`,
`capability/asset/asset.go:65`, `capability/event/repository.go:107`,
`capability/network/network.go:17`, `capability/nft/nft.go:40`,
`capability/signer/signer.go:229`,
`capability/transaction/repository.go:135`). Well-known capability names are
declared independently at `core/capability/capability.go:10`. The transaction
model includes terminal-freeze and uncertainty semantics
(`capability/transaction/transaction.go:32`,
`capability/transaction/statemachine.go:11`).

**State.** These are stateless interfaces, value models, validation rules,
and status transitions. There is no connection, lock, database, or durable
field at this layer. The repository interfaces specify persistence semantics
while implementations own actual rows (`capability/transaction/repository.go:135`,
`capability/event/repository.go:107`).

**Call surface.** Application and adapter code imports these contracts;
contracts do not import application, adapter, or infrastructure packages.
The runtime receives them through module registration and explicit assertions
(`core/module/module.go:104`, `adapters/evm/module.go:104`).

**Configuration, protocol, and copy.** No dedicated configuration schema or
file protocol belongs here. Payload-specific fields are values inside neutral
models, such as signer request/signed data
(`capability/signer/signer.go:170`, `capability/signer/signer.go:205`);
stable category strings and messages belong to the error contract
(`core/errors/errors.go:9`, `core/errors/errors.go:135`).

## 3. Application Workflows

**Responsibility and implementation.** This layer composes neutral contracts
into recoverable workflows. The transaction module assembles flow plus worker
and publishes only the flow when a transactor exists
(`application/transaction/module.go:15`, `application/transaction/module.go:124`).
`Flow` runs create through submit (`application/transaction/flow.go:26`);
`Worker` scans stale statuses under leases (`application/transaction/worker.go:133`,
`application/transaction/worker.go:237`). Eventing binds module handlers and
polls durable sources (`application/eventing/bind.go:15`,
`application/eventing/dispatcher.go:132`). Idempotency service checks request
hashes and returns prior accepted responses (`application/idempotency/service.go:35`).
Projection is an executable replay boundary with host-owned backends
(`application/projection/projection.go:18`, `application/gameledger/contract.go:32`).

**State.** Workflows hold injected repositories, sources, signers, clocks,
loggers, and metrics; they do not choose the storage technology.
`Worker` and `Dispatcher` are process-memory orchestration objects
(`application/transaction/worker.go:133`,
`application/eventing/dispatcher.go:132`). Durable state is owned by the
repository implementation in layer 5. The example projection ledger keeps a
process map and is explicitly a reference contract, not host storage
(`application/gameledger/contract.go:121`,
`application/gameledger/contract.go:132`).

**Call surface.**

| Caller | Channel | Callee | Evidence |
|---|---|---|---|
| Host / transaction capability consumer | direct Go interface | `transaction.Flow` | `application/transaction/module.go:135` |
| Runtime task scheduler | in-process task function | `transaction.Worker.Run` | `application/transaction/module.go:141` |
| `Worker` | `transaction.Repository` | memory or PostgreSQL store | `application/transaction/worker.go:138` |
| `Worker` | `Reconciler` / `StatusQuery` | EVM or Solana `StatusOf` | `application/transaction/control.go:13`, `adapters/evm/reconciler.go:14` |
| `Dispatcher` | `event.Source` | EVM log source or Solana signature source | `application/eventing/dispatcher.go:145`, `adapters/evm/eventsource.go:65` |
| `Dispatcher` | `event.Repository` | memory or PostgreSQL event store | `application/eventing/dispatcher.go:132` |
| Runtime / host | module `Registry` | bound event handlers | `application/eventing/bind.go:15` |

**Configuration and lifecycle.** Retry, scan interval, task timeout,
concurrency, and metrics are constructor options rather than global config:
worker settings at `application/transaction/worker.go:52`; dispatcher retry
settings at `application/eventing/dispatcher.go:33`. Runtime starts and stops
these long-running loops as registered tasks
(`core/module/module.go:104`, `application/transaction/module.go:135`).
Copy embedded in logs and reports is workflow-local
(`application/transaction/worker.go:363`, `application/eventing/dispatcher.go:222`).

## 4. Chain Adapters

**Responsibility and implementation.** EVM and Solana adapters isolate SDK
types, wire calls, identity verification, endpoint failover, event polling,
family transaction records, and status mapping. EVM exposes module, network,
account, asset, event, flow, and status capabilities
(`adapters/evm/module.go:20`, `adapters/evm/network.go:110`); Solana does the
same independently (`adapters/solana/module.go:20`,
`adapters/solana/network.go:146`). Family preparation and chain records are
deliberately separate: EVM nonce/receipt mapping (`adapters/evm/nonce.go:20`,
`adapters/evm/tx.go:15`) versus Solana blockhash windows
(`adapters/solana/blockhash.go:20`, `adapters/solana/reconciler.go:19`).

**State.** Endpoint pools, current identities, nonce allocation, and
blockhash pins are process memory inside the adapter
(`adapters/evm/pool.go:18`, `adapters/evm/nonce.go:20`,
`adapters/solana/blockhash.go:20`). Durable transaction history is delegated
to repositories; chain-shaped evidence is stored in a transaction's `ChainData`
(`capability/transaction/transaction.go:129`,
`adapters/solana/reconciler.go:19`).

**Call surface.** Adapter methods call vendor SDK clients over HTTP/WS JSON-RPC.
EVM isolates go-ethereum behind local interfaces and the endpoint pool
(`adapters/evm/client.go:28`, `adapters/evm/pool.go:81`); Solana does the same
for gagliardetto (`adapters/solana/client.go:18`,
`adapters/solana/pool.go:22`). Application code reaches adapters only through
`StatusQuery`, `Transactor`, and `event.Source`; hosts reach read capabilities
through published adapters.

**Configuration and copy.** Adapter assembly uses in-memory config structs,
not the file schema directly: EVM at `adapters/evm/config.go:41`; Solana at
`adapters/solana/config.go:36`. No standalone protocol schema file exists;
JSON tags describe stored chain records and RPC payloads
(`adapters/evm/flow.go:31`, `adapters/solana/reconciler.go:19`). Package
documentation and behavior notes are the adapters' copy surface
(`adapters/evm/doc.go:1`, `adapters/solana/doc.go:1`).

## 5. Infrastructure Implementations

**Responsibility and implementation.** This layer supplies replaceable
technical effects. PostgreSQL owns durable transaction and event storage plus
versioned migrations (`infrastructure/persistence/sql/store.go:77`,
`infrastructure/persistence/sql/events.go:18`,
`infrastructure/persistence/sql/store.go:92`). Memory stores exist for tests
and examples and say so explicitly
(`infrastructure/persistence/memory/memory.go:22`,
`infrastructure/persistence/memory/events.go:19`). The remote signer is an
HTTP custody integration (`infrastructure/signer/remote/remote.go:68`);
Prometheus recorder/exporter and memory metrics are observability
implementations (`infrastructure/observability/prometheus/recorder.go:13`,
`infrastructure/observability/prometheus/exporter.go:17`).
The local-signer package is only a development skeleton
(`infrastructure/signer/local/doc.go:1`).

**State.**

| Implementation | Storage | Evidence |
|---|---|---|
| SQL transaction store | PostgreSQL rows via one `*sql.DB` pool | `infrastructure/persistence/sql/store.go:77`, `infrastructure/persistence/sql/store.go:114` |
| SQL event store | PostgreSQL event/cursor rows; coordination through database constraints | `infrastructure/persistence/sql/events.go:18`, `infrastructure/persistence/sql/events.go:29` |
| Memory transaction store | maps guarded by one mutex | `infrastructure/persistence/memory/memory.go:22` |
| Memory event store | event and cursor maps guarded by one mutex | `infrastructure/persistence/memory/events.go:19` |
| Remote signer | no local persistence; HTTP request/response to custody service | `infrastructure/signer/remote/remote.go:68`, `infrastructure/signer/remote/remote.go:130` |
| Prometheus memory collector | process counters, gauges, histogram snapshots | `infrastructure/observability/prometheus/recorder.go:13` |
| Memory metrics collector | process maps/gauges/histograms | `infrastructure/observability/memory/memory.go:27` |

There is no Redis, MySQL, Kafka, NATS, or file-store implementation.

**Call surface.** Application workflows call repository interfaces; the host
composition root selects this layer. SQL uses pgx through `database/sql`
(`infrastructure/persistence/sql/store.go:18`,
`infrastructure/persistence/sql/store.go:114`); remote signing uses HTTP
with a bearer token (`infrastructure/signer/remote/remote.go:18`,
`infrastructure/signer/remote/remote.go:130`); Prometheus exposes text over
an injected writer (`infrastructure/observability/prometheus/exporter.go:17`).

**Configuration, protocol, and copy.** DSNs and service endpoints are host
inputs, not part of `core/config.Config`. PostgreSQL DDL is embedded and
version-tracked (`infrastructure/persistence/sql/store.go:78`,
`infrastructure/persistence/sql/store.go:92`); remote signing request/response
types are local JSON structs (`infrastructure/signer/remote/remote.go:80`).
Operational caveats live in package docs (`infrastructure/persistence/memory/doc.go:1`,
`infrastructure/persistence/sql/doc.go:1`).

## 6. Host, Tools, Examples, and Conformance

**Responsibility and implementation.** Examples demonstrate explicit assembly
without inventing framework behavior (`examples/minimal/main.go:27`,
`examples/eventstream/main.go:42`, `examples/signing/main.go:20`). The
transaction load tool builds its own dev chain, driver, HTTP dashboard, and
pipeline assembly (`tools/txload/devchain/chain.go:83`,
`tools/txload/driver.go:173`, `tools/txload/http.go:27`). Conformance suites
make adapter and repository behavior checkable without binding tests to one
implementation (`tests/conformance/adapter/adapter.go:25`,
`tests/conformance/repository/repository.go:20`). The production CLI is still
a skeleton (`cmd/chame/main.go:12`).

**State and call surface.** Tool state is process memory plus channels and an
HTTP dashboard (`tools/txload/driver.go:193`, `tools/txload/http.go:27`);
the mock chain owns simulated blocks and transactions and exposes JSON-RPC
over HTTP (`tools/txload/devchain/chain.go:83`,
`tools/txload/devchain/chain.go:241`). Examples call adapters directly and do
not run `Runtime`. Conformance suites call subjects through contracts. No
host-level persistence is invented here.

**Configuration and copy.** Tool options come from command flags
(`cmd/chame-txload/main.go:39`), examples use constants and local fixtures,
and conformance fixtures are structs. Site copy for public usage lives under
`docs/`, while this analysis and the full specification remain repository
documents (`docs/.vitepress/config.ts:9`).

## Why These Six Layers

1. **Runtime orchestration cannot be merged into contracts because it owns
   start-order and termination policy, while contracts stay callable after
   the runtime stops.** Registration metadata and graph validation are
   separate inputs to `Runtime.Start`
   (`core/module/module.go:44`, `core/runtime/graph.go:19`), and lifecycle
   transition legality is modeled independently
   (`core/lifecycle/lifecycle.go:59`).
2. **Neutral contracts are separated because application and adapter code
   depend only on semantics, enabling family implementations and conformance
   suites without importing each other.** Transaction, event, adapter, and
   repository suites all program against these seams
   (`tests/conformance/adapter/adapter.go:25`,
   `tests/conformance/repository/repository.go:20`).
3. **Workflows are separated because reliability policy is shared across
   families even though submission mechanics are not.** The worker calls one
   injected `Reconciler` and one repository (`application/transaction/worker.go:133`),
   while EVM and Solana each map their own chain evidence
   (`adapters/evm/reconciler.go:14`, `adapters/solana/reconciler.go:19`).
4. **Adapters are separated per family because the irreducible details differ
   at transport and recovery-record level.** EVM verifies chain ID and allocates
   nonces (`adapters/evm/network.go:149`, `adapters/evm/nonce.go:20`); Solana
   verifies genesis and pins blockhash windows
   (`adapters/solana/network.go:60`, `adapters/solana/blockhash.go:20`).
5. **Infrastructure is separated because persistence, custody, and metrics
   have different deployment lifetimes and owners than workflow policy.** SQL
   migration runs idempotently per process start
   (`infrastructure/persistence/sql/store.go:92`), memory stores disclaim
   cross-process coordination (`infrastructure/persistence/memory/memory.go:22`),
   and the remote client only mediates an external signer
   (`infrastructure/signer/remote/remote.go:68`).
6. **Host/tool/conformance code is separate because it selects concrete
   implementations and fixtures; it is not another reusable framework layer.**
   `txload` builds its own EVM module and transaction module
   (`tools/txload/driver.go:330`, `tools/txload/driver.go:343`,
   `tools/txload/driver.go:371`), while conformance suites invoke common
   subjects (`tests/conformance/adapter/adapter.go:25`).

## Source Catalog

| Concept | File | Configuration schema | Protocol file | Copy surface |
|---|---|---|---|---|
| Runtime kernel | `core/runtime/runtime.go`, `core/module/module.go`, `core/runtime/graph.go` | `core/config/config.go:74` | None | `core/lifecycle/lifecycle.go:28`, `core/errors/errors.go:135` |
| Chain-neutral contracts | `capability/*`, `core/capability/capability.go` | None | JSON model tags only | Package `doc.go` files and validation messages |
| Application workflows | `application/transaction`, `application/eventing`, `application/idempotency`, `application/projection`, `application/gameledger` | Constructor options | Stored JSON payloads only | Workflow logs/reports and package docs |
| Chain adapters | `adapters/evm`, `adapters/solana` | `adapters/evm/config.go:41`, `adapters/solana/config.go:36` | SDK call sites; JSON chain records | `adapters/*/doc.go` |
| Infrastructure | `infrastructure/persistence`, `infrastructure/signer`, `infrastructure/observability` | DSN/endpoint host inputs | Embedded SQL; remote-signing JSON; Prometheus text | `infrastructure/*/doc.go`, HTTP exposition |
| Host/tools/examples/tests | `cmd`, `examples`, `tools`, `tests/conformance` | CLI flags and local fixtures | Mock HTTP JSON-RPC; HTTP dashboard JSON | Example/tool output and suite names |

## Cross-Cutting Inputs

Configuration is merged in one order: code defaults, file, environment
substitution, and explicit overrides, followed by validation before start
(`core/config/config.go:1`, `core/config/load.go:50`,
`core/config/validate.go:12`). Secrets and URL query strings are redacted at
configuration/log boundaries (`core/config/redact.go:10`,
`core/logging/redact.go:25`). Health and error semantics are framework
contracts rather than per-layer copies (`core/health/health.go:26`,
`core/errors/policy.go:34`).

Every framework module follows one lifecycle: metadata registration in
`Register`, resource creation in `Start`, and deadline-aware resource release
in `Stop` (`core/module/module.go:19`). Runtime then schedules registered
tasks after modules and cancels them before modules stop
(`core/module/module.go:104`, `core/runtime/runtime.go:297`). SQL stores add a
start-time migration action (`infrastructure/persistence/sql/store.go:92`);
adapters verify network identity before serving (`adapters/evm/module.go:223`,
`adapters/solana/module.go:250`). Memory/tool state is intentionally
process-scoped and ends with the process (`infrastructure/persistence/memory/memory.go:22`,
`tools/txload/driver.go:173`).
