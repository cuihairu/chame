// Package solana implements the Solana adapter (spec §8): cluster identity
// verification, account and SPL token queries, transaction and signature
// queries, commitment configuration, and connection metrics, independent
// of the EVM adapter.
//
// This is the only package allowed to import Solana SDKs (ADR-0001). SDK
// types never cross the package boundary: the public surface speaks
// identifiers, base58 strings, and the chain-agnostic record types
// defined here.
//
// Phase 4 scope (spec §19, ADR-0003): read-only capabilities. Transaction
// building, signing, submission, blockhash-expiry recovery, and finality
// tracking land with the transaction capability in Phase 5; asking for
// them here returns an explicit unsupported-capability error.
package solana
