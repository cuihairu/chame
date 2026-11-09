// Package core is the foundation every other layer stands on, in two
// groups, so a reader can take the dependency direction once instead of
// judging it package by package.
//
// Cross-layer contracts: names and types that appear in port and module
// declarations and are referenced by capability packages, adapters,
// applications, and the runtime alike.
//
//	capability   capability slot names (spec §6.1)
//	identifiers  chain identity vocabulary: networks, addresses, assets
//	module       the module and registry contract the runtime validates
//
// Runtime rules: execution vocabulary any layer may use, which carries
// no port semantics. lifecycle is the state machine core/runtime drives
// (spec §4.2); runtime is the composition root; clock, config, errors,
// logging, metrics, and health are the process-wide services a module or
// adapter may call.
//
// Nothing under core/ imports anything outside core/: the contracts and
// rules here depend on the standard library alone, which is what keeps
// them safe to be depended on from every direction.
package core
