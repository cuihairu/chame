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

## 宿主侧调优（Go 代码面，不是配置键）

下列调优项由组合根以 Go 代码设置，不进 YAML 配置文件。零值取内置默认。

| 项 | 位置 | 默认 | 说明 |
|---|---|---|---|
| `LogSourceConfig.Depth` | `adapters/evm/eventsource.go:45` | `1`（`adapters/evm/eventsource.go:24`） | 确认窗口：只轮询深度至少如此的区块。 |
| `LogSourceConfig.FinalityDepth` | `adapters/evm/eventsource.go:45` | `12`（`adapters/evm/eventsource.go:26`） | 事件达到最终的区块深度（ADR-0006）。 |
| `LogSourceConfig.FirstWindow` | `adapters/evm/eventsource.go:45` | `1000`（`adapters/evm/eventsource.go:31`） | 限制首次（零高度游标）轮询扫描的区块范围——实时订阅，非历史回填。 |
| `SignatureSourceConfig.Commitment` | `adapters/solana/eventsource.go:30` | 网络已配置的 commitment（`finalized`，ADR-0006） | 零值取网络默认。 |
| `SignatureSourceConfig.Limit` | `adapters/solana/eventsource.go:30` | `1000`（`adapters/solana/eventsource.go:26`） | 签名历史分页大小。RPC 拒绝字面 `0`，因此非正值省略该参数、用节点默认分页。 |
| `PoolConfig.MaxOpen` / `MaxIdle` / `ConnMaxLifetime` | `infrastructure/persistence/sql/store.go:43` | `16` / 与 `MaxOpen` 一致 / 未设置（`infrastructure/persistence/sql/store.go:39`） | PostgreSQL 存储的 database/sql 连接池；显式负的 `MaxIdle` 关闭空闲保持。 |

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
