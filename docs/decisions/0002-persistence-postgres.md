# ADR-0002: 首个持久化实现选 PostgreSQL

- 状态：已接受（代决，用户可推翻）
- 日期：2026-10-15
- 对应规格：§10、§23 第 2 项

## 背景

交易记录、幂等键、事件游标需要持久化（§10.2）。规格要求决定首个官方 SQL 实现的数据库，并明确是否复用游戏项目现有数据库。

## 决策

首个生产实现为 **PostgreSQL**，经 `database/sql` 接口 + `pgx` 的 stdlib 驱动接入。Schema 用显式迁移 SQL 管理，不使用 ORM。内存实现（`infrastructure/persistence/memory`）仅服务单元测试与示例，生产路径禁止依赖（§19 Phase 5 验收）。

## 理由

- 游戏后端 PostgreSQL 是主流默认选择，事务与约束语义完整，适合幂等 upsert、交易状态机乐观并发控制。
- `pgx` 是 Go 生态最成熟的 Postgres 驱动，stdlib 模式可与 `database/sql` 生态互操作。
- 幂等键唯一性、恢复读取等语义需要显式 SQL 控制，ORM 会隐藏锁与 upsert 语义。
- 复用游戏现有数据库：持久化契约（Store 接口）不绑定具体数据库；官方 SQL 实现的方言以 PostgreSQL 为准，游戏侧若用既有 Postgres 实例可直接复用同一实现。

## 备选方案

- **MySQL**：无决定性优势，社区对 Go Postgres 栈的默认倾向更强；后续可按同一接口补实现。
- **SQLite**：运维简单，但交易台账的并发写与多实例部署场景弱；可作为嵌入式场景的后续补充。
- **ent / gorm**：否，理由见上。

## 影响范围

- `infrastructure/persistence/sql` 依赖 `pgx`；该依赖不得进入 `core/`、`capability/`。
- 迁移 SQL 存放于 `infrastructure/persistence/sql` 内，版本化提交。

## 测试要求

- SQL 实现用真实 PostgreSQL 的集成测试（CI service 容器），标记为集成测试，不混入单元测试（§20）。
- `tests/conformance/` 覆盖 memory 与 sql 实现语义一致：幂等键唯一性、状态机读取、游标续传。
