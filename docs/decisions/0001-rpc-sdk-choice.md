# ADR-0001: RPC SDK 选型（EVM / Solana）

- 状态：已接受（代决，用户可推翻）
- 日期：2026-10-15
- 对应规格：§7、§8、§23 第 1 项

## 背景

适配器需要 RPC 客户端。规格 §23 要求按维护活跃度、上下文取消、WebSocket 订阅、交易支持和许可证比较后决定，不得凭空假定。

## 决策

- **EVM**：`github.com/ethereum/go-ethereum`（`ethclient` + `accounts/abi`）。
- **Solana**：`solana-go`（原 `github.com/gagliardetto/solana-go`，2026 年已移交 Solana Foundation 维护，仓库 `github.com/solana-foundation/solana-go`，v1.22+ 持续发版）。集成时以 `go get` 实际解析到的模块路径为准。—— 已解析（2026-10-15，阶段四）：`solana-foundation` 路径不被该模块认领（其 `go.mod` 声明自身为 `github.com/gagliardetto/solana-go`），实落 **`github.com/gagliardetto/solana-go` v1.25.0**，见 ADR-0003 集成落地。

## 理由（按 §23 比较维度）

| 维度 | go-ethereum | solana-go |
|---|---|---|
| 维护活跃度 | 以太坊官方执行客户端，长期活跃（2026-02 官方文档仍明确支持作为库引用） | 2026-10 模块文档仍在更新，由基金会接管后维护前景更稳 |
| 上下文取消 | `ethclient`/`rpc` 全程接受 `context.Context` | RPC 调用接受 `context.Context` |
| WebSocket 订阅 | 内置 `rpc` WS 客户端与订阅 | 提供 WS 客户端与订阅 |
| 交易支持 | 签名、EIP-155、ABI 编解码完备 | 交易构建、消息序列化、SPL 支持完备 |
| 许可证 | 库代码 LGPL-3.0（`cmd/` 之外） | Apache-2.0 |

## 备选方案

- **自研 JSON-RPC 客户端**：否。ABI 编解码、EIP-55 校验、地址/金额类型、重连细节成本过高，且属重复造轮子。
- **其他社区 EVM/Solana Go 库**：否。成熟度和维护强度均不及上述两者，Solana 生态无同级替代。

## 影响范围

- 仅 `adapters/evm` 允许导入 go-ethereum，仅 `adapters/solana` 允许导入 solana-go；`core/`、`capability/` 永不导入链 SDK（规格 §2.2 已有规则，本决策不放宽）。
- 本项目许可证为 Apache-2.0；作为下游使用 LGPL-3.0 库（动态链接语义的依赖）可行。go-ethereum 仅被 `adapters/evm` 引用，隔离边界清晰。
- SDK 类型不得泄漏出适配器包：适配器对外只暴露 capability 契约类型。

## 测试要求

- 单元测试不访问真实公共 RPC（规格 §19 Phase 3 验收）。
- `tests/conformance/` 约束两个适配器对同一 capability 契约行为一致。
- Solana SDK 集成时验证模块路径与许可证字段，写入该阶段报告。

## 集成落地（2026-10-15，阶段三）

- EVM 集成版本 `github.com/ethereum/go-ethereum v1.17.8`，`go mod tidy` 解析的传递依赖已入 `go.sum`。
- 隔离边界已验证：`go list` 下仅 `adapters/evm` 直接导入该 SDK；`core/`、`capability/` 不导入。
- SDK 类型未泄漏：`adapters/evm` 对外只暴露 `BlockInfo`、`LogRecord`、`CallQuery`、`LogFilter`、`NetworkStats` 等链无关记录类型，地址与数量走 `core/identifiers`。
- 单元与集成测试均通过可注入的 `Dialer`/`rpcClient` seam 使用假客户端，不访问真实公共 RPC（规格 §19 阶段三验收）。
