# 源码分层分析

- [English](/analysis/source-layer-analysis.en)
- 术语表：[中文](/zh/glossary) | [English](/glossary.en)

本页证据固定于仓库提交
`974749317bc9835a58c770cd9d4da73da0d8cf3d`。以下六层描述运行时职责和依赖方向，
不是目录标签。

```text
宿主/工具 -> 运行时内核 -> 业务工作流 -> 链适配器 -> 链 API
              |                |
              +--> 中性契约 <--+
              +-> 基础设施 -> PostgreSQL / 外部服务
```

## 1. 运行时内核

**职责与实现。** `Runtime` 拥有进程生命周期、依赖排序、模块注册、能力查询、
后台任务取消和聚合健康（`core/runtime/runtime.go:51`）。状态机按
`New -> Configuring -> Initializing -> Starting -> Running -> Stopping ->
Stopped` 推进；启动期失败可进入 stopping，stopped 是终态
（`core/lifecycle/lifecycle.go:10`、`core/lifecycle/lifecycle.go:59`）。
依赖图解析必选与可选模块依赖，不使用反射容器
（`core/runtime/graph.go:12`、`core/module/module.go:19`）。

**状态。**运行时状态、启动顺序和任务取消是进程内存
（`core/runtime/runtime.go:51`）。模块注册同样是内存态，但被刻意限定为单
所有者、单运行时：能力、健康检查、任务和事件处理器都在一个 `Store`
（`core/module/module.go:88`）。不存在 Redis、数据库或文件型运行时协调。

**调用面。**组合根注册模块；运行时校验图、先启动依赖、所有模块后再启动任务、
停模块前取消任务，并按逆序停模块（`core/runtime/runtime.go:131`、
`core/runtime/runtime.go:297`、`core/runtime/runtime.go:382`）。消费方按名查询
能力并自行断言接口（`core/runtime/runtime.go:341`）。

**配置与文案。**共享选项是 logger、clock 和 shutdown timeout
（`core/runtime/runtime.go:64`）；进程配置来自第 7 层前的分层配置，模型在
`core/config/config.go:74`。用户可见状态名与生命周期模型放在一起
（`core/lifecycle/lifecycle.go:28`）；错误文案与分类错误模型放在一起
（`core/errors/errors.go:135`）。

## 2. 链无关契约

**职责与实现。**这些包定义“链对宿主意味着什么”：账户、资产、事件、最终性、
网络、NFT、签名和交易（`capability/account/account.go:115`、
`capability/asset/asset.go:65`、`capability/event/repository.go:107`、
`capability/network/network.go:17`、`capability/nft/nft.go:40`、
`capability/signer/signer.go:229`、
`capability/transaction/repository.go:135`）。稳定能力名独立声明在
`core/capability/capability.go:10`。交易模型包含终态冻结与不确定性语义
（`capability/transaction/transaction.go:32`、
`capability/transaction/statemachine.go:11`）。

**状态。**这一层是无状态接口、值模型、校验规则和状态迁移；没有连接、锁、
数据库或持久字段。仓库接口规定持久化语义，实际行由实现层拥有
（`capability/transaction/repository.go:135`、
`capability/event/repository.go:107`）。

**调用面。**应用与适配器代码导入这些契约；契约不导入应用、适配器或基础设施包。
运行时通过模块注册和显式断言接收它们
（`core/module/module.go:104`、`adapters/evm/module.go:104`）。

**配置、协议与文案。**本层没有专属配置 schema 或文件协议。载荷相关字段是中性
模型内的值，例如签名请求与签名结果
（`capability/signer/signer.go:170`、`capability/signer/signer.go:205`）；
稳定分类字符串与文案属于错误契约
（`core/errors/errors.go:9`、`core/errors/errors.go:135`）。

## 3. 业务工作流

**职责与实现。**该层把中性契约组装成可恢复流程。交易模块组装 Flow 与 Worker，
只有存在 transactor 时才发布 Flow（`application/transaction/module.go:15`、
`application/transaction/module.go:124`）。`Flow` 负责创建到提交
（`application/transaction/flow.go:26`）；`Worker` 在租约保护下扫描滞留状态
（`application/transaction/worker.go:133`、
`application/transaction/worker.go:211`）。事件层绑定模块处理器并轮询持久源
（`application/eventing/bind.go:15`、
`application/eventing/dispatcher.go:131`）。幂等服务检查请求哈希并返回既往
accepted 响应（`application/idempotency/service.go:40`）。投影是可执行的重放
边界，后端由宿主拥有（`application/projection/projection.go:18`、
`application/gameledger/contract.go:32`）。

**状态。**工作流持有注入的仓库、事件源、签名器、时钟、日志和指标；不选择存储
技术。`Worker` 与 `Dispatcher` 是进程内编排对象
（`application/transaction/worker.go:133`、
`application/eventing/dispatcher.go:131`）。持久状态由第 5 层仓库实现拥有。
示例投影账本使用进程 map，明确是参考契约而非宿主存储
（`application/gameledger/contract.go:121`、
`application/gameledger/contract.go:132`）。

**调用面。**

| 调用方 | 通道 | 被调方 | 证据 |
|---|---|---|---|
| 宿主 / 交易能力消费方 | 直接 Go 接口 | `transaction.Flow` | `application/transaction/module.go:135` |
| 运行时任务调度 | 进程内任务函数 | `transaction.Worker.Run` | `application/transaction/module.go:141` |
| `Worker` | `transaction.Repository` | 内存或 PostgreSQL 仓库 | `application/transaction/worker.go:138` |
| `Worker` | `Reconciler` / `StatusQuery` | EVM 或 Solana `StatusOf` | `application/transaction/control.go:13`、`adapters/evm/reconciler.go:14` |
| `Dispatcher` | `event.Source` | EVM 日志源或 Solana 签名源 | `application/eventing/dispatcher.go:145`、`adapters/evm/eventsource.go:65` |
| `Dispatcher` | `event.Repository` | 内存或 PostgreSQL 事件仓库 | `application/eventing/dispatcher.go:131` |
| 运行时 / 宿主 | 模块 `Registry` | 绑定的事件处理器 | `application/eventing/bind.go:15` |

**配置与生命周期。**重试、扫描间隔、任务超时、并发和指标是构造选项，不是全局
配置：Worker 设置在 `application/transaction/worker.go:52`；Dispatcher 重试设置在
`application/eventing/dispatcher.go:33`。运行时把长循环作为注册任务启动和停止
（`core/module/module.go:104`、`application/transaction/module.go:135`）。
日志与报告文案属于各工作流
（`application/transaction/worker.go:219`、
`application/eventing/dispatcher.go:221`）。

## 4. 链适配器

**职责与实现。**EVM 与 Solana 适配器隔离 SDK 类型、线上调用、身份校验、端点
失败切换、事件轮询、族内交易记录和状态映射。EVM 暴露模块、网络、账户、资产、
事件、Flow 和状态能力（`adapters/evm/module.go:20`、
`adapters/evm/network.go:32`）；Solana 独立实现同一组边界
（`adapters/solana/module.go:20`、`adapters/solana/network.go:34`）。
族内准备与链记录被刻意分开：EVM nonce/回执映射
（`adapters/evm/nonce.go:20`、`adapters/evm/tx.go:15`）对应 Solana
blockhash 窗口（`adapters/solana/blockhash.go:20`、
`adapters/solana/reconciler.go:19`）。

**状态。**端点池、当前身份、nonce 分配和 blockhash pin 是适配器内进程内存
（`adapters/evm/pool.go:18`、`adapters/evm/nonce.go:20`、
`adapters/solana/blockhash.go:20`）。持久交易历史交给仓库；链形态证据存入交易
的 `ChainData`（`capability/transaction/transaction.go:129`、
`adapters/solana/reconciler.go:19`）。

**调用面。**适配器方法通过 HTTP/WS JSON-RPC 调用厂商 SDK 客户端。EVM 把
go-ethereum 隔离在本地接口和端点池之后
（`adapters/evm/client.go:28`、`adapters/evm/pool.go:81`）；Solana 对
gagliardetto 做同样隔离（`adapters/solana/client.go:18`、
`adapters/solana/pool.go:22`）。应用代码只通过 `StatusQuery`、`Transactor` 和
`event.Source` 访问适配器；宿主经发布的适配器访问读能力。

**配置与文案。**适配器装配使用内存配置结构，不直接消费文件 schema：EVM 在
`adapters/evm/config.go:41`；Solana 在 `adapters/solana/config.go:36`。没有独立
协议 schema 文件；JSON tag 描述存储链记录和 RPC 载荷
（`adapters/evm/flow.go:31`、`adapters/solana/reconciler.go:19`）。包文档与行为
注释是适配器的文案面（`adapters/evm/doc.go:1`、`adapters/solana/doc.go:1`）。

## 5. 基础设施实现

**职责与实现。**该层提供可替换技术副作用。PostgreSQL 拥有持久交易与事件存储
以及版本化迁移（`infrastructure/persistence/sql/store.go:41`、
`infrastructure/persistence/sql/events.go:18`、
`infrastructure/persistence/sql/store.go:92`）。内存存储面向测试和示例，并明确
声明这一限制（`infrastructure/persistence/memory/memory.go:10`、
`infrastructure/persistence/memory/events.go:14`）。远端签名器是 HTTP 托管集成
（`infrastructure/signer/remote/remote.go:68`）；Prometheus recorder/exporter
与内存指标是观测实现
（`infrastructure/observability/prometheus/recorder.go:13`、
`infrastructure/observability/prometheus/exporter.go:17`）。本地签名包只是开发
骨架（`infrastructure/signer/local/doc.go:1`）。

**状态。**

| 实现 | 存储 | 证据 |
|---|---|---|
| SQL 交易仓库 | PostgreSQL 行，共享一个 `*sql.DB` 池 | `infrastructure/persistence/sql/store.go:41`、`infrastructure/persistence/sql/store.go:56` |
| SQL 事件仓库 | PostgreSQL 事件/游标行；靠数据库约束协调 | `infrastructure/persistence/sql/events.go:18`、`infrastructure/persistence/sql/events.go:38` |
| 内存交易仓库 | 单互斥锁保护的 map | `infrastructure/persistence/memory/memory.go:22` |
| 内存事件仓库 | 单互斥锁保护的事件与游标 map | `infrastructure/persistence/memory/events.go:19` |
| 远端签名器 | 无本地持久化；向托管服务发起 HTTP 请求/响应 | `infrastructure/signer/remote/remote.go:68`、`infrastructure/signer/remote/remote.go:130` |
| Prometheus 内存采集器 | 进程计数器、gauge、直方图快照 | `infrastructure/observability/prometheus/recorder.go:13` |
| 内存指标采集器 | 进程 map/gauge/直方图 | `infrastructure/observability/memory/memory.go:20` |

不存在 Redis、MySQL、Kafka、NATS 或文件存储实现。

**调用面。**业务工作流调用仓库接口；宿主组合根选择本层。SQL 通过 pgx 和
`database/sql`（`infrastructure/persistence/sql/store.go:18`、
`infrastructure/persistence/sql/store.go:56`）；远端签名用带 bearer token 的 HTTP
（`infrastructure/signer/remote/remote.go:18`、
`infrastructure/signer/remote/remote.go:130`）；Prometheus 通过注入 writer 输出
文本（`infrastructure/observability/prometheus/exporter.go:17`）。

**配置、协议与文案。**DSN 和服务端点是宿主输入，不属于
`core/config.Config`。PostgreSQL DDL 内嵌并记录版本
（`infrastructure/persistence/sql/store.go:78`、
`infrastructure/persistence/sql/store.go:92`）；远端签名请求/响应类型是本地
JSON 结构（`infrastructure/signer/remote/remote.go:80`）。运维限制写在包文档
（`infrastructure/persistence/memory/doc.go:1`、
`infrastructure/persistence/sql/doc.go:1`）。

## 6. 宿主、工具、示例与合规

**职责与实现。**示例演示显式装配，不发明框架行为
（`examples/minimal/main.go:27`、`examples/eventstream/main.go:42`、
`examples/signing/main.go:20`）。交易压测工具自带开发链、driver、HTTP 仪表盘和
流水线装配（`tools/txload/devchain/chain.go:83`、
`tools/txload/driver.go:173`、`tools/txload/http.go:27`）。合规套件让适配器和
仓库行为可校验，而不把测试绑定到单一实现
（`tests/conformance/adapter/adapter.go:25`、
`tests/conformance/repository/repository.go:20`）。生产 CLI 仍是骨架
（`cmd/chame/main.go:12`）。

**状态与调用面。**工具状态是进程内存、channel 和 HTTP 仪表盘
（`tools/txload/driver.go:193`、`tools/txload/http.go:27`）；模拟链拥有模拟区块
和交易，并以 HTTP JSON-RPC 暴露（`tools/txload/devchain/chain.go:83`、
`tools/txload/devchain/chain.go:241`）。示例直接调用适配器，不运行 `Runtime`。
合规套件通过契约调用被测对象。这里不发明宿主级持久化。

**配置与文案。**工具选项来自命令行 flag（`cmd/chame-txload/main.go:39`），
示例用常量和本地 fixture，合规 fixture 是结构体。公开使用文案在 `docs/`；
本分析和完整规格保持仓库文档（`docs/.vitepress/config.ts:9`）。

## 为什么是这六层

1. **运行时编排不能并入契约：它拥有启动顺序和终止策略，而契约需要在运行时
   停止后仍可调用。**注册元数据和图校验是 `Runtime.Start` 的独立输入
   （`core/module/module.go:44`、`core/runtime/graph.go:19`），生命周期迁移
   规则独立建模（`core/lifecycle/lifecycle.go:59`）。
2. **中性契约单独成层：应用与适配器只依赖语义，因此不同链实现和合规套件不必
   相互导入。**交易、事件、适配器和仓库套件都面向这些接缝
   （`tests/conformance/adapter/adapter.go:25`、
   `tests/conformance/repository/repository.go:20`）。
3. **工作流单独成层：可靠性策略跨链共享，而提交机制不共享。**Worker 只调用注入
   的 `Reconciler` 和仓库（`application/transaction/worker.go:133`），EVM 与
   Solana 分别映射自己的链证据（`adapters/evm/reconciler.go:14`、
   `adapters/solana/reconciler.go:19`）。
4. **适配器按链族拆开：传输与恢复记录细节不可归约。**EVM 校验 chain ID 并分配
   nonce（`adapters/evm/network.go:149`、`adapters/evm/nonce.go:20`）；Solana
   校验 genesis 并固定 blockhash 窗口
   (`adapters/solana/network.go:60`、`adapters/solana/blockhash.go:20`）。
5. **基础设施单独成层：持久化、托管和指标的部署生命周期与所有者不同于工作流
   策略。**SQL 迁移在进程启动时幂等执行
   （`infrastructure/persistence/sql/store.go:92`），内存存储声明不跨进程协调
   （`infrastructure/persistence/memory/memory.go:10`），远端客户端只中介外部
   签名服务（`infrastructure/signer/remote/remote.go:68`）。
6. **宿主/工具/合规代码单独成层：它们选择具体实现和 fixture，不是又一个可复用
   框架层。**`txload` 自建 EVM 模块与交易模块
   （`tools/txload/driver.go:330`、`tools/txload/driver.go:343`、
   `tools/txload/driver.go:371`），合规套件调用共同被测对象
   (`tests/conformance/adapter/adapter.go:25`）。

## 源码目录解说

| 概念 | 实现文件 | 配置 schema | 专属协议文件 | 文案挂载 |
|---|---|---|---|---|
| 运行时内核 | `core/runtime/runtime.go`、`core/module/module.go`、`core/runtime/graph.go` | `core/config/config.go:74` | 无 | `core/lifecycle/lifecycle.go:28`、`core/errors/errors.go:135` |
| 链无关契约 | `capability/*`、`core/capability/capability.go` | 无 | 仅 JSON 模型 tag | 各包 `doc.go` 与校验文案 |
| 业务工作流 | `application/transaction`、`application/eventing`、`application/idempotency`、`application/projection`、`application/gameledger` | 构造选项 | 仅存储 JSON 载荷 | 工作流日志/报告与包文档 |
| 链适配器 | `adapters/evm`、`adapters/solana` | `adapters/evm/config.go:41`、`adapters/solana/config.go:36` | SDK 调用点；JSON 链记录 | `adapters/*/doc.go` |
| 基础设施 | `infrastructure/persistence`、`infrastructure/signer`、`infrastructure/observability` | DSN/端点宿主输入 | 内嵌 SQL；远端签名 JSON；Prometheus 文本 | `infrastructure/*/doc.go`、HTTP exposition |
| 宿主/工具/示例/测试 | `cmd`、`examples`、`tools`、`tests/conformance` | CLI flag 与本地 fixture | 模拟 HTTP JSON-RPC；HTTP 仪表盘 JSON | 示例/工具输出与套件名 |

## 横切输入

配置按固定顺序合并：代码默认值、文件、环境替换、显式覆盖，随后启动前校验
（`core/config/config.go:1`、`core/config/load.go:50`、
`core/config/validate.go:12`）。密钥和 URL 查询串在配置/日志边界脱敏
（`core/config/redact.go:10`、`core/logging/redact.go:25`）。健康与错误语义是
框架契约，而不是每层复制的规则（`core/health/health.go:26`、
`core/errors/policy.go:34`）。

框架模块共用一个生命周期：`Register` 写元数据，`Start` 建资源，`Stop`
按 deadline 释放（`core/module/module.go:19`）。运行时随后在模块后调度任务、
停模块前取消任务（`core/module/module.go:104`、
`core/runtime/runtime.go:297`）。SQL 仓库增加启动迁移动作
（`infrastructure/persistence/sql/store.go:92`）；适配器先校验网络身份再服务
（`adapters/evm/module.go:223`、`adapters/solana/module.go:250`）。内存/工具状态
刻意限定进程内，随进程结束（`infrastructure/persistence/memory/memory.go:10`、
`tools/txload/driver.go:173`）。
