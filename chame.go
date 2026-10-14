// Package chame is a Go game backend framework that abstracts blockchains
// behind one runtime. A game server talks to a single API; EVM and Solana
// are adapters behind it.
//
// Layout and rules are defined by docs/design/chame-spec.md: core/ holds
// framework infrastructure, capability/ holds chain-agnostic contracts,
// adapters/ implement concrete chains, and game business code stays outside
// the framework.
//
// Status: skeleton. Package interfaces land with the phased plan in
// todo.md; see docs/decisions/ for recorded architecture decisions.
package chame
