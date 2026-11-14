# On-chain Asset Ownership and Game Ledger Boundary

Status: finalized framework contract; the host game still owns its ledger implementation.

## Decision

**The chain is authoritative for external asset facts; the game ledger is authoritative for in-game spendable rights and inventory. A chain event may support a ledger entry only after the configured finality threshold is met and the event remains on the canonical chain.** A queried balance is not a game ledger and does not itself grant a player redemption, spending, or transfer rights. `AssetProvider` only queries assets and balances; for SPL, the holder is a token account ([`capability/asset/asset.go:71`](../../capability/asset/asset.go#L71), the `Balance` contract comment). The event dispatcher delays delivery until the configured finality threshold (`MinFinality`, [`application/eventing/dispatcher.go:42`](../../application/eventing/dispatcher.go#L42)) and leaves reorg invalidation to the consumer (`Reorg` callback, [`application/eventing/dispatcher.go:104`](../../application/eventing/dispatcher.go#L104)).

## Canonical asset identity

Hosts must key assets by `(network.family, network.name, network.chain_id, standard, contract_or_mint, token_id?)`; symbols, names, addresses, or token IDs alone are insufficient. Preserve standard namespaces: `native`, `erc20`, `erc721`, `erc1155`, `spl-token`, and `spl-nft` are distinct. Native assets have no contract; ERC standards require an EVM network and contract; SPL standards require a Solana network and mint; ERC-721/1155 and SPL NFT require a token ID; fungible standards forbid one. Reject unknown standards. Canonicalize EVM addresses to lowercase `0x` form and token IDs to unsigned decimal without leading zeroes. Preserve Solana base58 case.

The executable contract is in [`capability/asset/identity.go:33`](../../capability/asset/identity.go#L33) (`AssetIdentity`/`Standard`, shared by both adapters and the ledger) and [`application/gameledger/contract.go`](../../application/gameledger/contract.go), with the assertion that standard differences must not be normalized away in [`capability/asset/identity_test.go:17`](../../capability/asset/identity_test.go#L17). Existing `identifiers.AssetID` locates fungible query assets and is not an NFT token-ID identity ([`core/identifiers/asset.go:23`](../../core/identifiers/asset.go#L23)).

## Authority, visibility, and writes

| Case | Authority and game-ledger rule |
|---|---|
| Current on-chain holder/balance | A node or trusted index supplies chain observations. An SPL holder is a token account, not a game player. The host explicitly maps wallets to players and verifies that binding. |
| Spendable balance/inventory | The host game ledger is authoritative. Rights are spendable only after its business entry commits; chain balances must not overwrite it. |
| Before finality | The host may expose a `pending` record for queries, but it is not spendable, redeemable, or eligible for irreversible rewards. After the configured `confirmed` or `final` threshold, and absent a reorg, the host may atomically commit state and ledger entry in one database transaction. |
| Finality/reorg | A `reorged` event grants no spendable rights. Reverse prior reversible credits with one compensation entry linked to the original event. If rewards were consumed or caused external effects, enter a host-defined dispute process; a chain reorg is not a game database rollback. |
| Duplicate/out-of-order events | Use a stable chain event key (for example network + tx hash/signature + log/instruction index); enforce uniqueness in the same transaction as the ledger write. Same-key/same-payload is existing/no-op; different payload is rejected. Older state cannot overwrite newer state, and compensation is unique. |

Application request idempotency continues to use `application/idempotency` `(scope,key,request hash)` and the repository's atomic `CreateInserted` / `CreateSameKeyExisting` winner result ([`application/idempotency/service.go:38`](../../application/idempotency/service.go#L38), [`capability/transaction/repository.go:184`](../../capability/transaction/repository.go#L184), result of checklist item 1). Chain-event keys and player-request keys are separate namespaces. The executable contract uses `Apply` to insert a repeated event only once, and a reorged entry keeps answering its compensation amount until the host records the inverse operation applied with `MarkCompensated` — a host that stops between the mark and the operation retries with the same amount ([`application/gameledger/contract.go:54`](../../application/gameledger/contract.go#L54), [`application/gameledger/contract.go:86`](../../application/gameledger/contract.go#L86), [`application/gameledger/contract.go:118`](../../application/gameledger/contract.go#L118), [`application/gameledger/contract_test.go:58`](../../application/gameledger/contract_test.go#L58)).

## Scope and explicit exclusions

The repository currently exposes native/ERC-20/SPL fungible queries plus read-only NFT ownership lookups through `capability/nft` — EVM ERC-721 `ownerOf`, ERC-1155 `balanceOf`, and Solana SPL holding (Metaplex metadata decides whether the mint is an NFT), wired in [`adapters/evm/asset.go:17`](../../adapters/evm/asset.go#L17) and [`adapters/solana/asset.go:17`](../../adapters/solana/asset.go#L17) behind [`capability/nft/nft.go:40`](../../capability/nft/nft.go#L40). A query is not an index: no chain-wide NFT ownership index exists. This design does not add a production game ledger, player-wallet binding, marketplace, drops, support reconciliation, or dispute workflow. The reference package is a minimal verifiable contract, not persistence; hosts must implement transactional uniqueness, replay/rebuild, authorization, and business compensation. **No custodial wallets or production key operations are implemented.** Production HSM/KMS, rotation, approvals, and funds controls remain host responsibilities, consistent with analysis gap 5 ([`chain-game-foundation-2026-10-19.md:57`](../analysis/chain-game-foundation-2026-10-19.md#L57)).

Data export, analytics export, and cloud backup are off by default; hosts must explicitly enable them before sending player, wallet, or ledger data to external services.

## Independent index and rebuildable projection

The event dispatcher still owns RPC polling and the finality/reorg gate, and saves raw event evidence through `event.Repository.Create` before advancing the chain-source cursor. The projection sits downstream of that and does not poll the chain or write the ledger: [`application/projection/projection.go:25`](../../application/projection/projection.go#L25) (`Replayer`) reads the persisted cursor and `ReplayEvents`, feeds canonical raw events to a pluggable `Backend`, and produces a disposable derived read model. `ReplayEvents` keeps the original payload and event records, including delivered ones; reorg-marked records stay auditable but do not count toward the canonical projection.

Rebuild steps: read the saved cursor for `(network, source)`; clear only the projection backend; read the raw evidence before that cursor in a stable order of chain height, event index, and ID; reapply the non-reorged events. The cursor lives in the existing event repository — the projection store holds no chain position and never rewrites raw events. A rebuild must compare deterministic before/after snapshots; when a replay fails, the raw evidence stays so the derived state can be cleared and retried. Fake A (key-value aggregation) and fake B (aggregate-after-append) prove that swapping the adapter keeps the projection semantics the same.

The external seam is `projection.Backend`: a host may implement the query/write bridge for Goldsky, The Graph, or a self-built indexer, hand normalized events to the same projection logic, or write the projection into its own read model. Those are optional external options — this repository binds no service and adds no SDK or dependency, and external sending and cloud backup stay off by default. The current implementation is an in-memory fake that verifies the adapter contract; it is not a real service integration or a full-history backfill service.

## Falsifiable checks

- `TestDuplicateTransferEventIsAppliedExactlyOnce`: redelivery creates one entry.
- `TestPendingEntryIsVisibleButNotSpendable`: pending is queryable but unavailable until confirmed.
- `TestReorgCompensationAnsweredUntilApplied`: the reorg compensation amount stays answerable until the host records `MarkCompensated`; afterwards the entry is not spendable.
- `TestReorgCompensationRequiresADeliveredState`: only a delivered (confirmed/final) entry earns a reversal; pending entries compensate nothing and live entries cannot be compensated.
- `TestAssetIdentityNormalizationPreservesStandardDifferences`: normalize address/token ID while preserving ERC-721/ERC-1155/SPL distinctions.

These tests validate the boundary contract; they do not prove a host's complete ledger or production chain data.
