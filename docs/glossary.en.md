# Glossary

- [中文](/zh/glossary)

This table records concepts at the architecture layer, not directory names.
The linked source locations are the implementation evidence at commit
`c700e30d45106e905fd22f364fc4616e74454654` or newer; anchors are re-checked when a cited line moves.

| Term | English | Definition | Evidence |
|---|---|---|---|
| 运行时内核 | Runtime kernel | Owns lifecycle phases, dependency ordering, capability registration, background tasks, and aggregate health for one process. | `core/runtime/runtime.go:51`, `core/lifecycle/lifecycle.go:10` |
| 模块仓库 | Module registry | A per-module view over runtime registration state; it publishes one owner's capabilities, handlers, health checks, and tasks without global mutation. | `core/module/module.go:19`, `core/module/module.go:88` |
| 能力槽 | Capability slot | A stable chain-contract name validated for coverage before start; callers resolve the typed implementation by explicit interface assertion. | `core/capability/capability.go:10`, `core/runtime/runtime.go:341` |
| 链无关契约 | Chain-neutral contract | Stateless interfaces and models for network, account, asset, transaction, event, finality, and signing; they define semantics but never own connections, storage, or keys. | `capability/transaction/transaction.go:129`, `capability/event/repository.go:107` |
| 交易状态机 | Transaction state machine | A shared, frozen-terminal status model that preserves chain uncertainty through unknown and reorged states. | `capability/transaction/transaction.go:32`, `capability/transaction/statemachine.go:11` |
| 业务装配器 | Application assembler | Composes neutral contracts into transaction, event, idempotency, and projection workflows; state and side effects are injected. | `application/transaction/module.go:15`, `application/eventing/dispatcher.go:132` |
| 恢复工作器 | Recovery worker | Periodically leases stuck transaction records and applies a reconciled status only after optimistic state matching. | `application/transaction/worker.go:133`, `application/transaction/worker.go:237` |
| 事件分发器 | Event dispatcher | Polls configured sources, stores durable evidence and cursors, promotes finality, retries failures, and rolls back on reorg. | `application/eventing/dispatcher.go:132`, `application/eventing/dispatcher.go:240` |
| 链适配器 | Chain adapter | Converts one family's SDK and wire protocol into the neutral capabilities, while owning family-specific pools, identity checks, and chain records. | `adapters/evm/module.go:20`, `adapters/solana/module.go:20` |
| 端点池 | Endpoint pool | A family adapter's ordered RPC endpoints with failover, request timeout, and transport health state. | `adapters/evm/pool.go:18`, `adapters/solana/pool.go:18` |
| 观察接缝 | Observation seam | The adapter method through which the shared recovery flow asks for a recorded transaction's strongest current chain status. | `application/transaction/worker.go:29` (`Reconciler` contract), `adapters/evm/reconciler.go:16` (`StatusOf`) |
| 基础设施实现 | Infrastructure implementation | Replaceable persistence, signer, and observability implementations wired by the host; it may own durable stores or external clients. | `infrastructure/persistence/sql/store.go:41`, `infrastructure/signer/remote/remote.go:68` |
| SQL 仓库 | SQL repository | The PostgreSQL implementation of transaction and event repositories, sharing one connection pool and explicit migrations. | `infrastructure/persistence/sql/store.go:41`, `infrastructure/persistence/sql/events.go:18` |
| 进程内仓库 | In-memory repository | Process-local transaction and event state intended for tests/examples; it survives nothing and coordinates no other instances. | `infrastructure/persistence/memory/memory.go:22` (`Store`), `infrastructure/persistence/memory/events.go:19` (`EventStore`) |
| 远端签名客户端 | Remote signing client | An HTTP integration seam for an external custody or signing service, including description, sign, and health operations. | `infrastructure/signer/remote/remote.go:54`, `infrastructure/signer/remote/remote.go:89` |
| Prometheus 记录器 | Prometheus recorder | An in-process metrics collector and standard-library text exporter used by hosts and the load tool. | `infrastructure/observability/prometheus/recorder.go:13`, `infrastructure/observability/prometheus/exporter.go:17` |
| 分层配置 | Layered configuration | Plain configuration structs merged from code defaults, file, environment substitution, and explicit overrides, then validated before runtime start. | `core/config/config.go:74`, `core/config/load.go:50` |
| 可观测性契约 | Observability contract | Framework-side logging, metrics, health, errors, and clocks; deployment-specific export is owned by infrastructure or the host. | `core/metrics/metrics.go:19`, `core/health/health.go:26` |
| 合规套件 | Conformance suite | Shared behavioral suites that exercise adapter, event-repository, and transaction-repository implementations against common contracts. | `tests/conformance/adapter/adapter.go:25` (`Subject`), `tests/conformance/repository/repository.go:30` (`Run`) |
