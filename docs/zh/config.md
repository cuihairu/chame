# 配置 / API

## 配置加载

配置分层（`core/config`，规格 §5）：代码默认值 → 文件 → 环境变量 → 组合根显式覆盖。
校验在运行时启动前执行，直接拒绝不安全的形状。

```sh
go doc github.com/cuihairu/chame/core/config
```

关键分节：

| 键 | 类型 | 说明 |
|---|---|---|
| `app.name` | string | 应用名，用于日志与健康报告。 |
| `app.environment` | string | `development` / `staging` / `production`。 |
| `app.shutdown_timeout` | duration | 限定运行时停止序列。 |
| `logging.level` | string | 日志级别。 |
| `logging.format` | string | 日志格式（如 `json`）。 |
| `chains.evm[].name` | string | 逻辑 EVM 网络名（跨族唯一）。 |
| `chains.evm[].chain_id` | string | Chain ID 以字符串表达（公共层不依赖整数宽度）。 |
| `chains.evm[].rpc_urls` | string[] | 端点；跨端点切换。 |
| `chains.evm[].timeout` | duration | 单请求超时。 |
| `chains.evm[].read_only` | bool | 首期仅只读 EVM。 |
| `chains.solana[].name` | string | 逻辑 Solana 集群名。 |
| `chains.solana[].cluster` | string | 集群标识。 |
| `chains.solana[].rpc_urls` | string[] | 端点；逐端点 Genesis Hash 校验。 |
| `chains.solana[].commitment` | string | `processed` / `confirmed` / `finalized`。 |
| `chains.solana[].timeout` | duration | 单请求超时。 |
| `chains.solana[].read_only` | bool | 首期仅只读 Solana。 |
| `transactions.enabled` | bool | 交易特性门；阶段落地前校验拒绝。 |
| `events.enabled` | bool | 事件管线特性门。 |

## 入口

- `cmd/chame` — 二进制。
- `core/runtime` — 生命周期容器（模块注册、依赖图、优雅关闭）。
- `core/module` — 模块接口与注册表。
- `core/identifiers` — 链无关身份模型（`NetworkID`、`Address`、`Amount`、`AssetID`）。
- `capability/*` — 能力契约（transaction、event、signer、account、asset、nft、finality、network）。
- `adapters/evm`、`adapters/solana` — 只读适配器。
- `application/*` — 用例（transaction、eventing、idempotency、gameledger、projection）。
- `infrastructure/persistence/*` — 内存与 PostgreSQL 存储。
- `infrastructure/signer/remote` — 远端签名 HTTP 客户端（`infrastructure/signer/local` 为骨架占位；开发签名器 `LocalDevSigner` 在 `adapters/evm` 与 `adapters/solana`）。
- `infrastructure/observability/*` — 指标与成本记录。

完整契约集见[规格](https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md)。
