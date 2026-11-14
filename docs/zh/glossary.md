# 术语表

- [English](/glossary.en)

本表收录抽象层概念，而不是目录名。链接源码为提交
`c700e30d45106e905fd22f364fc4616e74454654` 或更新的实现证据；被引用行漂移时重锚。

| 术语 | 英文 | 定义 | 证据 |
|---|---|---|---|
| 运行时内核 | Runtime kernel | 拥有单进程生命周期阶段、依赖排序、能力注册、后台任务与聚合健康。 | `core/runtime/runtime.go:51`、`core/lifecycle/lifecycle.go:10` |
| 模块仓库 | Module registry | 运行时注册状态的模块视图；发布本模块的能力、处理器、健康检查和任务，不做全局改动。 | `core/module/module.go:19`、`core/module/module.go:88` |
| 能力槽 | Capability slot | 启动前校验覆盖关系的稳定链契约名；调用方通过显式接口断言取得类型化实现。 | `core/capability/capability.go:10`、`core/runtime/runtime.go:341` |
| 链无关契约 | Chain-neutral contract | 网络、账户、资产、交易、事件、最终性和签名的无状态接口与模型；定义语义，不拥有连接、存储或密钥。 | `capability/transaction/transaction.go:129`、`capability/event/repository.go:107` |
| 交易状态机 | Transaction state machine | 共享且终态冻结的状态模型，用 unknown 与 reorged 显式保留链上不确定性。 | `capability/transaction/transaction.go:32`、`capability/transaction/statemachine.go:11` |
| 业务装配器 | Application assembler | 把中性契约组装成交易、事件、幂等和投影工作流；状态与副作用全部注入。 | `application/transaction/module.go:15`、`application/eventing/dispatcher.go:132` |
| 恢复工作器 | Recovery worker | 周期性租约滞留交易记录，并在乐观状态匹配后才写入对账结果。 | `application/transaction/worker.go:133`、`application/transaction/worker.go:237` |
| 事件分发器 | Event dispatcher | 轮询配置源，持久化证据与游标，推进最终性，重试失败，并在重组时回滚。 | `application/eventing/dispatcher.go:132`、`application/eventing/dispatcher.go:240` |
| 链适配器 | Chain adapter | 把某链族的 SDK 与线上协议转换成中性能力，同时拥有族内端点池、身份校验和链记录。 | `adapters/evm/module.go:20`、`adapters/solana/module.go:20` |
| 端点池 | Endpoint pool | 链族适配器持有的有序 RPC 端点，提供失败切换、请求超时和传输健康状态。 | `adapters/evm/pool.go:18`、`adapters/solana/pool.go:18` |
| 观察接缝 | Observation seam | 共享恢复流程向适配器询问已记录交易当前最强链态的专用接口。 | `application/transaction/worker.go:29`（`Reconciler` 契约）、`adapters/evm/reconciler.go:16`（`StatusOf`） |
| 基础设施实现 | Infrastructure implementation | 宿主装配的可替换持久化、签名和观测实现；可拥有持久存储或外部客户端。 | `infrastructure/persistence/sql/store.go:41`、`infrastructure/signer/remote/remote.go:68` |
| SQL 仓库 | SQL repository | 交易与事件仓库的 PostgreSQL 实现，共享连接池并使用显式迁移。 | `infrastructure/persistence/sql/store.go:41`、`infrastructure/persistence/sql/events.go:18` |
| 进程内仓库 | In-memory repository | 交易与事件的进程内状态，面向测试/示例；不持久化，也不跨实例协调。 | `infrastructure/persistence/memory/memory.go:22`（`Store`）、`infrastructure/persistence/memory/events.go:19`（`EventStore`） |
| 远端签名客户端 | Remote signing client | 外部托管或签名服务的 HTTP 集成接缝，覆盖描述、签名与健康操作。 | `infrastructure/signer/remote/remote.go:54`、`infrastructure/signer/remote/remote.go:89` |
| Prometheus 记录器 | Prometheus recorder | 进程内指标收集器和标准库文本导出器，供宿主与压测工具使用。 | `infrastructure/observability/prometheus/recorder.go:13`、`infrastructure/observability/prometheus/exporter.go:16` |
| 分层配置 | Layered configuration | 由代码默认值、文件、环境替换和显式覆盖合并的普通配置结构，启动前完成校验。 | `core/config/config.go:74`、`core/config/load.go:50` |
| 可观测性契约 | Observability contract | 框架侧日志、指标、健康、错误和时钟；部署侧导出由基础设施或宿主拥有。 | `core/metrics/metrics.go:19`、`core/health/health.go:26` |
| 合规套件 | Conformance suite | 针对适配器、事件仓库和交易仓库共同契约的共享行为套件。 | `tests/conformance/adapter/adapter.go:25`（`Subject`）、`tests/conformance/repository/repository.go:30`（`Run`） |
