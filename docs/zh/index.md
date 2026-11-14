---
layout: home

hero:
  name: Chame
  text: 把区块链抽象在统一运行时之后的 Go 游戏后端框架
  tagline: 游戏侧只面对一套 API，EVM 与 Solana 都是其后的适配器
  actions:
    - theme: brand
      text: 快速开始
      link: /zh/quickstart
    - theme: alt
      text: 规格（26 章）
      link: https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md

features:
  - title: 一份契约
    details: 游戏侧保持单一链无关契约；各链的 SDK 与密钥方案收进适配器。
  - title: 交易模型
    details: 跨链统一状态机，unknown/reorged 显式表达不确定性，终态冻结。
  - title: 可靠性
    details: 幂等键、持久化、租约恢复 Worker、游标式断点续传与重组处理。
  - title: 安全
    details: 可插拔签名器、allow-list 授权、载荷摘要审计、脱敏密钥与日志、生产环境守卫。
---

![Chame 游戏后端框架架构图](/images/architecture-zh.svg)

## 这是什么

**Chame** 是一个把区块链抽象在统一运行时之后的 **Go 游戏后端框架**。游戏侧只面对
一套 API，EVM 与 Solana 都是其后的适配器。接一条链要一套 SDK、一套密钥方案、
一套事件模型；加第二条链通常意味着重写对接层。Chame 把链收进适配器，游戏侧保持
同一份契约。

完整规格见 [`docs/design/chame-spec.md`](https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md)——共 26 章，覆盖架构、
适配器、交易状态机、幂等、事件处理、签名、错误模型、可观测性、测试、CI、分期实施
与完成定义。架构决策汇总在 [`docs/design/decisions.md`](https://github.com/cuihairu/chame/blob/main/docs/design/decisions.md)，详细 ADR
在 [`docs/decisions/`](https://github.com/cuihairu/chame/tree/main/docs/decisions)。

## 开发

需要 Go 1.26+。

```sh
make ci        # gofmt 检查 + vet + 构建 + 测试
make test-race
make lint      # golangci-lint
```

CI 在 Go 1.26.x 与 1.27.x 上执行同样检查，另含 golangci-lint 与依赖漏洞扫描。
