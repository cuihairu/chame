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
// building, signing, submission, and finality tracking land with the
// transaction capability in later phases.
package evm
