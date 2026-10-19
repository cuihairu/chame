# Deployment write model

## EVM

Nonce allocation is in process memory and is safe only inside one Chame process. A single `(network, signer account)` may have one active writer. `LocalDevSigner` acquires a host-local OS lease for each EVM account at construction and returns an explicit `already has an active writer` error if another instance holds it; call `Close` during graceful shutdown (process exit also releases the lease). Duplicate `(network, account)` entries in a signer allow-list are rejected by validation.

The host-local lease does not coordinate across machines, containers without a shared lock namespace, or custom signer implementations. Production deployment must enforce a singleton writer per account through its scheduler/orchestrator. To scale horizontally, assign each instance a different signing account; do not share one account across replicas. Shared nonce coordination is not implemented.

## Solana

An expired recent blockhash with no observed signature remains unknown unless a ledger-wide history search also found no record. A failed status query is not evidence of non-execution. Keep reconciling unknown transactions; do not infer failure or blindly replay them.

## Secret and data handling

This library does not require or include real keys in tests. Production keys belong in a host-managed signer/HSM/KMS. Data export, analytics export, and cloud backup are off by default; a host must explicitly enable them before sending player, wallet, or ledger data externally.
