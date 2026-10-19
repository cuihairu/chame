# 游戏链上资产确权与账本边界

状态：定稿（框架契约，宿主账本实现仍由游戏负责）

## 决策

**链是外部资产事实的权威来源，游戏账本是游戏内可用权/物品的权威来源；只有满足游戏配置的 finality 门槛且事件仍在 canonical chain 上，链事件才可作为账本入账依据。** 查询到的余额不是游戏账本，也不自动赋予某个玩家兑换、消费或转移权。`AssetProvider` 只提供资产/余额查询，SPL holder 参数表示 token account，见 [`capability/asset/asset.go:65`](../../capability/asset/asset.go#L65)；事件 dispatcher 按配置门槛延迟投递，重组失效交由业务处理，见 [`application/eventing/dispatcher.go:131`](../../application/eventing/dispatcher.go#L131) 与 [`application/eventing/dispatcher.go:221`](../../application/eventing/dispatcher.go#L221)。

## 规范资产身份

宿主必须以 `(network.family, network.name, network.chain_id, standard, contract_or_mint, token_id?)` 作为资产键，禁止仅用符号、名称、地址或 token ID。标准保留差异：`native`、`erc20`、`erc721`、`erc1155`、`spl-token`、`spl-nft` 是不同身份空间。Native 不含合约；ERC 标准只接受 EVM 网络与合约地址；SPL 标准只接受 Solana 网络与 mint；ERC-721/1155 与 SPL NFT 必须带 token ID；fungible 标准不得带 token ID。EVM 地址规范为小写 `0x` 地址，token ID 规范为无前导零的十进制字符串；Solana base58 保持大小写原样。未知标准拒绝，不推断兼容。

可执行规范在 [`application/gameledger/contract.go:16`](../../application/gameledger/contract.go#L16)，标准不得抹平的断言在 [`application/gameledger/contract_test.go:72`](../../application/gameledger/contract_test.go#L72)。既有 `identifiers.AssetID` 是 fungible 查询标识，不可当成 NFT token-id identity，见 [`core/identifiers/asset.go:23`](../../core/identifiers/asset.go#L23)。

## 权威、可见性与写入规则

| 情形 | 权威和游戏账本规则 |
|---|---|
| 玩家当前链上持有人/余额 | 节点或可信索引给出链观察；SPL 的 holder 是 token account，不等于游戏玩家。宿主必须显式维护钱包到玩家的绑定，并验证其归属流程。 |
| 游戏可用余额/背包 | 宿主游戏账本是唯一权威。只有其业务流水成功提交后才可消费；不得每次从链上余额覆盖游戏账本。 |
| Finality 前链事件 | 可以作为 `pending` 记录和查询展示，但必须不可花、不可兑换、不可触发不可逆奖励。达到宿主选定的 `confirmed` 或 `final` 门槛且未 reorg 后，宿主可在一个数据库事务中将状态和账本入账一起提交。 |
| Finality / reorg | `reorged` 事件不得继续提供可用权。若已产生可逆入账，宿主以原事件关联的唯一补偿流水冲销；若奖励已消费或已产生外部副作用，进入宿主定义的人工/业务争议流程，不得伪装为链数据库回滚。 |
| 事件重复 / 乱序 | 事件主键必须来自稳定链事件身份（例如 network + tx hash/signature + log/instruction index）；唯一约束与账本写入处于同一数据库事务。重复键同 payload 返回 existing/no-op，异 payload 拒绝。旧状态不得覆盖新状态；reorg 补偿也必须唯一。 |

应用请求幂等仍沿用 `application/idempotency` 的 `(scope,key,request hash)` 及仓储 `CreateInserted` / `CreateSameKeyExisting` 原子胜者语义，见 [`application/idempotency/service.go:38`](../../application/idempotency/service.go#L38)、[`capability/transaction/repository.go:184`](../../capability/transaction/repository.go#L184) 和第 1 条成果。链事件幂等键与玩家请求幂等键是不同命名空间，不可互相替代。可执行契约用 `Apply` 对重复事件只插入一次、以 `SetState` 做一次补偿，见 [`application/gameledger/contract.go:140`](../../application/gameledger/contract.go#L140) 与 [`application/gameledger/contract_test.go:17`](../../application/gameledger/contract_test.go#L17)。

## 范围与明确不做

本仓当前仍是 native/ERC-20/SPL fungible 查询面；本设计不声称已经提供链上 NFT 所有权索引，也不新增生产游戏账本、玩家/钱包绑定、市场、掉落、客服对账或争议工作流。参考包是最小可验证契约，不是持久化实现；宿主须实现事务性唯一约束、重放/重建、授权和业务补偿。**不实现托管钱包，不运营生产密钥**，生产 HSM/KMS、轮换、审批与资金控制由宿主另行负责，范围依据分析缺失项第 5 条 [`chain-game-foundation-2026-10-19.md:57`](../analysis/chain-game-foundation-2026-10-19.md#L57)。

任何数据外发、分析导出或云备份默认关闭；宿主显式启用前不得将玩家、钱包或账本数据发送至外部服务。

## 可证伪验收

- `TestDuplicateTransferEventIsAppliedExactlyOnce`：同一事件重投只产生一个条目。
- `TestPendingEntryIsVisibleButNotSpendable`：pending 可查但不可花，确认后才可用。
- `TestReorgCompensatesOnce`：reorg 冲销恰好一次且条目不可花。
- `TestAssetIdentityNormalizationPreservesStandardDifferences`：规范化地址/token ID，同时保留 ERC-721/ERC-1155/SPL 标准差异。

这些测试验证边界契约，不证明宿主完整账本或生产链数据正确。
