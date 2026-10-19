# Chame 链游底座能力分析 / Chame as a Web3 Game Backend Foundation

- 日期：2026-10-19
- 基线：`672e525`（`git pull --rebase origin main` 后，工作区干净）
- 范围：仓库代码、设计规格、ADR 与 TODO；外部产品资料按下文检索日期标注。
- 结论口径：代码证据表示仓库内有可运行实现，不代表已在真实 RPC、生产负载或主网环境验证。本文只分析，不把产品定位或设计意图当作运营能力。

## 判断摘要

Chame 已不止是“统一接口”的设计稿：EVM 与 Solana 均有读写交易路径、签名器契约、持久化恢复、链事件轮询和最终性/重组处理的实际代码。但它仍是一个**链能力和交易可靠性底层库**，不是开箱可运营的链游后端：没有游戏服务层、玩家/物品账本、生产 NFT 所有权索引服务、官方生产指标导出、RPC 主网验证矩阵；EVM nonce 多实例共用账户明确不支持。源码测试大量使用假 RPC，因此实现存在与生产有效性是两种证据。

## 1. 链游底座能力矩阵

每行是一个验收格，共 9 格。分类统计按格计：**有代码证据 8，纯文档能力/约束 1，未开始 0**。有代码证据格中仍可能包含明确限制；“代码证据”不表示生产级完成。

| 能力 | 现状与证据（file:line） | 判定 |
|---|---|---|
| 链抽象、多链适配及扩链成本 | 核心交易模型不依赖 SDK，EVM 与 Solana 各自实现适配器；接口面已实际跨两种不同执行/账户模型，但新增链仍需实现网络身份、账户/资产、签名、交易格式、回执、事件源和 conformance fixture，不能仅加 RPC URL。[`capability/transaction/transaction.go:126`](../../capability/transaction/transaction.go#L126)、[`adapters/evm/module.go:99`](../../adapters/evm/module.go#L99)、[`adapters/solana/module.go:130`](../../adapters/solana/module.go#L130)、[`tests/conformance/adapter/adapter.go:1`](../../tests/conformance/adapter/adapter.go#L1) | 有代码证据；扩链成本中高，抽象保留链差异是优点，适配器职责面较宽是成本。 |
| 交易生命周期 | EVM 与 Solana 都接入 Prepare/Authorize/Dispatch/Settle，应用层逐阶段持久化，unknown 可恢复；EVM nonce 协调器是进程内状态，不支持多实例共用同一账户并发写入。另有幂等并发验收当前失败：`TestBeginConcurrentReplaysConvergeOnOneOperation` 复跑 10 次中 5 次失败，需先修验收/实现再依赖该语义。[`application/transaction/flow.go:15`](../../application/transaction/flow.go#L15)、[`adapters/evm/flow.go:178`](../../adapters/evm/flow.go#L178)、[`adapters/solana/flow.go:54`](../../adapters/solana/flow.go#L54)、[`adapters/evm/nonce.go:68`](../../adapters/evm/nonce.go#L68)、[`application/idempotency/service.go:126`](../../application/idempotency/service.go#L126)、[`application/idempotency/service_test.go:216`](../../application/idempotency/service_test.go#L216) | 有代码证据但存在验收失败；部署限制明确。失败重试以语义错误分类和恢复扫描为主，不等于任意业务交易可安全自动重放。 |
| 资产与确权 | 代码提供原生资产、EVM ERC-20 与 Solana SPL Token 的 metadata/balance 查询；SPL holder 明确是 token account，框架不由钱包推导 ATA。新增独立 NFT 查询 capability，未提供转移历史索引或游戏物品账本；资产观察不构成游戏侧防双花账本。[`capability/asset/asset.go:65`](../../capability/asset/asset.go#L65)、[`adapters/evm/asset.go:77`](../../adapters/evm/asset.go#L77)、[`adapters/solana/asset.go:74`](../../adapters/solana/asset.go#L74)、[`capability/nft/nft.go:1`](../../capability/nft/nft.go#L1) | 有代码证据（查询面）；历史索引和游戏账本仍缺。 |
| NFT 确权（独立检查格） | 新增标准保留差异的只读查询契约：EVM `ownerOf` / `balanceOf` 返回各自 owner / raw quantity；Solana 通过 Metaplex metadata PDA 识别 NFT，并查询钱包 token accounts。共用第 2 条规范化身份；latest/配置 commitment 的查询仍只是链观察，游戏入账继续经过 finality/reorg 规则。测试仅 fake RPC，未验证真实节点。[`capability/nft/nft.go:1`](../../capability/nft/nft.go#L1)、[`adapters/evm/asset.go:20`](../../adapters/evm/asset.go#L20)、[`adapters/solana/asset.go:20`](../../adapters/solana/asset.go#L20) | 有代码证据（最小只读查询）；未提供完整 Solana 索引/转移历史。 |
| 链与游戏逻辑一致性 | 交易 unknown/reorged 状态、恢复 Worker、事件 finality gate 和 reorg invalidation 可支撑最终性后发奖等模式；但链数据库与游戏数据库没有跨系统原子事务，游戏业务补偿、先玩后链的回滚/争议流程须由宿主游戏定义。[`capability/transaction/transaction.go:59`](../../capability/transaction/transaction.go#L59)、[`application/transaction/worker.go:22`](../../application/transaction/worker.go#L22)、[`application/eventing/dispatcher.go:221`](../../application/eventing/dispatcher.go#L221)、[`application/transaction/flow.go:32`](../../application/transaction/flow.go#L32) | 有代码证据；框架给出可靠状态钩子，不替游戏决定经济语义。 |
| 索引与事件 | 两链提供轮询事件源与 `Poll/Head/Observe`，dispatcher 至少一次投递并做 finality/reorg 门控；raw 事件先落仓再推进源 cursor。新增独立 `ReplayEvents` 与 projection backend 边界，可从保存 cursor 重建派生读模型，仍不是全链历史回填/复杂查询服务，也没有真实外部 indexer 接入。[`capability/event/repository.go:107`](../../capability/event/repository.go#L107)、[`application/eventing/dispatcher.go:294`](../../application/eventing/dispatcher.go#L294)、[`application/projection/projection.go:20`](../../application/projection/projection.go#L20)、[`infrastructure/persistence/sql/events.go:75`](../../infrastructure/persistence/sql/events.go#L75) | 有代码证据（边界与 fake）；托管/真实索引器仍未接入。 |
| 安全 | 有精确网络/账户 allow-list、签名请求审计摘要、远程 signer adapter；本地开发 signer 声明 Unsafe 并限制环境。生产密钥托管、HSM/KMS/多签策略、远程服务供应链和授权运行制度是宿主边界，不由仓库替代。[`capability/signer/allowlist.go:41`](../../capability/signer/allowlist.go#L41)、[`capability/signer/audit.go:13`](../../capability/signer/audit.go#L13)、[`infrastructure/signer/remote/remote.go:1`](../../infrastructure/signer/remote/remote.go#L1)、[`capability/signer/signer.go:74`](../../capability/signer/signer.go#L74) | 有代码证据；签名权限边界有实现，密钥基础设施与部署安全需外接。 |
| 运维与可观测 | 错误分类、健康检查、redacting slog、指标契约/进程内 collector 存在；Prometheus 官方导出器被 ADR 延后。代码能估算 EVM gas limit 并查询费用建议，但没有费用预算/链上实际成本归集与运营告警产品。[`core/metrics/metrics.go:1`](../../core/metrics/metrics.go#L1)、[`adapters/evm/prepare.go:278`](../../adapters/evm/prepare.go#L278)、[`docs/decisions/0007-observability.md:13`](../decisions/0007-observability.md#L13)、[`infrastructure/observability/memory/memory.go:1`](../../infrastructure/observability/memory/memory.go#L1) | 有代码证据；接入方需提供 exporter、dashboard、告警与实际费用核算。 |
| 合规与资金风险 | 规格说明充值/提取属于项目自行决定的游戏业务；仓库未见 KYC/制裁筛查、地区限制、资金限额/审批、托管分类或合规审计流程实现。这里只标能力边界，不构成资金操作建议。[`docs/design/chame-spec.md:22`](../design/chame-spec.md#L22)、[`docs/design/chame-spec.md:40`](../design/chame-spec.md#L40) | 仅文档/未实现；如游戏运营涉及受监管资金流，不能把此框架当作相应控制。 |

### 格子计数核对

- 有代码证据：8 格。例：`adapters/evm/flow.go:178`、`application/eventing/dispatcher.go:221`；另有 `capability/signer/allowlist.go:65`、`adapters/solana/asset.go:74`、`capability/nft/nft.go:1`、`application/projection/projection.go:1`。
- 仅文档/边界说明：1 格。例：`docs/design/chame-spec.md:22`、`docs/design/chame-spec.md:40`。
- 未开始：0 格。第 5 条已增加可重建投影适配边界；真实 Goldsky/The Graph/自建 indexer 接入、完整历史回填与真实节点验证仍待后续实施，不计为本矩阵独立验收格。

## 2. 能 / 不能直接当底座的判断

### 可以直接支撑（4 条）

1. **Go 游戏服务的模块化链连接与只读资产查询。** EVM/Solana 有各自网络适配、账户、native/token 查询并以模块能力发布；SDK 类型保持在 adapter 内。[`adapters/evm/module.go:99`](../../adapters/evm/module.go#L99)、[`adapters/solana/module.go:130`](../../adapters/solana/module.go#L130)、[`capability/asset/asset.go:65`](../../capability/asset/asset.go#L65)
2. **具持久化幂等与恢复的服务器发交易流程。** Flow 分阶段落库，恢复 Worker 租约认领并对账；PostgreSQL store 承担跨实例事务记录协调。[`application/transaction/flow.go:24`](../../application/transaction/flow.go#L24)、[`application/transaction/worker.go:22`](../../application/transaction/worker.go#L22)、[`infrastructure/persistence/sql/store.go:37`](../../infrastructure/persistence/sql/store.go#L37)
3. **以链事件驱动游戏服务端状态更新。** 两条链均有轮询 source、游标、事件持久化和最终性/重组路径，至少一次语义要求消费端幂等。[`adapters/evm/eventsource.go:62`](../../adapters/evm/eventsource.go#L62)、[`adapters/solana/eventsource.go:43`](../../adapters/solana/eventsource.go#L43)、[`application/eventing/dispatcher.go:131`](../../application/eventing/dispatcher.go#L131)
4. **将签名权限隔离在可替换 signer 之后。** 交易调用声明目标网络与账户，签名器自行实施 allow-list；远程签名服务可作为独立进程接入。[`capability/signer/allowlist.go:63`](../../capability/signer/allowlist.go#L63)、[`infrastructure/signer/remote/remote.go:17`](../../infrastructure/signer/remote/remote.go#L17)

### 需要补齐（5 条）

1. **NFT 转移索引与游戏资产账本。** 第 4 条已补 NFT 当前所有权/数量查询，第 5 条已补通用 raw-event 投影重建契约；仍无全历史 token-id 转移数据/回填、生产索引服务或生产游戏账本，宿主需对最终性事件幂等入账。[`capability/nft/nft.go:1`](../../capability/nft/nft.go#L1)、[`capability/asset/asset.go:65`](../../capability/asset/asset.go#L65)、[`application/projection/projection.go:20`](../../application/projection/projection.go#L20)
2. **规模化索引。** 当前基于 RPC 轮询的事件消费不是历史索引、复杂查询、回填/重建服务；生产游戏通常需要独立索引投影及读模型。[`capability/event/source.go:21`](../../capability/event/source.go#L21)、[`adapters/evm/eventsource.go:84`](../../adapters/evm/eventsource.go#L84)
3. **生产可观测链路。** 接官方 exporter、交易/事件 dashboard、费用与延迟指标、队列积压和失败告警；目前 exporter 延后，内存 collector 不做生产持久导出。[`docs/decisions/0007-observability.md:13`](../decisions/0007-observability.md#L13)
4. **真实节点和故障条件验证。** README 与 TODO 表明适配器验收以 fake RPC 为主；需纳入本地节点/测试网矩阵，覆盖限流、RPC 不一致、重组、服务端重启、交易重复提交与费用尖峰。[`README.zh.md:38`](../../README.zh.md#L38)、[`todo.md:1`](../../todo.md#L1)
5. **游戏服务边界。** 玩家身份映射、背包/掉落/交易市场、对账补偿、玩家 API 与反作弊均属于游戏业务层，仓库定位明确留给项目决定。[`docs/design/chame-spec.md:22`](../design/chame-spec.md#L22)

### 缺失且危险（4 条）

1. **把余额查询或链事件当游戏资产账本。** `AssetProvider` 只给链上余额，且 SPL token holder 是 token account；它不锁定游戏内兑换、转赠或奖励的并发业务约束，直接据此增发/消费会留双花窗口。[`capability/asset/asset.go:65`](../../capability/asset/asset.go#L65)
2. **把 EVM 单实例 nonce coordinator 当多副本安全。** 协调状态在进程内存，多进程共享签名账户明确不受支持；部署多个写实例会有 nonce 冲突/外部 writer 干扰风险。[`adapters/evm/nonce.go:68`](../../adapters/evm/nonce.go#L68)、[`docs/decisions/0004-evm-nonce-scope.md:11`](../decisions/0004-evm-nonce-scope.md#L11)
3. **幂等并发胜者语义曾缺少验收。** 历史修复 `d1f1260` 已修好测试夹具的线程安全与唯一 ID，故此前“`make ci` 测试失败、`-count=10` 重现 5 次”的结论已过时，不代表当前状态。产品侧曾仍依赖 ID 对比猜测胜者；本次改为仓储原子返回 inserted / same-key-existing，并补齐固定 ID、随机 ID 并发验收；通过结果见本清单提交记录。[`application/idempotency/service.go`](../../application/idempotency/service.go)、[`capability/transaction/repository.go`](../../capability/transaction/repository.go)、[`infrastructure/persistence/memory/memory.go`](../../infrastructure/persistence/memory/memory.go)、[`infrastructure/persistence/sql/store.go`](../../infrastructure/persistence/sql/store.go)
4. **把业务回滚视为链数据库事务回滚。** `Flow` 明示数据库提交与链提交不原子；发奖、道具发放等需等待业务采用的 finality，再设计补偿与争议处理。[`application/transaction/flow.go:32`](../../application/transaction/flow.go#L32)、[`docs/decisions/0006-finality-policy.md:13`](../decisions/0006-finality-policy.md#L13)
5. **把本地开发 signer、远程签名接口或链 SDK 接入等同于完整密钥运营。** 仓库定义权限及调用边界，但不提供生产 HSM/KMS 策略证明、密钥轮换演练或资金操作审批闭环；密钥不得落盘在游戏业务数据、日志或分析材料中。[`capability/signer/signer.go:86`](../../capability/signer/signer.go#L86)、[`infrastructure/signer/remote/remote.go:12`](../../infrastructure/signer/remote/remote.go#L12)

## 3. 对标：相邻产品/中间件（外部资料）

检索日期：2026-10-19。对标的是可组合能力，不假定对方产品质量或覆盖所有链。Chame 为可嵌入 Go 库，三者分别偏游戏平台栈、交易运营、链数据索引，职责并不完全重合。

| 对象 | 定位与资料显示的能力 | 与 Chame 对照 | 来源 |
|---|---|---|---|
| [Sequence](https://docs.sequence.xyz/sdk/unreal/authentication) | 面向游戏的 Web3 平台；文档列有 Unity/Unreal/TS/Go SDK、Indexing、Metadata、Wallet 等 API。 | 游戏接入面、账户/钱包、索引和元数据产品化程度更完整；Chame 更偏 Go 服务内嵌链适配、可自托管的事务协调。Sequence 文档未证明其能力可替换 Chame 的 Go runtime/仓储语义。 | [Sequence 文档首页](https://docs.sequence.xyz/sdk/unreal/authentication)、[游戏支付概览](https://docs.sequence.xyz/solutions/payments/overview/)（检索 2026-10-19） |
| [thirdweb Engine](https://portal.thirdweb.com/engine/v2/get-started) | EVM 后端交易服务：异步队列、backend wallets、交易状态、卡住交易同 nonce 重提、webhook 与访问 token。 | Chame 具跨 EVM/Solana 的应用层持久化/未知态恢复抽象，Engine 展示了更直接的 EVM 托管写入、队列和运维面；Chame 没有等价的 HTTP 管理面、托管钱包控制台或交易 timeline。 | [Engine v2 快速开始](https://portal.thirdweb.com/engine/v2/get-started)、[Transactions 能力页](https://portal.thirdweb.com/engine/v3)（检索 2026-10-19） |
| [Goldsky](https://goldsky.com/) | 区块链索引与数据管道：Subgraphs、实时索引、历史数据、streams/ETL、RPC failover。 | Chame 内置事件 source 负责可靠轮询、游标和消费门控，不提供托管历史数据/GraphQL 查询或多链数据管线；二者可并用，索引器供游戏读模型，Chame 管理交易状态与模块运行。 | [Goldsky 产品概览](https://goldsky.com/)、[支持链目录](https://goldsky.com/chains)（检索 2026-10-19） |

外部资料是产品文档/官网描述，表格中的“更完整/缺少”仅对照仓库可见代码与这些页面当前公开能力，不是独立性能或安全评测。

## 4. 补齐清单与推荐推进顺序

依赖排序以“先定账本语义，再可靠读链，再扩大写入，再运营化”为准。以下每条验收都可被反例推翻；不含时间承诺。

| 顺序 | 补齐项 | 最小可证伪验收形态 |
|---:|---|---|
| 1 | **已完成（本次提交）**：仓储 `Create` 返回原子 `inserted` / `same-key-existing` 结果，Service 据此判定胜者。 | 固定 ID 与随机 ID 两种构造下，16 个并发同键同哈希 Begin 必须恰有 1 个 `CreateInserted`/Created，其余 `CreateSameKeyExisting`/Existing，且全数返回同一持久化记录；异哈希仍必须全部冲突或仅一个哈希成功。 |
| 2 | **已完成（设计定稿 + 可验证契约）**：[`docs/design/game-asset-ownership-and-ledger-boundary.md`](../design/game-asset-ownership-and-ledger-boundary.md) 定义规范资产身份、链事实/游戏账本权威边界、finality 前 pending 不可花、事件幂等与 reorg 补偿，并明确不做托管钱包/生产密钥运营；英文版同步。契约代码 [`application/gameledger/contract.go`](../../application/gameledger/contract.go)，沿用 `application/idempotency` 与 `CreateInserted` 原子胜者语义。 | 已有故障断言：`TestDuplicateTransferEventIsAppliedExactlyOnce` 重投只建一条流水；`TestPendingEntryIsVisibleButNotSpendable` pending 可查不可花、确认后可用；`TestReorgCompensatesOnce` reorg 冲销一次；`TestAssetIdentityNormalizationPreservesStandardDifferences` 规范化身份但 ERC-721/ERC-1155/SPL 不合并。**宿主持久化账本、真实充值/铸造/转移端到端回放及乱序故障注入仍待宿主实现/验证**；本契约不是该证据。 |
| 3 | **已完成（单实例决策 + 运行时防护 + 部署文档）**：选择 EVM signer 单实例约束；本机重复 signer 实例经 OS 文件锁明确拒绝，跨主机由宿主编排保证每账户单 writer；Solana 过期恢复保留 unknown/uncertain 保护。见 [`docs/deployment-write-model.md`](../deployment-write-model.md)。 | `TestEVMConcurrentWriterInstancesRejectSharedAccount` 断言第二实例明确失败；`TestStatusOfReportsUnknownAfterExpiry` 断言过期未观测状态为 unknown；`TestExpiryUncertainWithoutHistorySearch` 断言未做全历史搜索时为 uncertain。Send 后断连恢复仍由既有交易状态/幂等恢复路径覆盖，不代表可任意重放。 |
| 4 | **已完成（最小只读查询）**：目标为 EVM ERC-721/ERC-1155 + Solana Metaplex NFT；查询复用第 2 条 `AssetIdentity`，类型保留标准差异，结果显式标为未满足游戏 finality，入账继续走现有 finality/reorg 规则。fake RPC 验收，不代表真实节点验证。 | `TestNFT721SameContractDifferentTokenIDsAreDistinct` 保留同合约 tokenId 区分；`TestNFT1155QuantityIsPreserved` 保留 raw quantity；`TestSolanaMetaplexNFTIsDistinctFromFungibleSPL` 检验 canonical metadata PDA 分类与标准错配拒绝；`TestNFTQueryRPCErrorAndUnfinalizedObservation`、`TestSolanaNFTQueryPropagatesRPCFailure` 检验 RPC 错误传播及未最终性标注。完整转移历史数据与生产回填仍未提供；第 5 条已提供重建适配边界。 |
| 5 | **已完成（可重建读模型边界 + fake 后端契约）**：[`application/projection/projection.go`](../../application/projection/projection.go) 独立于 RPC dispatcher；事件仓储提供 `ReplayEvents`，raw payload 留存，派生 backend 可替换。外部 Goldsky/The Graph/自建 indexer 仅为可选接入方案，未绑定服务。 | `TestRebuildMatchesDeletedProjection` 从保存 cursor 删除派生读模型后重建快照一致；`TestRebuildPreservesRawEvidence` 断言 raw ID/payload 保留；`TestIndexerBackendSwapPreservesProjectionSemantics` 比较 fake A/B 投影。真实索引服务接入、全历史回填及生产级断线/reorg 压测仍未完成。 |
| 6 | **部分完成（本地传输层故障矩阵；真实节点/链语义仍待验证）**：新增 `tests/faultmatrix` 本地 HTTP JSON-RPC 故障 harness，覆盖 EVM 与 Solana 形状的余额读取、fake-signer 提交 RPC、事件轮询 RPC，以及超时、429、5xx、连接中断、慢响应；注入 503 后再次请求验证续跑。 | **实测边界**：36 场景/故障组合全通过；恢复用例 503→重试成功。该 harness 的响应是合成的，写入只发 JSON-RPC 请求且不签名/不广播交易，不能证明适配器状态机、节点重启、EVM reorg 或 Solana blockhash 过期语义。Foundry 镜像获取受阻，真实节点矩阵全部待节点。容量仅测本机 `httptest` HTTP transport，不作为链节点吞吐门槛。完整矩阵与实测格见下文。 |
| 7 | **已完成（Prometheus text 导出 + 信号/成本接口）**：[`docs/production-observability.md`](../production-observability.md)；标准库 `/metrics` handler、低基数指标 recorder、日志幂等键关联、按内部键查询费用。 | 接入导出器后，单笔交易从业务幂等键（仅内部关联，不作为高基数标签）可在日志中追踪；错误率、unknown 堆积、事件延迟、gas 使用/费用产生告警；测试标签不会出现玩家/交易 ID。已验证 text scrape、key 仅进入日志、费用按键聚合；外部告警与生产持久化成本存储由宿主接入。 |
| 8 | **已完成（通用原语，默认关闭）**：现有 `application/transaction.Flow` 接入宿主可配审批、按网络/操作类型的单笔及 UTC 日限额、append/query 审计；见 [`docs/internal-controls.md`](../internal-controls.md)。地区合规结论由宿主自评。 | `TestControlApprovalDenied`、`TestControlLimitDenied`、`TestControlAuditQueryIncludesRejectedAttempt`、`TestControlDisabledPreservesFlow` 覆盖；规则由宿主配置，框架不做真实资金操作或托管。**此项不是法律意见。** |

**推荐次序（已代决）**：1 → 2 → 3 → 4 → 5 → 6 → 7 → 8。先解除幂等并发阻断，再确定账本/finality 和签名写入并发边界，才扩确权和读模型；真实节点验证在索引边界落定后做；运营可观测和依地区适用的资金控制最后闭环。第 4 条 fake RPC 证据不替代第 6 条真实节点/测试网验证；第 5 条 fake backend 证据不代表真实索引服务已接入。若首个目标游戏明确不含 NFT 或受监管资金流，相应项目可不进入该产品部署，但框架能力缺口仍在。

### 第 6 条执行记录（2026-10-19）

**节点取得尝试**：在仓库初始干净、HEAD 与 `origin/main` 同为 `97328b3a94c4c1d614f843d6c2341e1a0918df2e` 后，仅尝试一次：`timeout 45s docker pull ghcr.io/foundry-rs/foundry:latest`。Docker 输出（本次命令输出原文）：

```text
latest: Pulling from foundry-rs/foundry
de47083ed7d7: Pulling fs layer
191bdd6639b5: Pulling fs layer
5ae30d21561f: Pulling fs layer
2aff5e9d87ca: Pulling fs layer
2bb8f16d567c: Download complete
191bdd6639b5: Download complete
6001fa6e0ea6: Download complete
de47083ed7d7: Pull complete
2aff5e9d87ca: Download complete
5ae30d21561f: Download complete
```

45 秒窗口内没有得到可用镜像；随后 `docker image inspect ghcr.io/foundry-rs/foundry:latest --format '{{.Id}}'` 返回 `Error response from daemon: No such image: ghcr.io/foundry-rs/foundry:latest`。结论：**环境受阻·待节点**。没有访问公网测试网，也未使用真实密钥。

**本地传输层矩阵**：命令 `go test ./tests/faultmatrix -v`。全部 36 个组合的“实测”均为 `httptest` 合成 JSON-RPC 响应；慢响应为 20 ms 且在 30 ms context deadline 内。超时响应延迟 60 ms；连接中断由关闭 hijacked HTTP 连接注入。预期中的“请求成功/传输错误”均由 harness 实测；它不调用 EVM/Solana Adapter 的账户/交易/eventSource 入口，因此“适配器/节点实测”均未测。

| 固定场景 | 故障 | 预期 | 实测 |
|---|---|---|---|
| EVM 余额读取 `eth_getBalance` | 无故障 | RPC 请求成功 | harness 成功；适配器/节点未测·环境受阻，待节点 |
| EVM 余额读取 `eth_getBalance` | 超时 | 请求截止并报传输错误，不伪报余额 | harness 失败如预期；适配器恢复策略未测·待节点 |
| EVM 余额读取 `eth_getBalance` | HTTP 429 | 报限流错误，不伪报余额 | harness 收到 429；适配器分类已有单测 `TestClassifyMapsTaxonomy` 覆盖；节点未测·待节点 |
| EVM 余额读取 `eth_getBalance` | HTTP 5xx | 报服务/网络错误，不伪报余额 | harness 收到 503；适配器分类已有单测覆盖；节点未测·待节点 |
| EVM 余额读取 `eth_getBalance` | 连接中断 | 报连接错误，重连后可续跑 | harness 收到连接错误；重连未测·待节点 |
| EVM 余额读取 `eth_getBalance` | 慢响应 | deadline 内成功；超 deadline 则超时 | harness 20 ms 内成功；边界压力未测·待节点 |
| EVM fake-signer 提交 `eth_sendRawTransaction` | 无故障 | 固定 RPC 请求成功 | harness 成功；未签名/未广播·适配器/节点未测 |
| EVM fake-signer 提交 `eth_sendRawTransaction` | 超时 | 保留提交结果未知，不推断成功或失败 | harness 传输错误；状态机语义未测·待节点 |
| EVM fake-signer 提交 `eth_sendRawTransaction` | HTTP 429 | 明确限流，不确认交易成功 | harness 收到 429；分类单测覆盖；节点未测·待节点 |
| EVM fake-signer 提交 `eth_sendRawTransaction` | HTTP 5xx | 保留不确定，不确认交易成功 | harness 收到 503；节点/恢复未测·待节点 |
| EVM fake-signer 提交 `eth_sendRawTransaction` | 连接中断 | 结果按未知处理，恢复后查状态而非盲目重放 | harness 连接错误；交易状态恢复未测·待节点 |
| EVM fake-signer 提交 `eth_sendRawTransaction` | 慢响应 | deadline 内成功；超时结果保持未知 | harness 20 ms 内成功；交易确认语义未测·待节点 |
| EVM 事件轮询 `eth_getLogs` | 无故障 | RPC 请求成功 | harness 成功；游标/日志解码未测·待节点 |
| EVM 事件轮询 `eth_getLogs` | 超时 | 不推进游标，重试可续跑 | harness 传输错误；适配器游标语义未测·待节点 |
| EVM 事件轮询 `eth_getLogs` | HTTP 429 | 限流错误，不推进游标 | harness 收到 429；节点未测·待节点 |
| EVM 事件轮询 `eth_getLogs` | HTTP 5xx | 网络错误，不推进游标 | harness 收到 503；节点未测·待节点 |
| EVM 事件轮询 `eth_getLogs` | 连接中断 | 错误后重连续跑，避免静默跳游标 | harness 连接错误；重连未测·待节点 |
| EVM 事件轮询 `eth_getLogs` | 慢响应 | deadline 内成功；超时则不推进游标 | harness 20 ms 内成功；节点未测·待节点 |
| Solana 余额读取 `getBalance` | 无故障 | RPC 请求成功 | harness 成功；适配器/节点未测·环境受阻，待节点 |
| Solana 余额读取 `getBalance` | 超时 | 请求截止并报传输错误，不伪报余额 | harness 失败如预期；节点未测·待节点 |
| Solana 余额读取 `getBalance` | HTTP 429 | 报限流错误，不伪报余额 | harness 收到 429；分类已有 `TestClassifyHTTPStatus` 覆盖；节点未测·待节点 |
| Solana 余额读取 `getBalance` | HTTP 5xx | 报服务/网络错误，不伪报余额 | harness 收到 503；分类已有单测覆盖；节点未测·待节点 |
| Solana 余额读取 `getBalance` | 连接中断 | 报连接错误，恢复后重试 | harness 收到连接错误；重连未测·待节点 |
| Solana 余额读取 `getBalance` | 慢响应 | deadline 内成功；超 deadline 则超时 | harness 20 ms 内成功；节点未测·待节点 |
| Solana fake-signer 提交 `sendTransaction` | 无故障 | RPC 请求成功 | harness 成功；未签名/未广播·适配器/节点未测 |
| Solana fake-signer 提交 `sendTransaction` | 超时 | 保留提交结果未知，不推断成功或失败 | harness 传输错误；状态语义未测·待节点 |
| Solana fake-signer 提交 `sendTransaction` | HTTP 429 | 明确限流，不确认交易成功 | harness 收到 429；分类单测覆盖；节点未测·待节点 |
| Solana fake-signer 提交 `sendTransaction` | HTTP 5xx | 保留不确定，不确认交易成功 | harness 收到 503；节点/恢复未测·待节点 |
| Solana fake-signer 提交 `sendTransaction` | 连接中断 | 结果未知，恢复后查签名状态而非盲目重放 | harness 连接错误；状态恢复未测·待节点 |
| Solana fake-signer 提交 `sendTransaction` | 慢响应 | deadline 内成功；超时结果保持未知 | harness 20 ms 内成功；交易确认语义未测·待节点 |
| Solana 事件轮询 `getSignaturesForAddress` | 无故障 | RPC 请求成功 | harness 成功；游标/签名解码未测·待节点 |
| Solana 事件轮询 `getSignaturesForAddress` | 超时 | 不推进游标，重试可续跑 | harness 传输错误；游标语义未测·待节点 |
| Solana 事件轮询 `getSignaturesForAddress` | HTTP 429 | 限流错误，不推进游标 | harness 收到 429；节点未测·待节点 |
| Solana 事件轮询 `getSignaturesForAddress` | HTTP 5xx | 网络错误，不推进游标 | harness 收到 503；节点未测·待节点 |
| Solana 事件轮询 `getSignaturesForAddress` | 连接中断 | 错误后重连续跑，避免静默跳游标 | harness 连接错误；重连未测·待节点 |
| Solana 事件轮询 `getSignaturesForAddress` | 慢响应 | deadline 内成功；超时则不推进游标 | harness 20 ms 内成功；节点未测·待节点 |

| 故障恢复项 | 预期 | 实测 |
|---|---|---|
| 端点中断/重启后恢复 | 失败请求后重连，重新读取同一固定场景 | `TestRestartRecovery`：首请求注入 503，第二次请求成功，`recovery calls=2`；仅本地合成 HTTP 服务，不是节点进程重启 |
| 节点身份不符 | 启动校验拒绝错误身份，不能当作暂时 outage | 未测·真实 EVM chain ID / Solana genesis hash 节点尚未取得 |
| EVM reorg | 重组后事件/交易状态不被误判为最终 | 未测·需要可控 EVM 链和真实区块/日志状态 |
| Solana blockhash 过期 | 过期后保留不确定状态并正确恢复 | 未测·需要真实 validator、区块推进与过期交易 |

**容量实测**：命令 `go test ./tests/faultmatrix -run '^$' -bench BenchmarkConcurrentQueries -benchtime=2s -count=1` 输出 `BenchmarkConcurrentQueries-14 22614 109535 ns/op 32113 B/op 112 allocs/op`，即本机 HTTP transport 基线约 `1/0.000109535 = 9,129` 请求/秒（并发 `RunParallel`，i9-10880H）。此数只代表本地合成 HTTP JSON-RPC handler，不含 go-ethereum/Solana SDK 解码、适配器重试/池化、节点、磁盘或生产网络；EVM/Solana 节点吞吐、端到端轮询吞吐及交易吞吐均**未测**。

**§4-6 状态同步**：由原“完成真实 RPC/测试网故障验证与容量门槛”更新为**部分完成**。补齐本地 transport 故障 harness、36 格结果矩阵、503 后恢复用例与本机 transport 容量参考；真实节点/测试网缺席，故真实适配器矩阵、节点身份校验、节点进程重启、EVM reorg、Solana blockhash 过期和链节点容量门槛仍未完成。恢复这些验收需要可重启 Anvil/geth 与 solana-test-validator，或风险可控的专用测试网端点；不需要真实资金密钥。

## 5. 英文结论 / English conclusion

Chame has substantial code behind its EVM and Solana adapters: read capabilities, transaction preparation/signing/submission, durable idempotency and recovery, polling event sources, finality handling, a rebuildable projection boundary with fake backend adapters, signer boundaries, and basic observability contracts. It can serve as a **Go chain integration and transaction coordination layer** for a game backend.

It is not yet a complete, operations-ready Web3 game backend foundation by itself. The repository does not provide a game inventory/ledger, a production indexer integration or full historical backfill, a production metrics exporter, production RPC validation evidence, or multi-process EVM nonce coordination for a shared signer. Treat chain observations as inputs to a game-owned ledger and make rewards/ownership changes idempotent and finality-aware.

## 验收口径补记

- 仓库没有 `package.json`，因此不存在可确认的 npm 文档构建脚本；项目可用的仓库级构建/验证入口是 `make ci`（`Makefile` 中包含 fmt-check、vet、build、test）。
- 外部资料检索日期统一为 2026-10-19；没有引入或粘贴任何密钥。数据外发/云备份默认关闭；如宿主部署引入云备份或外部数据导出，必须由宿主显式启用，本文分析材料本身不外发。
