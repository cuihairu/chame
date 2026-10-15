package transaction

import (
	"context"

	"github.com/cuihairu/chame/capability/signer"
)

// Prepared is the carry-over a prepare step hands to its sign step. The
// flow shuttles it between the two and never opens either field: the
// payload is adapter encoding, the chain data is adapter-owned record
// state.
type Prepared struct {
	// Payload is the unsigned transaction encoding the sign step signs.
	Payload []byte
	// ChainData is the payload the record should hold while it waits to
	// be signed: the adapter-owned state recovery needs (a pinned
	// blockhash window, a claimed nonce, the intent as built).
	ChainData any
}

// Authorized is the carry-over a sign step hands to a dispatch step. The
// flow shuttles it and never opens either field.
type Authorized struct {
	// Signed is the broadcastable encoding.
	Signed []byte
	// ChainData is the payload the record should hold while it waits to
	// be broadcast.
	ChainData any
}

// SettleOutcome tells a transactor how the flow ended for a record, so
// it can close the chain-side claims a prepare made (a claimed nonce
// above all, §7.5).
type SettleOutcome string

const (
	// SettleBroadcast: the signed bytes were handed to the chain, or
	// their fate is open. Claims are spent — a nonce must never be
	// reused, because the transaction may still land (§14.3: a lost
	// submission response never licenses a resend).
	SettleBroadcast SettleOutcome = "broadcast"
	// SettleFailed: the record reached a terminal failure before the
	// bytes could be accepted. Claims are released — a build that never
	// broadcasts leaves no hole (§7.5).
	SettleFailed SettleOutcome = "failed"
)

// Transactor is the chain-specific half of a transaction flow (§9). It
// is implemented by adapter-backed glue the composition root wires; the
// flow sees only records, statuses, and chain references, never chain
// encodings (ADR-0001: the application layer imports no adapter).
//
// Each step reads what it needs from the record when it runs: a record
// that reached a phase in an earlier run carries its chain data, so a
// resumed flow works from the record — a sign step may receive a zero
// Prepared and must take the payload from the record's chain data.
type Transactor interface {
	// Prepare turns the intent stored on the record into an unsigned
	// payload and the chain data to keep for it. It may claim chain-side
	// resources (an EVM nonce, §7.5); a prepare that fails releases its
	// own claims, because only the prepare knows whether it made any.
	Prepare(ctx context.Context, tx Transaction) (Prepared, error)
	// Authorize signs the prepared payload with the signers given —
	// coverage is answered by each signer's own allow-list (§12.3), and
	// a signature hole must stop the flow before anything is broadcast.
	// It returns the bytes to broadcast and the chain data to keep.
	Authorize(ctx context.Context, tx Transaction, p Prepared, signers ...signer.Signer) (Authorized, error)
	// Dispatch broadcasts the signed bytes and reports the chain's
	// reference for them. An error leaves the outcome exactly as
	// classified: the flow decides what the record becomes.
	Dispatch(ctx context.Context, tx Transaction, a Authorized) (string, error)
	// Settle closes the claims the prepare made, as reported by the
	// flow's outcome. It is advisory bookkeeping: implementations must
	// tolerate being called for a record whose claims are already spent
	// or unknown to them.
	Settle(ctx context.Context, tx Transaction, outcome SettleOutcome)
}
