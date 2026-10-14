# Chame

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26%2B-00ADD8.svg)](https://go.dev)
[![CI](https://github.com/cuihairu/chame/actions/workflows/ci.yml/badge.svg)](https://github.com/cuihairu/chame/actions/workflows/ci.yml)

> [English](README.md) | **中文**

**Chame** 是一个把区块链抽象在统一运行时之后的 **Go 游戏后端框架**。游戏侧只面对
一套 API，EVM 与 Solana 都是其后的适配器。

## 为什么做

接一条链要一套 SDK、一套密钥方案、一套事件模型。加第二条链通常意味着重写对接层。
Chame 把链收进适配器，游戏侧保持同一份契约。

## 范围（来自设计稿）

| 领域 | 内容 |
|---|---|
| Runtime | 生命周期、调度、恢复 |
| 适配器 | EVM、Solana |
| 交易模型 | 跨链统一状态机 |
| 可靠性 | 幂等键、持久化、恢复 Worker |
| 事件 | 游标式断点续传、链重组处理 |
| 安全 | 可插拔签名器、权限边界、本地开发密钥 |
| Provider | `AccountProvider`、`AssetProvider` |
| 运维 | 错误分类、日志、指标、测试、CI |

## 当前状态

**阶段一已落地。** 完整规格见
[`docs/design/chame-spec.md`](docs/design/chame-spec.md)——共 24 章，覆盖架构、
适配器、交易状态机、幂等、事件处理、签名、错误模型、可观测性、测试、CI、
分期实施与完成定义。

已交付：Runtime 生命周期容器（模块注册、依赖图校验、优雅关闭）、分层配置
系统、链无关身份模型（`NetworkID`、`Address`、`Amount`、`AssetID`）。架构
决策汇总在 [`docs/design/decisions.md`](docs/design/decisions.md)，详细 ADR 在
[`docs/decisions/`](docs/decisions/)。分期实施见 [`todo.md`](todo.md)——
下一步：交易公共模型与状态机。

## 开发

需要 Go 1.26+。

```sh
make ci        # gofmt 检查 + vet + 构建 + 测试
make test-race
make lint      # golangci-lint
```

CI 在 Go 1.26.x 与 1.27.x 上执行同样检查，另含 golangci-lint 与依赖漏洞扫描。

## 许可证

[Apache-2.0](LICENSE)。
