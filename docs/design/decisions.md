# Chame 架构决策（规格 §23 汇总）

本文件汇总规格 §23「需要在实施前确认的架构决策」的结论，供快速审阅。逐条详细理由、备选方案与测试要求见 [docs/decisions/](../decisions/) 的对应 ADR。

**代决说明**：用户已授权代理决策（见 todo.md），下列每条均为**已代决**，用户可随时推翻；推翻时修订对应 ADR 并在本表更新状态。

| # | 决策项 | 结论 | 状态 | ADR |
|---|---|---|---|---|
| 1 | RPC SDK 选型 | EVM 用 go-ethereum（ethclient + accounts/abi，LGPL-3.0 隔离在 adapters/evm）；Solana 用 solana-go（已移交 Solana Foundation 维护，Apache-2.0） | 已代决 | [ADR-0001](../decisions/0001-rpc-sdk-choice.md) |
| 2 | 持久化实现 | 首个生产实现 PostgreSQL（database/sql + pgx stdlib 驱动，显式迁移 SQL，无 ORM）；memory 实现仅限测试 | 已代决 | [ADR-0002](../decisions/0002-persistence-postgres.md) |
| 3 | Solana 交易能力范围 | 首期只读先行，交易构建/签名随交易阶段交付 | 已代决 | [ADR-0003](../decisions/0003-solana-tx-scope.md) |
| 4 | EVM Nonce 协调范围 | 首期单实例每账户串行分配；多实例共享账户并发写明确不支持（文档标注使用限制） | 已代决 | [ADR-0004](../decisions/0004-evm-nonce-scope.md) |
| 5 | 事件订阅策略 | 轮询优先（游标 + 重叠窗口回补 + 唯一标识去重）；WebSocket 为后续优化项 | 已代决 | [ADR-0005](../decisions/0005-event-subscription.md) |
| 6 | 最终性策略 | 每网络可配置；EVM 默认 finalized/safe 标签、无标签时固定深度（mainnet 类默认 12）；Solana 默认 finalized commitment；不支持组合显式报错 | 已代决 | [ADR-0006](../decisions/0006-finality-policy.md) |
| 7 | 可观测性实现 | 抽象先交付（log/slog 门面 + 窄指标接口 + core/health 模型）；官方 Prometheus 导出器延至生产基础设施阶段 | 已代决 | [ADR-0007](../decisions/0007-observability.md) |
| 8 | 公开 API 稳定范围 | 首期承诺兼容：根包 + core/* 公共接口 + capability/* 契约；application/、adapters/、infrastructure/ 为内部实现不承诺兼容；tag 语义化版本仅由用户触发 | 已代决 | [ADR-0008](../decisions/0008-public-api-scope.md) |

## 补充决策（阶段一实施中记录）

- **License**：Apache-2.0（与本组织多数仓库一致，2026-10-15 落地）。
- **Go 工具链**：go.mod 声明 `go 1.26`（本机 1.27.1），CI 矩阵 1.26.x + 1.27.x。
- **模块依赖装配**：显式装配优先，不做反射式 DI 容器（规格 §4.4 已定，此处落实：模块经构造函数拿依赖，runtime 只管生命周期与图校验）。
- **依赖引入**：golang.org/x/crypto（仅 sha3，EIP-55 校验）与 gopkg.in/yaml.v3（仅配置文件层）；二者均非链 SDK，core 依旧不导入任何链 SDK。
