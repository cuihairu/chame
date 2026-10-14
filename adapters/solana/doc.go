// Package solana implements the Solana adapter (spec §8): cluster identity
// verification, account and token queries, commitment configuration, and
// blockhash-expiry-aware transaction handling, independent of the EVM
// adapter.
//
// This is the only package allowed to import Solana SDKs (ADR-0001).
//
// Status: skeleton. Implementation lands with the phased plan in todo.md.
package solana
