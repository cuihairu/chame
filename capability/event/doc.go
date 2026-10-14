// Package event defines the standard on-chain event model and delivery
// contract (spec §11): unique event identities, deduplication, at-least-
// once delivery with idempotent consumers, cursor-based resumable
// consumption, and reorganization handling. Event models differ between
// chains and are never assumed identical (§11.1); the model carries only
// what every source can express, with chain-specific payloads kept raw
// (spec §21.2).
package event
