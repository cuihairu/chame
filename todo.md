# Chame TODO

规格来源：[docs/design/chame-spec.md](docs/design/chame-spec.md)。验收以实际证据为准：每项完成条目保留一条证据行（commit hash + 测试数）。

## 仓库事务

- [x] License 选择与文件落地（Apache-2.0，README 徽章按实际 LICENSE 挂） — c8f108c
- [x] 语言/工具链确定（Go 1.26+，模块 github.com/cuihairu/chame，CI 矩阵 1.26.x + 1.27.x） — b1715b7
- [x] 目录骨架按规格 §3 建立（核心包 doc.go 空壳，编译通过） — c8f108c

## 待用户确认的架构决策（规格 §23）

- [x] 逐条确认 §23 架构决策项（ADR-0001~0008，代决，用户可推翻） — 3dd3a1b

## 分期（规格 §19）

- [x] 阶段一：Runtime + 配置系统 + 公共能力层（§2 §4 §5 §6） — 58f8c45，51 测试
- [x] 阶段二：交易公共模型与状态机（§9） — 33ec919，57 测试
- [x] 阶段三：EVM Adapter 只读（§7）+ 错误体系（§14） — b35a7f1，97 测试
- [x] 阶段四：Solana Adapter 只读（§8，ADR-0003） — bc20949，经共享适配器契约套件
- [x] 阶段五：幂等 / 持久化 / 恢复（§10） — dadab89，174 测试
- [x] 阶段六：链上事件处理（§11） — 1ad0731，203 测试
- [x] 适配器事件源（evm.LogSource 9c4cfed / solana.SignatureSource 9392cd1 / examples/eventstream bc890d8） — bc890d8
- [x] 阶段七：Signer 与密钥安全（§12） — 25da458，224 测试
- [x] 阶段七后续：远端签名客户端（infrastructure/signer/remote.Client） — df7475d，11 测试
- [x] 交易阶段·EVM Nonce 协调器（§7.5） — 99da3f4，13 测试
- [x] 交易阶段·EVM 交易签名（§12.1 SigEVMTransaction） — e231bf9，8 测试
- [x] 交易阶段·EVM 提交/回执状态映射（§7.6 §7.8） — b7dd8b1，9 测试
- [x] 交易阶段·EVM 交易准备 builder（§7.6） — 4e2642f，13 测试
- [x] 交易阶段·Solana Blockhash 期限判定（§8.4） — 72a99aa，10 测试
- [x] 交易阶段·Solana 交易准备/签名/提交（§8.2 §8.3） — 041d832，9 测试
- [x] 交易阶段·链观察对接 reconciler（§7.8 §8.5 §10.4） — 296566e，21 测试
- [x] 交易阶段·流程服务接入状态机（§9 §10 §14.3） — 2fdb7e7，32 测试
- [x] 交易阶段·收尾 StatusOf 契约测试入 conformance — 1fc9266
- [x] 交易阶段·运行时装配模块（application/transaction.Module） — 89a8796，4 测试
- [x] 阶段八：AccountProvider / AssetProvider（§13） — 07b8002，239 测试
- [x] 阶段九：错误体系 + 可观测性（§14 §15） — 359a1bc，303 测试
- [x] 阶段十：测试体系 + CI（§16 §20） — 08f9b40，308 测试
- [x] 首期完成定义逐条核对（§22，21 项） — 3dd3a1b，308 测试
- [x] 补齐清单第 7 条：生产观测与成本接口（prometheus /metrics + CostRecorder / CostQuery） — 87e006f
- [x] 补齐清单第 8 条：审批、限额与审计内控（默认关闭，WithInternalControls） — 40a1bc0

## 代码深审遗留（docs/review/2026-11-01-code-review.md）

- [x] R1(P2)：EVM nonce Reconcile 同步 s.next = max(s.next, s.chainNext)，外部写入者后不再分发陈旧 nonce — fa6d672，484 测试
- [x] R2(P2)：Solana blockhash 期限边界对运行时 check_hash_age 实证（开/闭区间）后统一注释与测试 — f986d54，486 测试（实证为闭区间：Agave runtime last_valid = height + max_age − age，age ≤ max_age 即有效）
- [x] R3(P3)：SQL applyMigration 加 pg_advisory_xact_lock，多副本并发首启不冲突 — 49c6ace，486 测试（真实 PG 并发 Migrate 验证通过）
- [x] R4(P3)：EVM Settle 精确容忍 ErrUnknownNonce，其余错误透传或记日志 — 03dd8a3，486 测试（不可达错误入错误计数器）

## 红线

- 发布正式版本必须由用户明确给出版本号并触发
- 单 commit 英文简短，rebase 后推，不打 tag，不 force push
- 文档不得出现 AI 设计 / ui ux pro max / 舰队 等元叙事字样

## 审核发现（651be58..HEAD）

- [x] 审核发现：Reconcile 只把低于 chainNext 的 nonce 从 free 池删除、却将 Foreign 租约留在 leases 中，该租约随后经 Abandon 重新入 free 池，链已消费的 nonce 可被再次分发，恰好绕过本修复的目标（adapters/evm/nonce.go:148）— Reconcile 现在删除 Foreign 租约，Abandon 返回 ErrUnknownNonce，不再复用（adapters/evm/nonce.go:210、adapters/evm/nonce_test.go:315，480 测试）
- [ ] 审核发现：advisory 锁加在 applyMigration 事务内，但 migrationTable 的 CREATE TABLE IF NOT EXISTS 在锁外执行，双副本真空首启（表尚不存在）仍可能并发建表竞态失败，与注释宣称的跨实例收敛不符（infrastructure/persistence/sql/store.go:93）
- [ ] 审核发现：并发迁移测试在已迁移的共享库上运行，两个 goroutine 均走「已应用即返回」路径，锁所针对的首启竞态路径未被实际覆盖，注释引用的 TestStoreMigrateIsIdempotent 是串行测试也覆盖不到（infrastructure/persistence/sql/store_test.go:86）
- [ ] 审核发现：Settle 对非 ErrUnknownNonce 的错误仅累加错误计数器即丢弃，错误中携带的 nonce/sender 诊断细节无任何日志可查，运维只能看到计数上涨无从定位（adapters/evm/flow.go:279）
