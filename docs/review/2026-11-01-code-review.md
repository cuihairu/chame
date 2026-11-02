# 代码深审报告（2026-11-01）

按请求代码审查流程对本仓全量执行。范围：全部 Go 源码（`capability/`、`adapters/`、`application/`、`infrastructure/`、`core/`、`examples/`、`tests/`），逐文件人工读取 + 静态扫描。方法：① 安全静态扫描（硬编码密钥 / shell 注入 / SQL 拼接 / eval / 反序列化 / 路径穿越）；② 隐藏 bug 猎捕（错误处理缺失 / 竞态 / 越界 / 资源泄漏 / off-by-one / 并发问题）；③ 质量门（lint / 类型检查 / 测试基线）；④ 结论按严重度排序，每条附 file:line 证据。

## 门禁（质量门）

| 门 | 结果 | 证据 |
|---|---|---|
| `go build ./...` | ✓ | 退出码 0 |
| `go vet ./...` | ✓ | 退出码 0，无诊断 |
| `go test ./...` | ✓ 全绿 | 29 个包含测试的包 `ok`，482 个测试/fuzz 函数；SQL 部分按 `CHAME_TEST_PG_DSN` 门控 skip |
| `go test -race -p 1 ./...` | ✓ | 无 race 报告、无 FAIL、无 panic |
| `golangci-lint run` | ✓ 0 问题 | 全仓静态检查无告警 |
| 文档站 `npm run docs:build` | ✓ | 独立于本审，7.09s，产物含 logo.jpg |

## 安全静态扫描结论

- **硬编码密钥**：未发现。`grep -niE "password|secret|apikey|api_key|token"` 命中的 `const` 值仅错误消息与测试夹具，无真实凭据常量。
- **shell 注入 / os/exec**：无 `os/exec`、无 `exec.Command` 调用（全仓 grep 为空）——无命令拼接面。
- **SQL 拼接**：`infrastructure/persistence/sql/store.go` 全部使用参数化 `$1..$n`（`Create`/`Attach`/`Claim`/`FindStale`/`UpdateStatus`），无 `fmt.Sprintf` 拼接 SQL。唯一动态片段为 `FindStale` 的状态列表经 `string_to_array($1, ',')` 参数传递（store.go:289-292），且 `StaleQuery.Validate` 先行拒绝未知状态与空列表（capability/transaction/repository.go:68-71），无注入面。
- **eval / unsafe / reflect**：无 `unsafe` 实际使用（`unsafe` 字样仅出现在注释，如 adapters/evm/signer.go:162 描述租约语义）；无 `reflect` 驱动的反序列化入口。
- **路径穿越**：无基于外部输入的文件路径拼接（无 `filepath.Join` + 用户输入模式；无 `os.ReadFile` 外部路径）。
- **反序列化**：`chain_data` 经 `encoding/json` 编解码（store.go:396-406），无 `gob`/`yaml` 外部输入反序列化。

结论：安全静态扫描无 P0/P1。

## 隐藏 bug 猎捕结论

逐文件读取覆盖：`adapters/evm/{nonce,flow,pool,signer,client,module,asset}.go`、`adapters/solana/{blockhash,flow,signer,pool,client,module,asset}.go`、`application/transaction/{flow,worker,control}.go`、`application/eventing/dispatcher.go`、`application/gameledger/contract.go`、`infrastructure/persistence/sql/{store,events}.go`、`infrastructure/signer/remote/remote.go`、`core/{runtime,config,errors,logging,health,metrics}`。

发现的 bug 类（错误处理缺失 / 竞态 / 越界 / 资源泄漏 / off-by-one / 并发）：**共 4 条**——P2×2、P3×2，无 P0/P1。资源泄漏面（`rows.Close`/`resp.Body.Close`/`db.Close`）全部经 `defer` 处理；CAS 状态推进（`UpdateStatus`/`Attach`/`Claim`）经 `RowsAffected` 校验，无丢失写；ledger 状态秩单调（`observationRank` 0/1/2），`SetState` 拒绝回退与重组记录复活；恢复 worker 租约、nonce 协调器租约、事件派发最终性门控均正确。

## 发现清单（按严重度排序）

| ID | 严重度 | 位置 (file:line) | 发现 | 处置 |
|---|---|---|---|---|
| R1 | P2 | adapters/evm/nonce.go:181、:245 | `Reconcile` 刷新 `chainNext` 但不同步 `s.next`。链上外部写入者消耗非推进 `chainNext` 后，`take()` 仍从旧的 `s.next` 分发（:245 `n := s.next`），给出已被链消费的陈旧 nonce，直至进程重启（重启重新播种才对齐）。单写入者模式（本仓支持的模式）下不可达；仅在已被归类为 `Foreign`（外部写入者，需操作员处理）的路径上显现。 | 记 todo（P2）：Reconcile 后令 `s.next = max(s.next, s.chainNext)`，并把低于 `chainNext` 的空闲/租约按 Foreign 归类（分类已有）。 |
| R2 | P2 | adapters/solana/blockhash.go:37-42 | `BlockhashPin.Valid` 采用**开区间**边界（`height < LastValidBlockHeight` 有效，`>=` 过期；注释 :36-37 明示「有效至 LastValidBlockHeight 且不含该高度」）。外部 Solana 文档对该边界口径分裂：OnFinality「网络到达该高度即过期」（开区间，与代码一致）vs GetBlock/Triport/Helius「最后有效高度」（闭区间）。代码内部自洽且明示开区间意图，故不改码；但边界语义为外部歧义，需对 Solana 运行时 `check_hash_age` 实证后再信赖。 | 记 todo（P2）：对 Solana 运行时 `check_hash_age` 实证边界含/斥；实证后按结果统一注释与验证测试。 |
| R3 | P3 | infrastructure/persistence/sql/store.go:86-125 | `Migrate`/`applyMigration` 非跨实例并发安全：两个副本同时首次启动，均 `SELECT EXISTS` 见未应用，随后均 `INSERT INTO chame_schema_migrations`，后者在 version 主键冲突处报错、启动失败。自愈（重启时已应用，`SELECT EXISTS` 短路）；仅影响多副本同时首启。Store 注释自称「跨实例经数据库协调」，值得加 `pg_advisory_xact_lock`。 | 记 todo（P3）：applyMigration 内套 `pg_advisory_xact_lock`，令并发首启不冲突。 |
| R4 | P3 | adapters/evm/flow.go:274、:276 | `Network.Settle` 以 `_ =` 吞掉 `NonceCoordinator.Submitted`/`Abandon` 的错误。当前唯一可达错误为 `ErrUnknownNonce`，且注释 :266-269 明示该情形「容忍：结算为簿记而非前置条件」——功能安全；但 `_ =` 吞掉未来新增错误类别属代码质量隐患。 | 记 todo（P3）：改为 `errors.Is(err, ErrUnknownNonce)` 精确容忍，其余错误透传（或至少记日志）。 |

## 观察（不列为缺陷，已确认设计使然）

- **application/gameledger/contract.go**：参考版 Ledger 用裸 map 无锁（并发不安全），但包文档明示「生产宿主应实现等价的事务性唯一约束」，属宿主替换责任，非缺陷。
- **adapters/evm/flow.go:266-277** 与 **adapters/solana/flow.go:131**：Settle 无返回值（`capability/transaction/flow.go:77` 契约即无错误通道），簿记失败不阻塞记录推进，符合「结算为簿记」语义。

## 处置汇总

- **发现总数**：4（P2×2、P3×2；P0×0、P1×0）。
- **当场修复**：0（无 P0/P1）。**未改任何生产代码**。
- **记入 todo.md**：R1–R4（P2×2、P3×2）。
- **报告**：本文件。

## 门禁复述

`go build ./...` ✓、`go vet ./...` ✓、`go test ./...` 全绿 ✓、`-race -p 1` 全绿 ✓、golangci-lint 0 问题 ✓。本次审查零生产代码改动，门禁结论即基线。
