package transaction

// legalTransitions is the transition matrix (spec §9.1, §7.8, §10.4).
//
// created    -> prepared | failed
// prepared   -> signed | failed
// signed     -> submitted | unknown | failed
//
//	(a lost submission response lands in unknown, never failed)
//
// submitted  -> pending | included | reverted | failed | unknown
// pending    -> included | reverted | failed | unknown
// included   -> confirmed | reverted | reorged
// confirmed  -> finalized | reorged
// finalized  -> terminal
// reverted   -> terminal
// failed     -> terminal
// reorged    -> unknown (reconciliation, then unknown resolves)
// unknown    -> pending | included | confirmed | finalized | reverted |
//
//	failed  (resolution after reconciliation; never reorged
//	directly, because reorged requires a prior on-chain
//	observation that unknown does not carry)
//
// Rules the matrix encodes:
//
//   - Outcome uncertainty is a first-class status: timeouts and lost
//     responses never produce failed (spec §14.3: 交易提交超时先对账).
//   - Terminal statuses are frozen.
//   - No backwards movement except reorged -> unknown, which is a loss
//     of certainty, not a rollback.
var legalTransitions = map[TransactionStatus][]TransactionStatus{
	TxCreated:   {TxPrepared, TxFailed},
	TxPrepared:  {TxSigned, TxFailed},
	TxSigned:    {TxSubmitted, TxUnknown, TxFailed},
	TxSubmitted: {TxPending, TxIncluded, TxReverted, TxFailed, TxUnknown},
	TxPending:   {TxIncluded, TxReverted, TxFailed, TxUnknown},
	TxIncluded:  {TxConfirmed, TxReverted, TxReorged},
	TxConfirmed: {TxFinalized, TxReorged},
	TxFinalized: {},
	TxReverted:  {},
	TxFailed:    {},
	TxReorged:   {TxUnknown},
	TxUnknown:   {TxPending, TxIncluded, TxConfirmed, TxFinalized, TxReverted, TxFailed},
}
