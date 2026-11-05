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
- [x] VULN-CHAME-002 [非安全·过时依赖] go get -u ./... 因 proxy EOF 部分失败（仅安全项保留）：补跑非安全升级；验收：go list -m -u all 无网络错误 + go build ./... && go test ./... 绿 — 证据：`go get -u ./...` 成功（无 proxy EOF；升级 go 1.26.0→1.26.8、ethereum v1.17.10、solana-go v1.26.0、pgx v5.12.0 等，go.mod:5）；`go mod tidy` OK；`go list -m -u all` exit 0 无网络错误；`go build ./...` OK；`go test ./...` 全包 ok（tools/txload 12.138s，501 测试函数）；`govulncheck ./...` 复扫仍 0 漏洞
- [x] VULN-CHAME-003 [高危·GHSA-587h-7wmj-h5pm 命令注入] vite ≤6.4.3→≥6.4.4（chame/docs，pnpm 仓，终扫 0）【迁移 pnpm】npm-lock 仓按规范不 npm 改 lock，迁 pnpm：`pnpm import` 生成 pnpm-lock.yaml，package.json 加 `pnpm.overrides.vite=6.4.5`（vitepress 1.6.4 声明 vite ^5.4.14，须 override 抬升）；依赖树 vite 6.4.5 ≥6.4.4，GHSA-587h-7wmj-h5pm 不再命中，`pnpm audit --audit-level=high` = No known vulnerabilities found（metadata vulnerabilities 全 0：info/low/moderate/high/critical = 0/0/0/0/0，vite 无 finding），`pnpm run docs:build` 通过（vitepress v1.6.4，build complete in 84.92s）；验证后移除 package-lock.json；该仓无 test 脚本，audit-only 验收

- [x] 审核发现：isolatedDSNOptions 对 options 值里的单引号用 `strings.ReplaceAll(options, "'", "''")`（双写单引号）转义，但 pgx v5.11.0 的 conninfo 单引号值解析只认反斜杠转义 `\'`、不认 `''` 双写（pgconn/config.go:824-843 在首个未转义 `'` 处即终止值）；探针实证 `options='-c search_path=s''x'` → pgconn.ParseConfig 报 `invalid keyword/value`，而 `\'` 形式解析正确得 `-c search_path=s'x`。若 schema 未来含 `'`（如生成方式变动），DSN 会解析失败（响亮失败而非静默丢失隔离，故非安全缺陷）；当前 schema 恒为 `chame_sql_test_%d`（仅数字/小写/下划线，isolatedDSN 内部生成），转义路径实际不可达——属潜伏正确性缺陷 + 转义约定与 pgx/libpq 不符（正确约定为 `\'` 或干脆无需转义），且该路径无测试覆盖，一旦触发即误报于测试运行（infrastructure/persistence/sql/store_test.go:74） — 88c6be6，改用 `\'` 反斜杠转义（pgx v5.12.0 唯一识别），`TestIsolatedDSNOptionsEscapesQuote` 用含 `'` schema 实证往返正确（infrastructure/persistence/sql/store_test.go:303）
- [x] 审核发现：TestIsolatedDSNOptions 未覆盖「base DSN 已含 options 键」的重复键场景。隔离正确性依赖追加的 options 胜出：实测 pgx v5.11.0 两形式均为后写者胜出（keyword/value 顺序覆盖 config.go:855；URL 查询参数 last-wins parse_url.go:281），行为正确但无用例固定；若 pgx 解析器语义变更（如改先值优先），隔离会静默丢失且无任何测试告警。建议补两例：base=`host=... dbname=bar options='-c search_path=base'` 与 base=`postgres://h/db?options=-c%20search_path%3Dbase`，断言 ParseConfig 后 RuntimeParams["options"] 为隔离 search_path（infrastructure/persistence/sql/store_test.go:234） — 88c6be6，新增两用例经 pgx.ParseConfig 断言隔离 search_path 胜出（infrastructure/persistence/sql/store_test.go:258、store_test.go:264）
- [x] 审核发现：VULN-CHAME-003 将 GHSA-587h-7wmj-h5pm 标注为「路径穿越」，该 advisory 实为 Command Injection（CVSS 7.5、Windows-only、经 launch-editor ≤2.14.1，修复于 launch-editor 2.14.2 / vite 6.4.4），advisory 类型标签与根因描述有误；版本范围判定属实（实测 npm audit 报 vite 5.4.21 high、range ≤6.4.3、via 含 launch-editor 系列与 vite `.map` 路径穿越及 esbuild 链），建议仅精确化措辞（根因在 launch-editor 命令注入，非路径穿越；且 5.x 线在声明范围内无对应修复版本，npm fixAvailable=False）（todo.md:86） — 2026-11-04 复核实证：OSV GHSA-587h-7wmj-h5pm = `launch-editor: fs.existsSync() existence check can be bypassed on Windows`（aliases CVE-2026-112942、CVSS 4.0 高），确为 Windows 命令执行而非路径穿越；`npm audit --package-lock-only --json` 显示 vite high ≤6.4.3 fixAvailable=False，via 链含 launch-editor 5 项（含 `escapeCmdArgs() omits % escaping`、`Editor CLI flag misinterpretation causing arbitrary command execution`）与 vite `.map` 路径穿越（≤6.4.1）及 esbuild（≤0.24.2 moderate，无修复）。措辞修正已并入本行与 VULN-CHAME-003 条目
- [x] 审核发现：TestIsolatedDSNOptions 的 URL 形态用例只经 net/url 断言（`u.Path=="/db"` 且 `u.Query().Get("options") != ""`，未断言解码后确切值、未做 pgx.ParseConfig 往返），与 conninfo 用例（经 pgx.ParseConfig 断言确切 `RuntimeParams["options"]`）不对称。URL 分支的真实消费者是 pgx stdlib（sql.Open→pgconn.ParseConfig，与 net/url 解析语义不同：如 `+`/`%20` 处理、重复键先/后写胜出）；若 URL 分支回归（如 `%20` 被还原为 `+`、options 值丢失 `-c`、双重编码），当前测试因仅查「非空」而静默通过。建议 URL 用例同样做 pgx.ParseConfig 往返并断言 `RuntimeParams["options"]=="-c search_path="+schema`（infrastructure/persistence/sql/store_test.go:280、infrastructure/persistence/sql/store_test.go:287） — 88c6be6，全部用例统一走 pgx.ParseConfig 往返并断言确切 `RuntimeParams["options"]`（infrastructure/persistence/sql/store_test.go:276）
- 覆盖范围（本轮 2026-11-03）：独立复核 86662087245974e4ca7ccf7af43cb138366115d8..HEAD 全量 3 commit（6229793 fix(sql): isolate conninfo DSN options correctly；c203fb6、9624cc3 均为 todo.md 文档记录）；改动文件 infrastructure/persistence/sql/store_test.go（代码）+ todo.md（文档）。验证：逐行读 pgx v5.11.0 解析器源码（pgconn/config.go:769-859 keyword/value、pgconn/parse_url.go:60-292 URL/query）＋ scratch 模块探针实测 pgconn.ParseConfig（`''` 非法、`\'` 正确、重复 options 两形式后写胜出、URL 缺 scheme 响亮报错）＋ `go test -run TestIsolatedDSNOptions` 绿（cached）、`go vet ./infrastructure/persistence/sql/` 与 `go build ./...` 通过；VULN 条目以 `npm audit --package-lock-only` 实测核对（vite 5.4.21 high/≤6.4.3 属实，go.mod 无 toolchain 属实）。安全维度（注入/反序列化/越界/泄漏/竞态/错误处理）未发现新增风险：base DSN 按来源使用、schema 为内部生成值、无网络/无文件/无并发新面；错误路径均响亮失败而非静默丢隔离。核心修复（keyword/value 分支）经实证正确。本区间未见越权改动或未覆盖高优路径。
- [x] 审核发现：isolatedDSNOptions 的 keyword/value 分支转义只处理单引号（`strings.ReplaceAll(options,"'", "\\'")`），未处理反斜杠：pgx 引号值解析器把 `\` 当转义符（pgconn/config.go:852 遇 `\` 即跳过下一字符），schema 含 `\` 时被静默吞掉（探针实证 `foo\bar`→解析得 `-c search_path=foobar`，无报错）；新增 TestIsolatedDSNOptionsEscapesQuote 只钉住含 `'` 情形，`\` 路径仍无覆盖（infrastructure/persistence/sql/store_test.go:77） — 属实：转义先反斜杠后引号（`\\`→`\\`、`'`→`\'`），新增 TestIsolatedDSNOptionsEscapesBackslash 钉住 `\` 路径；回退修复前该测试实测失败（options 得 `-c search_path=chame_sql_test_probe`，`\probe` 被吞），修复后通过（infrastructure/persistence/sql/store_test.go:77、store_test.go:325，495 测试）
- [x] 审核发现：docs 迁移 pnpm（删除 package-lock.json、新增 pnpm-lock.yaml）但 docs/package.json 无 `packageManager` 字段、也无 .npmrc 强制 pnpm；`npm install` 会忽略 `pnpm.overrides.vite`、重建 package-lock.json，并按 vitepress ^1.6.4 声明的 vite ^5.4.14 装回无修复版本的 vite 5.4.x，静默撤销 GHSA-587h-7wmj-h5pm 的缓解（docs/package.json:13） — 属实：`npm install --package-lock-only` 实测解析 vite 5.4.21（忽略 pnpm.overrides）；修复加 npm 原生 `overrides.vite=6.4.5` + `packageManager=pnpm@12.10.1` + pnpm-workspace.yaml 的 `overrides`/`allowBuilds`（pnpm 12 已不读 package.json 的 `pnpm.overrides`），重新生成 pnpm-lock.yaml 锁 vite 6.4.5；`npm install --package-lock-only` 复测 vite 6.4.5，`pnpm install --frozen-lockfile` 与 `pnpm run docs:build`（vitepress v1.6.4）均通过（docs/package.json:13、docs/pnpm-workspace.yaml、.github/workflows/docs.yml:16）
- [x] 审核发现：TestIsolatedDSNOptions 把数据库断言统一放宽为 `cfg.Database != "db" && cfg.Database != "bar"`，使所有用例（含 dbname=bar 的 keyword/value 形态与 /db 的 URL 形态）都接受任一值，丢失原 kv 用例精确断言 `== "bar"`、URL 用例 `u.Path=="/db"` 的区分度；若 URL 分支回归把库名写成 bar（或反之）不会告警（infrastructure/persistence/sql/store_test.go:289） — 属实：用例结构加 `database` 字段，按 base 形态断言确切库名（URL 形态 `db`、kv 形态 `bar`），恢复原精确区分度（infrastructure/persistence/sql/store_test.go:244、store_test.go:296，495 测试）
- [x] 审核发现：docs/package.json 保留 pnpm 12 已不读取的 `pnpm.overrides` 死配置块（实际 override 已迁至 pnpm-workspace.yaml:3-4），每次 `pnpm install`（含 CI）都会打印 `[WARN] The "pnpm" field in package.json is no longer read by pnpm ... "pnpm.overrides"`，与该提交声称的修复意图相悖，建议删除以免持续的告警噪音（docs/package.json:16） — 属实：实测 `pnpm install` 打印该 WARN；删除死配置块后 `pnpm install --frozen-lockfile` 无告警且通过（docs/package.json，`c5e0ba26ab158a79e2af22b51a3ac8d49588ce7b`，496 测试）
- [x] 审核发现：isolatedDSNOptions 的注释重写后残留重复词，store_test.go:73-74 读作 `RuntimeParams["options"]. The` 换行 `// The value is quoted...`（"The The"），属本提交引入的注释笔误，应改为单句（infrastructure/persistence/sql/store_test.go:74） — 属实：代码第 73 行确为重复 "The"；改为单句后 go test 通过（infrastructure/persistence/sql/store_test.go:73，`c5e0ba26ab158a79e2af22b51a3ac8d49588ce7b`，496 测试）
- [x] 审核发现：两个转义测试（TestIsolatedDSNOptionsEscapesQuote/Backslash，store_test.go:310-335）只覆盖 keyword/value 分支，URL 分支（url.QueryEscape 路径）对含 `'`/`\` schema 的转义无任何用例；URL 分支当下正确（探针实证 `'`/`\`/`\'`/空格/`+`/`&` 均往返），但回归（如去掉 `+`→`%20` 替换或双重编码）不会被现有用例捕获，与先前 88c6be6 修复的 URL/kv 对称性要求不一致（infrastructure/persistence/sql/store_test.go:310） — 属实：探针实证 URL 分支对 `'`/`\`/`\'`/空格/`+`/`&` 均正确往返；新增 TestIsolatedDSNOptionsEscapesURL 钉住 URL 分支（含 `'`/`\`/`\'`/空格/`+`/`&` 五例），go test 通过（infrastructure/persistence/sql/store_test.go:310、store_test.go:340，`c5e0ba26ab158a79e2af22b51a3ac8d49588ce7b`，496 测试）
- [x] 审核发现：docs.yml:30 用 `npm install -g pnpm@12` 装浮动最新 12.x（当前解析到 12.17.0），与 package.json `packageManager: pnpm@12.10.1` 及 lockfile 记录的 pnpm 12.10.1 不一致；CI 仅因 pnpm 的 `manage-package-manager-versions` 自切换（实测 12.17.0 运行时报告 using pnpm v12.10.1，需在安装期联网下载 12.10.1）才保持正确，且同一步 `pnpm --version` 打印的是全局 12.17.0 而非实际生效的 12.10.1，属误导性诊断；建议钉死版本（`pnpm@12.10.1` 或 `pnpm/action-setup` 指定 version）以去除运行时下载与版本漂移风险（.github/workflows/docs.yml:30） — 属实：docs.yml 确用浮动 `pnpm@12`；钉死为 `pnpm@12.10.1` 与 packageManager 一致，`pnpm install --frozen-lockfile` 与 `pnpm run docs:build` 均通过（.github/workflows/docs.yml:30，`c5e0ba26ab158a79e2af22b51a3ac8d49588ce7b`，496 测试）
- [x] 审核发现：pnpm-workspace.yaml:5-6 的 `allowBuilds` 为严格白名单，实测未列入的带 build script 依赖（core-js）使 `pnpm install` 直接 `ERR_PNPM_IGNORED_BUILDS` 硬失败；当前 docs 树仅 esbuild 一个构建脚本依赖（已验证放行），但 vitepress 或其传递依赖日后引入任何新 postinstall/构建脚本都会在未同步更新白名单时使 docs CI 中断，属本提交引入的、比原 npm ci 更严格的持续维护约束（docs/pnpm-workspace.yaml:5） — 属实：用 pnpm 12.10.1 实测 core-js（白名单外）`pnpm install` 报 `ERR_PNPM_IGNORED_BUILDS` 退出 1；补注释明确任何新增构建脚本依赖必须同步加入 `allowBuilds`，否则 CI 硬失败（docs/pnpm-workspace.yaml:5，`c5e0ba26ab158a79e2af22b51a3ac8d49588ce7b`，496 测试）

## 审核发现

- 审核发现：本轮 diff 未见问题（1ed414dc..HEAD）。 — 不属实：pnpm 12 实测忽略 docs/package.json 顶层 `overrides` 字段（探针解析 vite@5.4.21 而非锁定的 6.4.5），有效 override 实际仅存在于 pnpm-workspace.yaml，package.json 中为死配置；已删除并复测 `pnpm install --frozen-lockfile` 与 `pnpm run docs:build` 通过（docs/package.json）
- [x] 审核发现：删除 npm 的 overrides 后，使用 npm 安装文档依赖会忽略 pnpm-workspace.yaml 并重新解析出 Vite 5.x，使此前针对漏洞固定的 Vite 6.4.5 失效（docs/package.json:13） — 属实：无 npm `overrides` 时 `npm install --package-lock-only` 解析 vite 5.4.21（vitepress ^1.6.0→1.6.4 声明 vite ^5.4.14），pnpm-workspace.yaml 的 override 对 npm 无效；补回 package.json 顶层 `overrides.vite=6.4.5` 后 npm 解析 vite 6.4.5、`npm audit` 0 漏洞，pnpm 12.10.1 `pnpm install --frozen-lockfile` 与 `pnpm run docs:build` 均通过（docs/package.json:13，504 测试）
- 审核发现：代码块换行只设置在 pre 上，而 VitePress 默认样式为内部 code 显式设置 white-space: pre，导致长代码行仍不换行（docs/.vitepress/theme/custom.css:2） — 不属实：2e2fda2 已在 custom.css:2-3 同时选中 `pre` 与 `pre code`，构建产物中 `pre code` 规则特异性 (0,2,3) 高于默认 (0,2,1) 且次序靠后，长代码行确已换行
- 审核发现：无侧栏内容和带 aside 的内容宽度规则被 VitePress 带 scoped 属性的默认规则以更高优先级覆盖，桌面文档内容仍受原有宽度限制（docs/.vitepress/theme/custom.css:9） — 不属实：2e2fda2 已在 custom.css:7-15 为三条宽度规则加 `!important`，构建产物确认自定义规则覆盖带 scoped 属性的默认规则，内容宽度已为 960px
- [x] 审核发现：vite 锁定现分裂为双源——docs/package.json 顶层 `overrides`（仅对 npm 生效，对项目声明的包管理器 pnpm@12.10.1 为死配置，此前审核已实测确认 pnpm 忽略之）与 docs/pnpm-workspace.yaml 各一份，二者需手动同步，任一改动未同步即漂移（docs/package.json:13） — 属实：删除 package.json 顶层 overrides，pnpm-workspace.yaml:3 成为 Vite 6.4.5 唯一 override 来源；pnpm 12.10.1 `install --frozen-lockfile` 与 `run docs:build` 均通过（docs/pnpm-workspace.yaml:3，505 测试）
- [x] 审核发现：txload 司机 `Run` 在 HTTP 面读侧并发下写 `endpoints`/`startedAt`/`collector` 无锁（driver.go:376,381 ↔ http.go:150,158），CI go1.26 实测 data race（TestHandlerServesDashboardAndMetrics）；且任务 goroutine 在端点池装配前启动，竞窗更宽 Fixed · tools/txload/driver.go+http.go（新增 `runMu` 守卫 Run 初始化字段与 Snapshot//metrics/Metrics/Endpoints 读侧，端点池装配移到任务 spawn 之前）· go test -race ./tools/txload/... ok（14.4s）· e957c73 引入
- [x] 审核发现：`go t.Task(runCtx)`（driver.go:371）返回值未检查，golangci-lint errcheck 报错使 CI lint 连续 6 个 commit 红 Fixed · driver.go:381（goroutine 内 `_ = t.Task(runCtx)` 显式忽略并注释错误走向）· golangci-lint run tools/txload/... 0 issues
- [x] 审核发现：faultmatrix 用例对 `none`/`slow` 故障档用 30ms 超时断言成功（harness_test.go:96），-race 下本机（14 核高负载）稳定超时 FAIL，CI 快机通过、慢机必炸——超时阈值对 race/慢机无余量，应放宽或按 f == slow 提高预算（tests/faultmatrix/harness_test.go:96） — 属实：复现负载下 slow 用例超时；成功用例预算放宽至 1s，仅 timeout 档保留 30ms，`go test -race ./tests/faultmatrix/ -run TestFixedScenariosAcrossFaults -count=30` 与全仓 `make test-race` 通过（tests/faultmatrix/harness_test.go:95，505 测试）
- [x] 审核发现：删除 docs/package.json 顶层 `overrides.vite=6.4.5` 后，`npm install` 会忽略 pnpm-workspace.yaml 的 override 并重新解析出 vite@5.4.21（vitepress ^1.6.0→1.6.4 声明 vite ^5.4.14），重新暴露此前已审核确认并修复的 GHSA-587h-7wmj-h5pm；本仓无 .npmrc/engine-strict，`packageManager: pnpm@12.10.1` 仅提示不强制，npm 路径仍可达（docs/package.json:13） — 属实：实测无 package.json overrides 时 `npm install --package-lock-only` 解析 vite@5.4.21；补回 `overrides.vite=6.4.5` 后 npm 解析 vite@6.4.5，pnpm 12.10.1 `install --frozen-lockfile` 与 `run docs:build` 均通过（docs/package.json:13、docs/pnpm-workspace.yaml:4，506 测试）
- [x] 审核发现：新增 `runMu` 未覆盖 `/healthz` 读侧——其无锁读 `d.adapter`（http.go:95）与 `Run` 无锁写 `d.adapter`（driver.go:357）仍构成 data race；且 HTTP server 在 `Run` 之前启动（main.go:136 vs :145），`Run` 早退（如 evmMod.Start 失败）时 `d.adapter` 为 nil，`d.adapter.Network(d.nid)` 触发 nil 解引用 panic（tools/txload/http.go:95 ↔ driver.go:357） — 属实：`-race` 复现 data race（http.go:95 读 ↔ driver.go:357 写），无效端点 `Run` 早退复现 nil 解引用 panic；修复：`Adapter()`/`Run` 写侧改用 `runMu` 守卫，`/healthz` 经 `Adapter()` 取用并在 nil 时返回 503（tools/txload/driver.go:317、tools/txload/http.go:93），新增 TestHandlerHealthzBeforeRun/DuringRunStart 回归（tools/txload/driver_test.go:178、tools/txload/driver_test.go:219），`go test -race ./tools/txload/...` 通过（506 测试）
- [x] 审核发现：本轮 diff（99dd9ff9..HEAD）未发现新问题；既有开放条目记录的两项问题经复核属实——删除 docs/package.json 顶层 overrides 使 npm 安装忽略 pnpm-workspace.yaml override 并回退 vite@5.4.21（docs/package.json:13），新增 runMu 未覆盖 /healthz 无锁读 `d.adapter` 的竞态与 nil 解引用（tools/txload/http.go:95） — 属实：两项均复现并已修复（见上两条），全仓 `make test-race` 通过（506 测试）

- [x] 审核发现：本轮 diff 未见问题（a5fb3f05..HEAD） — 审核通过，无需改动：复核 a5fb3f05..HEAD 仅含 5bb56bd（healthz adapter 锁守卫 + vite npm override 恢复，新增 2 回归测试），全仓 `go test -race -p 1 -count=1 ./...` 通过（498 顶层测试 / 811 含子测试）。
- [x] 审核发现：本轮 diff 未见问题（5bb56bd0..HEAD）。 — 审核通过，无需改动：git rev-list 5bb56bd0..HEAD 仅含 a5761d2（docs: record audit pass for a5fb3f05..HEAD），该提交仅改动 todo.md 纯文档、无代码/配置变更。

- [x] 审核发现：本轮 diff 未见问题（a5761d29..HEAD）。 — 审核通过，无需改动：git rev-list a5761d29..HEAD 仅含 8972da6（todo: tick clean audit for 5bb56bd0..HEAD），该提交仅改动 todo.md 纯文档、无代码/配置变更；全仓 `go test -race -p 1 -count=1 ./...` 通过（498 顶层测试 / 31 包 ok）。

- [x] 审核发现：本轮 diff 未见问题（8972da6b..HEAD）。 — 审核通过，无需改动：git rev-list 8972da6b..HEAD 仅含 1cef5aa（todo: tick clean audit for a5761d29..HEAD），该提交仅改动 todo.md 纯文档、无代码/配置变更；全仓 `make test-race` 通过。
