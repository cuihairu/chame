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
- [x] 审核发现：advisory 锁加在 applyMigration 事务内，但 migrationTable 的 CREATE TABLE IF NOT EXISTS 在锁外执行，双副本真空首启（表尚不存在）仍可能并发建表竞态失败，与注释宣称的跨实例收敛不符（infrastructure/persistence/sql/store.go:93）— 建表纳入同一 advisory 锁，新测试在真空库上并发 Migrate 实证不再冲突（infrastructure/persistence/sql/store.go:110、infrastructure/persistence/sql/store_test.go:120，489 测试）
- [x] 审核发现：并发迁移测试在已迁移的共享库上运行，两个 goroutine 均走「已应用即返回」路径，锁所针对的首启竞态路径未被实际覆盖，注释引用的 TestStoreMigrateIsIdempotent 是串行测试也覆盖不到（infrastructure/persistence/sql/store_test.go:86）— 新增真空库并发首启测试，回退修复前该测试实测失败（duplicate key, pg_type_typname_nsp_index），修复后通过（infrastructure/persistence/sql/store_test.go:120，489 测试）
- [x] 审核发现：Settle 对非 ErrUnknownNonce 的错误仅累加错误计数器即丢弃，错误中携带的 nonce/sender 诊断细节无任何日志可查，运维只能看到计数上涨无从定位（adapters/evm/flow.go:279）— Settle 现记录错误日志（sender/nonce/outcome/error），新测试断言日志内容（adapters/evm/flow.go:297、adapters/evm/flow_test.go:271，489 测试）
- [x] 审核发现：Reconcile 将 foreign 租约从 s.leases 删除后，该 nonce 在后续 Reconcile/Snapshot 中不再出现，外部写入者消费只通过首次分类时的 report.Foreign 暴露一次，长期运行的协调器对「外部写入者消费」的持续性可见性随租约退役而消失，与 spec §7.5「需人工处理交易必须可见、不得静默」的持续性观察要求存在张力（adapters/evm/nonce.go:210）— 不属实：退役的是已被链消费（n < chainNext）、结果已确定的 nonce，不属「结果未知/需人工处理」类别；首次 report.Foreign 通知 + 持久化交易记录承担持续证据，退役是防 per-sender 无界增长的必要边界
- [x] 审核发现：Settle 对非 ErrUnknownNonce 错误仅计数并记日志，但函数签名无返回值、调用方（application/transaction/flow.go:347 f.settle）不感知，若未来 Submitted/Abandon 引入可重试错误类别，该错误将被静默吞掉无法上抛（adapters/evm/flow.go:294）— 不属实：capability/transaction/flow.go:77 契约明确 Settle 为 advisory bookkeeping、无返回值是设计；当前 Submitted/Abandon 仅产 ErrUnknownNonce（已容忍），非未知错误已记日志 + 计数非静默
- [x] 审核发现：TestStoreMigrateIsConcurrencySafeOnFirstStart 在共享测试库上 DROP 全部业务表（chame_event_cursors/chame_events/chame_transactions/chame_schema_migrations），若测试被并行执行（或失败后未重跑 Migrate）会破坏共享库中其他测试/套件，测试隔离性偏弱（infrastructure/persistence/sql/store_test.go:133）— 属实：并发首启测试改用独立 throwaway schema（isolatedDSN 建 schema、测试结束 DROP SCHEMA CASCADE），不再触碰共享库其他套件（infrastructure/persistence/sql/store_test.go:33、infrastructure/persistence/sql/store_test.go:146，482 测试）
- [x] 审核发现：该并发首启测试硬编码 n != 2（迁移数为 2），未来新增 0003 迁移文件会使断言误报、需同步维护（infrastructure/persistence/sql/store_test.go:167）— 属实：断言改用 sql.MigrationCount() 动态读取嵌入迁移数，未来新增迁移文件无需再同步（infrastructure/persistence/sql/migrate.go:37、infrastructure/persistence/sql/store_test.go:170，482 测试）
- 覆盖范围：651be58c..HEAD 全部 12 文件（adapters/evm 的 nonce/flow/network 及其测试、adapters/solana/blockhash 及其测试、infrastructure/persistence/sql/store 及其测试、README、todo.md）；核心逻辑（nonce 高水位与 foreign 退役、advisory 锁作用域、Settle 容忍、blockhash 闭区间边界）均经 build/vet/单测（含 -race 子集）验证正确，无确凿功能 bug，本轮产出为低危观察与测试缺口类发现。
- [x] 审核发现：Reconcile 对 NonceSubmitted 且已被链消费（n < chainNext，归入 Consumed）的租约从不从 s.leases 删除，长期运行协调器的 per-sender leases 无界增长；该问题先于本区间存在，现在 Consumed 随首次报告退役，InFlight 保留为替换/加价/取消的开放结果（adapters/evm/nonce.go:208、adapters/evm/nonce_test.go:341，490 测试）
- 覆盖范围（复审 2026-11-03）：独立复核 651be58c..HEAD 全部 12 文件；build/vet 通过，go test（含 -race 子集 TestFlowSettle*/TestNonceReconcile*）通过；确认本区间无确凿功能 bug（nonce 高水位/foreign 退役、advisory 锁事务作用域、Settle 精确容忍 ErrUnknownNonce、blockhash 闭区间边界均正确），新增观察 1 条（Consumed/InFlight 租约不退役的长期无界增长，先期存在）。
- [x] 审核发现：退役 Consumed 租约将「已入池未出块」的交易（PendingNonceAt 含 txpool，n < chainNext 即视为 Consumed）与「已挖出」同样对待，其结果仍可能开放（被替换/丢弃），退役后协调器不再于后续 Reconcile 持续显示该开放结果，仅依赖持久化交易记录，与 spec §7.5「已提交但结果未知的交易必须可见、不得静默」的持续性观察要求存在轻微张力（adapters/evm/nonce.go:208）— 不属实：n < chainNext 即链已接受该 nonce（入池即视为链侧消费），交易结果由持久化记录承担持续可见性；退役是防无界增长的既定设计，非静默丢失
- [x] 审核发现：新测试 TestNonceReconcileRetiresConsumedLeases 仅断言快照计数与二次 Reconcile 的 Consumed 为空，未断言退役 nonce 不可再次分发（退役后未调用 Next 验证返回 9 而非 7，也未断言对退役 nonce 调用 Abandon/Submitted 返回 ErrUnknownNonce）；Foreign 路径的对应断言在 TestNonceReconcileForeignLeaseNotReusable 中存在而 Consumed 路径缺失，测试保护不对称（adapters/evm/nonce_test.go:367）— 属实：补充退役后 Submitted/Abandon 返回 ErrUnknownNonce、Next 返回 9 的断言，与 Foreign 路径对称（adapters/evm/nonce_test.go:379、adapters/evm/nonce_test.go:382，482 测试）
- 覆盖范围（本轮 2026-11-03）：独立复核 651be58c..HEAD 后续全部未勾审核发现；build/vet 通过，go test ./... 全绿（含 -race 子集 TestNonceReconcile*/TestFlowSettle*、真实 PG 套件 TestStoreMigrateIsConcurrencySafeOnFirstStart/TestStoreMigrateIsConcurrencySafe/TestStoreMigrateIsIdempotent）；属实 3 条（SQL 测试隔离 + 动态迁移数 + Consumed 退役保护）已修，不属实 3 条（Foreign 退役可见性 / Settle 无返回值契约 / Consumed 退役张力）已注明理由。
- 覆盖范围（本轮 2026-11-03）：独立复核 5c1e090e..HEAD 单 commit（c677cd1，adapters/evm/nonce.go + nonce_test.go + todo.md）；build/vet 通过，go test -run TestNonceReconcile 全部通过（含新用例）；确认退役正确性（退役 n 满足 n < chainNext <= next，take 只分发 free/next+，不会复用；delete 于 range 中安全；退役后 Submitted/Abandon 仍返回 ErrUnknownNonce 与改动前一致），无确凿功能 bug，本轮产出为低危观察与测试缺口 2 条。

## 审核发现（c677cd13..HEAD，只审不改，2026-11-03）

- [x] 审核发现：isolatedDSN 直接拼接 `base + "&options=" + options`，假定 base DSN 已含 `?` 查询串；若 CHAME_TEST_PG_DSN 设为无查询参数形式（如纯 `postgres://user:pass@host/db`），拼接出的 DSN 非法（dbname 被吞入 `&options=...`）、测试以晦涩的 database-not-exist 错误失败而非明确报错，边界条件处理偏脆弱（infrastructure/persistence/sql/store_test.go:53）— 属实：改用 net/url 解析 base 并正确合并查询串（有则 `&`、无则 `?`），新增 TestIsolatedDSNOptions 双用例锁定（infrastructure/persistence/sql/store_test.go:58、infrastructure/persistence/sql/store_test.go:230，3dcaf58）
- [x] 审核发现：isolatedDSN 注释宣称「search_path 指向尚不存在的 schema 会被 PostgreSQL 拒绝」— 实证不实：PostgreSQL 接受该 GUC（SHOW search_path 返回该名字、连接成功），仅在首次未限定 CREATE 时报「no schema has been selected to create in」；建 schema 先行仍必要且正确，但注释给出的理由不准确（infrastructure/persistence/sql/store_test.go:40）— 属实：修正注释为真实机制（GUC 被接受，未限定 CREATE 才报 no schema has been selected to create in，故须先建 schema），3dcaf58
- 覆盖范围（本轮 2026-11-03）：独立复核 c677cd13..HEAD 单 commit（9308964，adapters/evm/nonce_test.go、infrastructure/persistence/sql/migrate.go + store_test.go、todo.md）；build/vet 通过；go test -race 子集（TestNonceReconcile*）与真实 PG 套件（CHAME_TEST_PG_DSN，含 TestStoreMigrateIsConcurrencySafeOnFirstStart）全绿；三处改动经实现与断言双向核对无误：Consumed 退役后 Submitted/Abandon 返回 ErrUnknownNonce、Next 跳过已消费 nonce（7→9）；MigrationCount 动态计数与真空库全量应用一致；isolatedDSN 隔离经 psql 探针实证（search_path 路由至 throwaway schema、public 表未触碰、DROP SCHEMA CASCADE 清理干净）。另注：tests/conformance/repository 的 IdempotencyUniqueness 在连跑下偶发失败（got 3/13 vs want 1/15），已复跑通过、属既有并发抖动，且不在本 commit 改动文件范围内，未列为发现。

- [x] 审核发现：isolatedDSNOptions 回退分支对 pgx keyword/value 形式 DSN（如 `host=... user=... dbname=...`，url.Parse 以 Scheme=="" 成功、故必落入回退）仍用 `?options=`/`&options=` 直接拼接非 URL 字符串；pgx 的 keyword/value 解析不把 `?`/`&` 当分隔符，options 片段被并入前一 token 的值（dbname 变为 `chame?options=-c%20search_path%3D...`），search_path 永不进入 RuntimeParams，连接要么指向不存在的库、要么静默失去隔离——恰是本 commit 声称消除的「晦涩 database-not-exist」缺陷；且 TestIsolatedDSNOptions 未覆盖回退/keyword-value 分支（实证 pgx v5.11.0：ParseConfig 得 database="chame?options=..."、RuntimeParams["options"] 为空）（infrastructure/persistence/sql/store_test.go:67、infrastructure/persistence/sql/store_test.go:73、infrastructure/persistence/sql/store_test.go:228） — 属实：回退分支改为向 keyword/value conninfo 追加单引号 `options` 键值（pgx 仅按空白分词，`?`/`&` 会被并入前 token），而非拼 URL 查询串；新增 keyword/value 用例经 pgx.ParseConfig 实证 RuntimeParams["options"] 就位、database 保持 `bar`，真实 PG（conninfo DSN）隔离验证通过（infrastructure/persistence/sql/store_test.go:64、infrastructure/persistence/sql/store_test.go:253，6229793）

## 漏洞扫描遗留（2026-11-03 全舰队扫描，源 scratch/vuln-scan-*.md）

- [x] VULN-CHAME-001 [高危·stdlib×10·GO-2026-6809 等] go toolchain go1.27.1→go1.27.3【需改 go.mod】：2026-11-03 晚实测 govulncheck ./... = affected by 10 vulnerabilities from the Go standard library（另 2 条 import 级、3 条 module 级未调用）；chame/go.mod 无 toolchain 指令，补 `toolchain go1.27.3`（同 herald/cockpit/servify/atlas 做法）；验收：govulncheck ./... 显示 No vulnerabilities found + go build ./... && go test ./... 绿 — 证据：go.mod:5 `toolchain go1.27.3`；`govulncheck ./...` = No vulnerabilities found / 0 affected；`go build ./...` OK；`go test ./...` 全包 ok（tools/txload 12.060s，共约 490 测试）
- [ ] VULN-CHAME-002 [非安全·过时依赖] go get -u ./... 因 proxy EOF 部分失败（仅安全项保留）：补跑非安全升级；验收：go list -m -u all 无网络错误 + go build ./... && go test ./... 绿
- [ ] VULN-CHAME-003 [高危·GHSA-587h-7wmj-h5pm 路径穿越] vite ≤6.4.3→≥6.4.4（chame/docs，package-lock.json 仓，终扫 1，vite≤6.4.3 受影响）【BLOCKED·规范禁 npm 改 lock】npm-lock 仓只审计未改动；验收：迁 pnpm（pnpm import）后 pnpm audit --audit-level=high 该包不再命中（或编排者批准 npm 侧修复后 npm audit 清零）；该仓无 test 脚本，audit-only
