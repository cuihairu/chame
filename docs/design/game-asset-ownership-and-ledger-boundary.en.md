# On-chain Asset Ownership and Game Ledger Boundary

Status: finalized framework contract; the host game still owns its ledger implementation.

## Decision

**The chain is authoritative for external asset facts; the game ledger is authoritative for in-game spendable rights and inventory. A chain event may support a ledger entry only after the configured finality threshold is met and the event remains on the canonical chain.** A queried balance is not a game ledger and does not itself grant a player redemption, spending, or transfer rights. `AssetProvider` only queries assets and balances; for SPL, the holder is a token account ([`capability/asset/asset.go:65`](../../capability/asset/asset.go#L65)). The event dispatcher gates delivery by configured finality and leaves business reorg handling to the consumer ([`application/eventing/dispatcher.go:131`](../../application/eventing/dispatcher.go#L131), [`application/eventing/dispatcher.go:221`](../../application/eventing/dispatcher.go#L221)).

## Canonical asset identity

Hosts must key assets by `(network.family, network.name, network.chain_id, standard, contract_or_mint, token_id?)`; symbols, names, addresses, or token IDs alone are insufficient. Preserve standard namespaces: `native`, `erc20`, `erc721`, `erc1155`, `spl-token`, and `spl-nft` are distinct. Native assets have no contract; ERC standards require an EVM network and contract; SPL standards require a Solana network and mint; ERC-721/1155 and SPL NFT require a token ID; fungible standards forbid one. Reject unknown standards. Canonicalize EVM addresses to lowercase `0x` form and token IDs to unsigned decimal without leading zeroes. Preserve Solana base58 case.

The executable contract is in [`application/gameledger/contract.go:16`](../../application/gameledger/contract.go#L16), with standard-preservation assertions in [`application/gameledger/contract_test.go:72`](../../application/gameledger/contract_test.go#L72). Existing `identifiers.AssetID` locates fungible query assets and is not an NFT token-ID identity ([`core/identifiers/asset.go:23`](../../core/identifiers/asset.go#L23)).

## Authority, visibility, and writes

| Case | Authority and game-ledger rule |
|---|---|
| Current on-chain holder/balance | A node or trusted index supplies chain observations. An SPL holder is a token account, not a game player. The host explicitly maps wallets to players and verifies that binding. |
| Spendable balance/inventory | The host game ledger is authoritative. Rights are spendable only after its business entry commits; chain balances must not overwrite it. |
| Before finality | The host may expose a `pending` record for queries, but it is not spendable, redeemable, or eligible for irreversible rewards. After the configured `confirmed` or `final` threshold, and absent a reorg, the host may atomically commit state and ledger entry in one database transaction. |
| Finality/reorg | A `reorged` event grants no spendable rights. Reverse prior reversible credits with one compensation entry linked to the original event. If rewards were consumed or caused external effects, enter a host-defined dispute process; a chain reorg is not a game database rollback. |
| Duplicate/out-of-order events | Use a stable chain event key (for example network + tx hash/signature + log/instruction index); enforce uniqueness in the same transaction as the ledger write. Same-key/same-payload is existing/no-op; different payload is rejected. Older state cannot overwrite newer state, and compensation is unique. |

Application request idempotency continues to use `application/idempotency` `(scope,key,request hash)` and the repository's atomic `CreateInserted` / `CreateSameKeyExisting` winner result ([`application/idempotency/service.go:38`](../../application/idempotency/service.go#L38), [`capability/transaction/repository.go:184`](../../capability/transaction/repository.go#L184), result of checklist item 1). Chain-event keys and player-request keys are separate namespaces. The executable contract applies an event once and compensates a reorg once ([`application/gameledger/contract.go:140`](../../application/gameledger/contract.go#L140), [`application/gameledger/contract_test.go:17`](../../application/gameledger/contract_test.go#L17)).

## Scope and explicit exclusions

The repository currently exposes native/ERC-20/SPL fungible queries. This design does not claim a general NFT ownership index and does not add a production game ledger, player-wallet binding, marketplace, drops, support reconciliation, or dispute workflow. The reference package is a minimal verifiable contract, not persistence; hosts must implement transactional uniqueness, replay/rebuild, authorization, and business compensation. **No custodial wallets or production key operations are implemented.** Production HSM/KMS, rotation, approvals, and funds controls remain host responsibilities, consistent with analysis gap 5 ([`chain-game-foundation-2026-10-19.md:57`](../analysis/chain-game-foundation-2026-10-19.md#L57)).

Data export, analytics export, and cloud backup are off by default; hosts must explicitly enable them before sending player, wallet, or ledger data to external services.

## Falsifiable checks

- `TestDuplicateTransferEventIsAppliedExactlyOnce`: redelivery creates one entry.
- `TestPendingEntryIsVisibleButNotSpendable`: pending is queryable but unavailable until confirmed.
- `TestReorgCompensatesOnce`: reorg compensation occurs once and the entry is not spendable.
- `TestAssetIdentityNormalizationPreservesStandardDifferences`: normalize address/token ID while preserving ERC-721/ERC-1155/SPL distinctions.

These tests validate the boundary contract; they do not prove a host's complete ledger or production chain data.
