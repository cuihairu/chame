// Package account defines the AccountProvider contract (spec §13.1):
// account queries, native balances, and chain-specific account details,
// without imposing one account model on every chain.
//
// An address is not a key-backed account. OwnerKind keeps externally
// owned, program-controlled, and contract accounts apart, and whether an
// address is managed by a signer is answered from the signer's own
// allow-list (spec §12.3) — never from a claim an account record makes
// about itself.
package account
