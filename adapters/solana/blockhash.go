package solana

import (
	"context"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// BlockhashPin is one recent blockhash with the block height it stays
// valid for (spec §8.4). A message built with it binds the hash; once the
// chain's block height reaches LastValidBlockHeight the chain will no
// longer accept a transaction carrying it. The hash is base58, the
// rendering the adapter uses everywhere else (ADR-0001: no SDK type
// crosses the boundary). A rebuild must take a fresh pin, not reuse this
// one.
type BlockhashPin struct {
	// Blockhash is the recent blockhash, base58.
	Blockhash string
	// LastValidBlockHeight is the last block height at which a
	// transaction carrying this hash is still accepted.
	LastValidBlockHeight uint64
}

// ExpiryState classifies one pin against an observed block height.
type ExpiryState string

const (
	// ExpiryValid means the chain will still accept the hash.
	ExpiryValid ExpiryState = "valid"
	// ExpiryExpired means the chain will no longer accept the hash.
	ExpiryExpired ExpiryState = "expired"
)

// Valid states where the pin stands at height. The hash is valid up to
// LastValidBlockHeight and exclusive of it: the boundary itself is the
// first height that rejects it.
func (p BlockhashPin) Valid(height uint64) ExpiryState {
	if height < p.LastValidBlockHeight {
		return ExpiryValid
	}
	return ExpiryExpired
}

// ExpiryVerdict is what one expiry check found (spec §8.4). It is never
// a command: only the last of the five states permits a rebuild, and even
// then the caller decides.
type ExpiryVerdict string

const (
	// ExpiryStillValid means the hash is inside its window. Nothing
	// happened yet; there is nothing to recover.
	ExpiryStillValid ExpiryVerdict = "still-valid"
	// ExpiryLanded means the chain accepted the original transaction. The
	// business effect happened and must not be retried.
	ExpiryLanded ExpiryVerdict = "landed"
	// ExpiryFailed means the original transaction was processed and did
	// not succeed. Terminal and not a retry: the business compensates.
	ExpiryFailed ExpiryVerdict = "failed"
	// ExpiryUncertain means the window has closed and the node reports no
	// status. That is not evidence the transaction did not run — it may
	// have landed on a branch this node cannot see. Keep the record
	// unknown and keep reconciling; do not rebuild.
	ExpiryUncertain ExpiryVerdict = "uncertain"
	// ExpiryRebuildable means the window has closed and a ledger-wide
	// search found no trace of the signature. This is the strongest
	// negative evidence a node can give, not a proof, and rebuilding is
	// still the caller's call.
	ExpiryRebuildable ExpiryVerdict = "rebuildable"
)

// ExpiryOptions tunes one expiry check.
type ExpiryOptions struct {
	// SearchHistory asks the node to search its ledger for the signature
	// instead of answering from the recent status cache. Without it, an
	// unobserved signature stays ExpiryUncertain.
	SearchHistory bool
}

// ExpiryReport is the outcome of one expiry check. It carries the evidence
// the caller needs to record on the transaction, not just the verdict.
type ExpiryReport struct {
	Pin BlockhashPin
	// Height is the block height the window was judged against.
	Height  uint64
	Verdict ExpiryVerdict
	// Status is the observed confirmation when the node had one.
	Status *ConfirmationStatus
	// HistorySearched reports whether the ledger was searched rather than
	// the recent status cache.
	HistorySearched bool
}

// LatestBlockhash pins the blockhash a new transaction should bind, at
// the caller's commitment (spec §8.3). A node that answers without one
// yields an error: there is no safe substitute.
func (n *Network) LatestBlockhash(ctx context.Context, commitment Commitment) (BlockhashPin, error) {
	c := n.commit(commitment).rpc()
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out *latestBlockhashResult
	err := n.pool.call(ctx, n.id.String(), n.opBase+".latest_blockhash", func(rc rpcClient) error {
		var e error
		out, e = rc.LatestBlockhash(ctx, c)
		return e
	})
	if err != nil {
		n.stats.countError()
		return BlockhashPin{}, err
	}
	if out == nil || out.Value == nil || out.Value.Blockhash.IsZero() {
		n.stats.countError()
		return BlockhashPin{}, cherrs.New(cherrs.InternalError, n.opBase+".latest_blockhash",
			"node returned no blockhash")
	}
	hash := out.Value.Blockhash.String()
	if hash == "" {
		n.stats.countError()
		return BlockhashPin{}, cherrs.New(cherrs.InvalidConfiguration, n.opBase+".latest_blockhash",
			"node returned an empty blockhash")
	}
	return BlockhashPin{
		Blockhash:            hash,
		LastValidBlockHeight: out.Value.LastValidBlockHeight,
	}, nil
}

// BlockHeight reports the node's current block height at the caller's
// commitment: the observation a pin's window is judged against.
func (n *Network) BlockHeight(ctx context.Context, commitment Commitment) (uint64, error) {
	c := n.commit(commitment).rpc()
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var height uint64
	err := n.pool.call(ctx, n.id.String(), n.opBase+".block_height", func(rc rpcClient) error {
		var e error
		height, e = rc.BlockHeight(ctx, c)
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	return height, nil
}

// Expire decides what to do with a submitted transaction whose blockhash
// may have expired (spec §8.4, spec §8.2).
//
// An expired blockhash is neither proof the transaction ran nor proof it
// did not. The check therefore asks the chain: a signature the node
// knows about is decisive either way, and only an unobserved signature
// that also survives a ledger-wide search may support a rebuild. A
// transaction that merely looks unobserved stays unknown.
func (n *Network) Expire(ctx context.Context, pin BlockhashPin, signature string, opts ExpiryOptions) (ExpiryReport, error) {
	report := ExpiryReport{Pin: pin, HistorySearched: opts.SearchHistory}
	height, err := n.BlockHeight(ctx, CommitmentFinalized)
	if err != nil {
		return report, err
	}
	report.Height = height
	if pin.Valid(height) == ExpiryValid {
		return report.withVerdict(ExpiryStillValid), nil
	}

	sig, err := toSignature(n.opBase+".expire", signature)
	if err != nil {
		return report, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var entries []*rpcSignatureStatusesResult
	err = n.pool.call(ctx, n.id.String(), n.opBase+".expire", func(rc rpcClient) error {
		var e error
		entries, e = rc.SignatureStatus(ctx, sig, opts.SearchHistory)
		return e
	})
	if err != nil {
		n.stats.countError()
		return report, err
	}
	if len(entries) == 0 || entries[0] == nil {
		if opts.SearchHistory {
			return report.withVerdict(ExpiryRebuildable), nil
		}
		return report.withVerdict(ExpiryUncertain), nil
	}
	status := confirmationStatus(signature, entries[0])
	report.Status = status
	if status.ErrDetail == "" {
		return report.withVerdict(ExpiryLanded), nil
	}
	return report.withVerdict(ExpiryFailed), nil
}

// withVerdict copies the report with its verdict set, so an error on the
// way leaves the caller with whatever was already observed.
func (r ExpiryReport) withVerdict(v ExpiryVerdict) ExpiryReport {
	r.Verdict = v
	return r
}

// confirmationStatus renders one signature status entry.
func confirmationStatus(signature string, e *rpcSignatureStatusesResult) *ConfirmationStatus {
	return &ConfirmationStatus{
		Signature:     signature,
		Slot:          e.Slot,
		Confirmations: uint64OrZero(e.Confirmations),
		ErrDetail:     errDetail(e.Err),
		Status:        string(e.ConfirmationStatus),
	}
}
