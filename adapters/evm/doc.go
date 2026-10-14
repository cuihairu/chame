// Package evm implements the EVM adapter (spec §7): multi-network RPC
// endpoints, chain-ID verification, read-only queries, and log reads,
// behind the chain-agnostic capability contracts.
//
// This is the only package allowed to import EVM SDKs (ADR-0001). SDK
// types never cross the package boundary: the public surface speaks
// identifiers, big.Int, and the chain-agnostic record types defined
// here.
//
// Phase 3 scope (spec §19): read-only capabilities. Transaction
// building, submission, and finality tracking land with the transaction
// capability in later phases. The signing surface (spec §12) starts at
// the development signer: EIP-191 personal-message signing over a held
// key, unsafe by declaration and refused in production environments;
// transaction signing joins when the transaction capability lands.
package evm
