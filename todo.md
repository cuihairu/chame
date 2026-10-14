# Chame TODO

规格来源：[docs/design/chame-spec.md](docs/design/chame-spec.md)（2026-10-14 整理自外部设计稿）。
每条验收以**实际证据**（commit hash / 测试数 / 可运行产物）为准，不接受"做完了"。

## 仓库事务

- [x] License 选择与文件落地（README 徽章按实际 LICENSE 挂）—— Apache-2.0（用户仓库惯例），LICENSE 落地，双语 README 徽章已挂
- [x] 语言/工具链确定（Go 版本、模块路径、CI 矩阵）—— Go 1.26 起（本机 1.27.1），模块 `github.com/cuihairu/chame`，CI 矩阵 1.26.x + 1.27.x（`.github/workflows/ci.yml`）
- [x] 目录骨架按规格 §3 建立（先空壳 + 编译通过）—— 核心包 doc.go 空壳已建；`examples/ templates/ tools/ adapters/*/internal/` 待有内容再建（git 不跟踪空目录，规格 §3.1 允许）

## 待用户确认的架构决策（规格 §23）

- [x] 逐条确认 §23 列出的架构决策项 —— **用户已授权代决，逐条给出推荐方案并标注**：见 [docs/decisions/](docs/decisions/) ADR-0001 ~ 0008，均标「代决，用户可推翻」

## 分期（规格 §19）

- [x] 阶段一：Runtime + 配置系统 + 公共能力层（§2 §4 §5 §6）—— runtime 生命周期状态机/依赖图/优雅关闭、module Registry、config 分层加载与 §5.4 校验、identifiers（NetworkID/Address/Amount/AssetID）、health/logging/clock、capability 声明检测、examples/minimal；51 个测试函数全绿（含 -race）
- [x] 阶段二：交易公共模型与状态机（§9）—— `capability/transaction`：OperationID/TransactionID、12 状态枚举（created→…→finalized/reverted/failed + reorged/unknown 表达不确定性）、合法转换矩阵（终态冻结、超时进 unknown 不进 failed、reorged 仅可回 unknown）、Transaction 记录（Network 钉死、IdempotencyKey/RequestHash、ChainData 归适配器）、TransitionTo 写时复制；57 个测试函数全绿（含 -race）
- [ ] 阶段三：EVM Adapter（§7）
- [ ] 阶段四：Solana Adapter（§8）
- [ ] 阶段五：幂等 / 持久化 / 恢复（§10）
- [ ] 阶段六：链上事件处理（§11）
- [ ] 阶段七：Signer 与密钥安全（§12）
- [ ] 阶段八：AccountProvider / AssetProvider（§13）
- [ ] 阶段九：错误体系 + 可观测性（§14 §15）
- [ ] 阶段十：测试体系 + CI（§16 §20）
- [ ] 首期完成定义逐条核对（§22）—— 五组验收全绿才谈发布

## 红线

- 发布正式版本必须由**用户明确给出版本号并触发**
- 单 commit 英文简短，rebase 后推，不打 tag，不 force push
- 文档不得出现 AI 设计 / ui ux pro max / 舰队 等元叙事字样
