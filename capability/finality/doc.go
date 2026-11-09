// Package finality defines the finality contract for the capability slot
// declared as capability.Finality (spec §7.8, §8.5, §11.2, §11.4). Today
// it carries the shared state vocabulary (State): the three states every
// source can express, the delivery gate compares, and persistence
// serializes. Per-network policies stay adapter-side for now — EVM
// confirmations depth (adapters/evm FinalityConfig) and Solana commitment
// (adapters/solana Commitment) — mapping their observations onto State.
// A per-network policy interface lands here when a second consumer needs
// it, not before (spec §2.1 reserves the FinalityProvider port).
package finality
