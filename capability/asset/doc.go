// Package asset defines the AssetProvider contract (spec §13.2): read-
// only asset metadata and balances. Asset writes and transfers go
// through the transaction capability — an AssetProvider must never
// produce on-chain side effects (spec §13.2).
//
// The contract carries no business rules. Whether items may trade,
// whether game currency lives on-chain, or how a chain balance maps to a
// game balance are application decisions (spec §13.3); the framework
// only reports what the chain reports, with integer-only amounts and
// per-asset precision (spec §6.4, §6.5).
package asset
