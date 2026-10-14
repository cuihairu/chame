// Package signer defines the pluggable signing contract (spec §12).
// Signing sits behind an explicit boundary: the Signer interface is
// independent of chain RPC and business modules, adapters and external
// signing services implement it, and business code can only sign for
// the networks and accounts a signer exposes.
//
// The boundaries of spec §12.3 are structural here: every request names
// its target network and account, authorization comes from the signer's
// allow-list rather than from the caller's payload, requests are
// auditable with payload digests instead of payloads, and key material
// travels as a redacting Secret so it never lands in logs, errors, or
// serialized config. The first release ships a deterministic test signer
// plus chain-local development signers in the adapters (spec §12.2); the
// external signing service is a plug-in point through this same
// contract, and a homegrown key custody system is deliberately out of
// scope (spec §12.2: 不要在核心框架中直接实现复杂密钥托管).
package signer
