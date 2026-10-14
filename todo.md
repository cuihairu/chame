# Chame TODO

规格来源：[docs/design/chame-spec.md](docs/design/chame-spec.md)（2026-10-14 整理自外部设计稿）。
每条验收以**实际证据**（commit hash / 测试数 / 可运行产物）为准，不接受"做完了"。

## 仓库事务

- [x] License 选择与文件落地（README 徽章按实际 LICENSE 挂）—— Apache-2.0（用户仓库惯例），LICENSE 落地，双语 README 徽章已挂
- [x] 语言/工具链确定（Go 版本、模块路径、CI 矩阵）—— Go 1.26 起（本机 1.27.1），模块 `github.com/cuihairu/chame`，CI 矩阵 1.26.x + 1.27.x（`.github/workflows/ci.yml`）
- [x] 目录骨架按规格 §3 建立（先空壳 + 编译通过）—— 核心包 doc.go 空壳已建；`examples/ templates/ tools/ adapters/*/internal/` 待有内容再建（git 不跟踪空目录，规格 §3.1 允许）

## 待用户确认的架构决策（规格 §23）

- [x] 逐条确认 §23 列出的架构决策项 —— **用户已授权代决，逐条给出推荐方案并标注**：见 [docs/decisions/](docs/decisions/) ADR-0001 ~ 0008，均标「代决，用户可推翻」

## 分期（规格 §19）

- [x] 阶段一：Runtime + 配置系统 + 公共能力层（§2 §4 §5 §6）—— runtime 生命周期状态机/依赖图/优雅关闭、module Registry、config 分层加载与 §5.4 校验、identifiers（NetworkID/Address/Amount/AssetID）、health/logging/clock、capability 声明检测、examples/minimal；51 个测试函数全绿（含 -race）
- [x] 阶段二：交易公共模型与状态机（§9）—— `capability/transaction`：OperationID/TransactionID、12 状态枚举（created→…→finalized/reverted/failed + reorged/unknown 表达不确定性）、合法转换矩阵（终态冻结、超时进 unknown 不进 failed、reorged 仅可回 unknown）、Transaction 记录（Network 钉死、IdempotencyKey/RequestHash、ChainData 归适配器）、TransitionTo 写时复制；57 个测试函数全绿（含 -race）
- [x] 阶段三：EVM Adapter（§7，只读能力）—— `adapters/evm`：多网络实例（mainnet/base/arbitrum 三元组验证隔离）、Endpoint Pool 读切换（transient 才切换、identity 不切换）、启动时逐端点 Chain ID 校验（不一致=配置错误，不是 outage）、`network.Provider` 健康检查、原生/ERC-20 余额、eth_call、日志读取、基础指标；`core/errors` §14 全量分类与 cause 保留；97 个测试函数全绿（含 -race），全部走假客户端，无真实 RPC
- [x] 阶段四：Solana Adapter（§8）—— `adapters/solana`（独立于 EVM，ADR-0003 只读先行）：多集群实例、启动时逐端点 Genesis Hash 校验（不一致=配置错误，不是 outage）、`network.Provider` 健康检查、账户/原生余额/SPL Token 账户与余额、交易与签名历史查询、签名状态查询、Commitment 配置（processed/confirmed/finalized，交易查询拒绝 processed）、显式 unsupported-capability 契约（调用交易能力得到明确错误）、基础指标；SDK 模块路径按 ADR-0001 口径落为 `github.com/gagliardetto/solana-go` v1.25.0（`solana-foundation` 路径不被该模块认领）；公钥/账户/SPL 语义原样保留（lamport 不换算、SPL 金额走字符串不经过 float），SDK 类型不出包（ADR-0001）；全部走假客户端，无真实 RPC
- [x] 阶段五：幂等 / 持久化 / 恢复（§10）—— `capability/transaction` Repository 契约（Create/Get/FindByIdempotencyKey/CAS UpdateStatus/FindStale/Claim/Release 租约，§10.1 冲突走 `cherrs.ErrIdempotencyConflict`，同键同请求哈希为幂等 no-op 不产生第二笔操作）+ `infrastructure/persistence/memory`（仅测试/示例用）+ `tests/conformance/repository` 共享语义套件（幂等唯一性含并发竞争、状态机 CAS、过期扫描分页、租约互斥/续租/过期接管、记录校验、ChainData 往返）+ `application/idempotency` Begin 服务（created/existing/conflict 三态，创建竞争后重查收敛到同一条记录）+ `application/transaction` 恢复 Worker（§10.4：可配置扫描间隔/租约 TTL/任务超时/并发上限/批量/最小滞留年龄/卡死状态集，逐条租约认领、矩阵化 CAS 推进、指标钩子与日志、手动对账入口 ReconcileOne、多实例去重有测试）+ `infrastructure/persistence/sql` PostgreSQL 实现（ADR-0002：pgx v5 stdlib + database/sql，内嵌版本化迁移 chame_schema_migrations，(scope,key) 部分唯一索引 + ON CONFLICT 幂等插入，单语句条件租约）；SQL 全量过同一 conformance 套件（CHAME_TEST_PG_DSN 门控，未设则诚实 skip，CI 挂 postgres service 容器）；174 个测试函数全绿（含 -race，SQL 部分走真实 PostgreSQL 容器）
- [x] 阶段六：链上事件处理（§11）—— `capability/event`：§11.2 事件记录（网络/来源类型/交易/区块位置/事件索引/规范化类型/原始数据/解码版本/观察时间/观察状态/最终性/处理状态/尝试次数）+ §11.3 链族感知唯一身份 `PositionKey`（网络+来源+交易+区块哈希+高度+索引+locator，交易哈希绝不当身份）+ `Repository` 契约（身份唯一 Create/FindPending 链序扫描/处理 CAS/MarkReorged/游标读写含回滚审计）+ `Source` 契约（Poll 只回已提交事件、Head 供重组检测、Observe 补确认）；`application/eventing` Dispatcher（§11.4 至少一次投递+幂等消费基线：轮询→持久化→去重→按配置等最终性→按类型路由 Handler→失败指数退避重试→预算耗尽转 poisoned 可见不消失；§11.5 持久化后才推游标、重读窗口零重复业务效果、回退/哈希不一致即 ErrCursorRegression；§11.6 重组：Head 哈希比对→失效队列事件（已投递留终态，补偿归应用）→回滚一级游标（父块不可验证则全量重扫，身份去重兜底）→审计计数不随前进丢失）；`core/module` 补齐阶段一欠账：Registry 后台任务注册位（runtime 起后启动、停模块前取消等待，panic 不带崩进程，忽略截止期不阻塞关停）+ 事件处理器注册位（按事件类型全局唯一，值经 capability 式类型断言绑定进 Dispatcher）；`infrastructure/persistence/memory` EventStore + `infrastructure/persistence/sql` EventStore（迁移 0002：chame_events/chame_event_cursors，JSONB 数据语义化往返、单语句条件游标 upsert 保单调、回滚计数原子上递增）；共享 conformance 套件 35 子测试，memory 与 SQL（真实 PostgreSQL，CHAME_TEST_PG_DSN 门控，CI 已挂 service）同套全过；203 个测试函数全绿（含 -race）。适配器侧 event.Source 实现（EVM 日志源补 BlockHash、Solana 签名源）随后续阶段接入，见下
- [ ] 适配器事件源：adapters/evm 与 adapters/solana 实现 `capability/event.Source`（EVM：LogRecord 补区块哈希 + HeaderByNumber + 确认深度窗口；Solana：按 slot 轮询签名 + commitment 语义），并为 examples 增加事件消费示例
- [ ] 阶段七：Signer 与密钥安全（§12）
- [ ] 阶段八：AccountProvider / AssetProvider（§13）
- [ ] 阶段九：错误体系 + 可观测性（§14 §15）
- [ ] 阶段十：测试体系 + CI（§16 §20）
- [ ] 首期完成定义逐条核对（§22）—— 五组验收全绿才谈发布

## 红线

- 发布正式版本必须由**用户明确给出版本号并触发**
- 单 commit 英文简短，rebase 后推，不打 tag，不 force push
- 文档不得出现 AI 设计 / ui ux pro max / 舰队 等元叙事字样
