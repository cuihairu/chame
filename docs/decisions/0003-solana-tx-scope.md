# ADR-0003: Solana 交易能力首期只读先行

- 状态：已接受（代决，用户可推翻）
- 日期：2026-10-15
- 对应规格：§8、§23 第 3 项

## 背景

§23 要求决定 Solana 首期交付只读，还是同时交付交易构建与签名。

## 决策

**只读先行**（与规格 §19 Phase 4 一致）。交易构建、签名、提交与 Blockhash 过期恢复推迟到交易能力阶段（§19 Phase 5）统一交付。

## 理由

- Solana 写路径的正确性依赖 Blockhash 生命周期管理（§8.4）：过期后盲目重发会造成重复业务交易。这类恢复语义需要幂等键与持久化配套（Phase 5），提前暴露半成品 API 违反 §20「不允许空实现伪装完成」。
- 只读能力（账户、余额、SPL Token、日志/交易查询）已覆盖接入验证与资产查询场景，可独立验收。

## 备选方案

- **同时交付交易构建与签名**：否。写路径缺持久化与恢复配套时不可靠，验收标准无法满足。

## 影响范围

- 首期 Solana 适配器能力声明不含交易能力；调用交易能力收到明确的"不支持"错误（§8 验收）。
- 只读场景不要求配置写入权限与签名器（§19 Phase 4 验收）。

## 测试要求

- 不支持的能力返回显式错误的契约测试。
- 不同 Commitment 处理的测试（§19 Phase 4 验收）。
- Blockhash 过期恢复策略在 Phase 5 以故障注入测试覆盖（模拟提交结果未知）。

## 集成落地（2026-10-15，阶段四）

- SDK 模块路径：ADR-0001 列出的 `github.com/solana-foundation/solana-go` 不被该模块认领（其 `go.mod` 声明自身为 `github.com/gagliardetto/solana-go`），集成时按 ADR-0001 的既定口径落为 `github.com/gagliardetto/solana-go` v1.25.0（Apache-2.0）。两处路径指向同一上游，版本线一致（均到 v1.25.0）。
- 隔离验证：`go list -deps` 确认 `adapters/evm` 不依赖 solana-go、`adapters/solana` 不依赖 go-ethereum、`core/` 不依赖任何适配器；`go doc` 确认导出面无 SDK 类型。
- 可测试性：SDK 客户端藏在未导出的 `rpcClient` seam 之后，`Dialer` 注入假客户端，单元测试不触真实 RPC（§16）。
- 不支持的写入能力通过 `Adapter.UnsupportedCapability` 返回 §14.1 `UnsupportedCapability` 分类错误（显式契约，非空实现伪装）。
