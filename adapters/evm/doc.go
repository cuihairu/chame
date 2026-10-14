// Package evm implements the EVM adapter (spec §7): multi-network RPC
// endpoints, chain-ID verification, nonce management, receipts, finality,
// and log events, all behind the capability contracts.
//
// This is the only package allowed to import EVM SDKs (ADR-0001).
//
// Status: skeleton. Implementation lands with the phased plan in todo.md.
package evm
