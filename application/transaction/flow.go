package transaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/clock"
	cherrs "github.com/cuihairu/chame/core/errors"
)

// Flow drives one transaction record through the shared state machine
// (§9). It owns everything chain-agnostic about the happy path: opening
// an idempotent operation, moving the record one phase at a time through
// conditional writes, attaching the chain reference in the same write as
// the status that makes it meaningful, and classifying a failed step into
// a status the matrix accepts. The chain-specific work is a Transactor
// the composition root supplies, so the application layer imports no
// adapter (ADR-0001).
//
// Every phase is durable before the next begins, so a flow that dies
// between two phases can resume: Drive runs it on a record at any phase
// it owns. A record at submitted or beyond is the recovery worker's
// (§10.4), not the flow's, and Drive returns it untouched.
//
// Two callers racing on one idempotency key converge on one record, and
// the conditional writes keep that record's state consistent: the loser
// of a step stops and releases the claim it made, it does not overwrite
// the winner. A store write that fails after a broadcast is the one
// case a flow cannot undo (§10.3: a database transaction and a chain
// transaction are not one atomic transaction) — the record then lacks
// the reference for bytes that may be on chain, the recovery worker
// reports the outcome as unknown, and the application reconciles it.
type Flow struct {
	repo           txmodel.Repository
	transactor     txmodel.Transactor
	signers        []signer.Signer
	now            clock.Clock
	logger         *slog.Logger
	approver       Approver
	limit          *Limit
	audit          AuditLog
	controlRequest ControlRequestFunc
}

// FlowOption customizes the flow.
type FlowOption func(*Flow)

// WithInternalControls enables host configured approval, limits and audit.
func WithInternalControls(a Approver, l *Limit, log AuditLog, request ControlRequestFunc) FlowOption {
	return func(f *Flow) { f.approver, f.limit, f.audit, f.controlRequest = a, l, log, request }
}

// WithSigners supplies the signers a flow offers to the transactor.
// Coverage is answered by each signer's own allow-list (§12.3); a payload
// no provided signer covers fails the record rather than being broadcast.
func WithFlowSigners(signers ...signer.Signer) FlowOption {
	return func(f *Flow) { f.signers = signers }
}

// WithLogger injects the diagnostic logger. Default: slog.Default.
func WithFlowLogger(l *slog.Logger) FlowOption {
	return func(f *Flow) { f.logger = l }
}

// WithClock injects the time source. Default: clock.System.
func WithFlowClock(c clock.Clock) FlowOption {
	return func(f *Flow) { f.now = c }
}

// NewFlow builds the flow. Repository and transactor are required: a flow
// without either would neither record nor execute anything.
func NewFlow(repo txmodel.Repository, transactor txmodel.Transactor, opts ...FlowOption) (*Flow, error) {
	if repo == nil {
		return nil, fmt.Errorf("transaction flow: repository is required")
	}
	if transactor == nil {
		return nil, fmt.Errorf("transaction flow: transactor is required")
	}
	f := &Flow{repo: repo, transactor: transactor, now: clock.System{}, logger: slog.Default()}
	for _, opt := range opts {
		opt(f)
	}
	if f.now == nil {
		f.now = clock.System{}
	}
	if f.logger == nil {
		f.logger = slog.Default()
	}
	return f, nil
}

// Begin opens the operation an idempotency key names (§10.1) or returns
// the one that already carries it. seed is the record to store: its
// identity, network, and request hash come from the application, and its
// chain data is where the application stores the intent the transactor
// builds from. With an idempotency key, the same key and same request
// returns the stored operation; the same key with a different request
// returns an idempotency conflict. Keyless records are opened plainly.
func (f *Flow) Begin(ctx context.Context, scope txmodel.IdempotencyScope, key string, seed txmodel.Transaction) (txmodel.Transaction, error) {
	if seed.Status == "" {
		seed.Status = txmodel.TxCreated
	}
	if seed.Status != txmodel.TxCreated {
		return txmodel.Transaction{}, fmt.Errorf("transaction flow: begin requires a created record, got %s", seed.Status)
	}
	if key != "" {
		if scope.Application == "" || scope.OperationType == "" {
			return txmodel.Transaction{}, fmt.Errorf("transaction flow: idempotency key %q requires an application and an operation type", key)
		}
		if seed.RequestHash == "" {
			return txmodel.Transaction{}, fmt.Errorf("transaction flow: idempotency key %q requires a request hash", key)
		}
		seed.IdempotencyScope = scope
		seed.IdempotencyKey = key
	}
	if seed.CreatedAt.IsZero() {
		seed.CreatedAt = f.now.Now()
	}
	if seed.UpdatedAt.IsZero() {
		seed.UpdatedAt = seed.CreatedAt
	}
	if err := txmodel.ValidateRecord(seed); err != nil {
		return txmodel.Transaction{}, err
	}
	if key == "" {
		if _, err := f.repo.Create(ctx, seed); err != nil {
			return txmodel.Transaction{}, err
		}
		return seed, nil
	}
	f.logger.InfoContext(ctx, "transaction operation opened", "op_id", seed.OperationID, "tx_id", seed.ID, "idempotency_key", key, "stage", "begin")

	stored, err := f.repo.FindByIdempotencyKey(ctx, scope, key)
	if err == nil {
		if stored.RequestHash != seed.RequestHash {
			return txmodel.Transaction{}, txmodel.IdempotencyConflict(scope, key)
		}
		// Replay of the same request: the stored operation is the
		// operation, so the application reads its progress from it.
		f.logger.InfoContext(ctx, "transaction operation replayed", "op_id", stored.OperationID, "tx_id", stored.ID, "idempotency_key", key, "stage", "begin")
		return stored, nil
	}
	if !errors.Is(err, txmodel.ErrRecordNotFound) {
		return txmodel.Transaction{}, err
	}
	if _, err := f.repo.Create(ctx, seed); err != nil {
		return txmodel.Transaction{}, err
	}
	// The store keeps the first record for one key: re-read so two
	// callers racing on the same request converge on one operation.
	got, err := f.repo.Get(ctx, seed.ID)
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, txmodel.ErrRecordNotFound) {
		return txmodel.Transaction{}, err
	}
	return f.repo.FindByIdempotencyKey(ctx, scope, key)
}

// Drive advances one record as far as this call can take it (§9): a
// created record through prepare, sign, and dispatch to submitted, or
// from whatever phase it already reached when a previous run stopped.
// Phases are committed in order, so a failure stops the record where it
// honestly is — a failed record is terminal, an undecided outcome is
// unknown, and a transient failure leaves the record in place for
// another Drive.
func (f *Flow) Drive(ctx context.Context, id txmodel.TransactionID) (txmodel.Transaction, error) {
	tx, err := f.repo.Get(ctx, id)
	if err != nil {
		return txmodel.Transaction{}, err
	}
	f.logger.InfoContext(ctx, "transaction drive", "op_id", tx.OperationID, "tx_id", tx.ID, "idempotency_key", tx.IdempotencyKey, "stage", string(tx.Status))
	switch tx.Status {
	case txmodel.TxCreated:
		if f.approver != nil || f.limit != nil || f.audit != nil {
			if f.controlRequest == nil {
				return tx, errors.New("transaction: internal controls require request mapper")
			}
			r := f.controlRequest(tx)
			r.At = f.now.Now()
			result, reason := "allowed", ""
			if err := f.limit.Check(r); err != nil {
				result, reason = "denied", err.Error()
			}
			if reason == "" && f.approver != nil {
				ok, err := f.approver.Approve(ctx, r)
				if err != nil {
					return tx, err
				}
				if !ok {
					result, reason = "denied", "approval required"
				}
			}
			if f.audit != nil {
				if err := f.audit.Append(ctx, AuditEntry{Operator: r.Operator, Action: r.Action, IdempotencyKey: r.IdempotencyKey, Network: r.Network, Result: result, Reason: reason, At: r.At}); err != nil {
					return tx, err
				}
			}
			if reason != "" {
				failed, err := f.failRecord(ctx, tx, txmodel.TxCreated)
				if err != nil {
					return failed, err
				}
				return failed, ErrControlDenied
			}
		}
		return f.prepare(ctx, tx)
	case txmodel.TxPrepared:
		return f.authorize(ctx, tx, txmodel.Prepared{})
	case txmodel.TxSigned:
		return f.dispatch(ctx, tx, txmodel.Authorized{})
	default:
		return tx, nil
	}
}

// prepare turns the record's intent into an unsigned payload. A decisive
// failure ends the record; anything else leaves it at created for
// another Drive.
func (f *Flow) prepare(ctx context.Context, tx txmodel.Transaction) (txmodel.Transaction, error) {
	p, err := f.transactor.Prepare(ctx, tx)
	if err != nil {
		if verdict(err, cherrs.SemReadOnly) {
			return f.failRecord(ctx, tx, txmodel.TxCreated)
		}
		return tx, err
	}
	if err := f.advance(ctx, tx, txmodel.TxCreated, txmodel.TxPrepared, "", p.ChainData); err != nil {
		return f.afterLostAdvance(ctx, tx, err)
	}
	tx.Status = txmodel.TxPrepared
	tx.ChainData = p.ChainData
	return f.authorize(ctx, tx, p)
}

// authorize signs the payload. A decisive failure ends the record and
// releases the claim the prepare made; anything else leaves the record
// at prepared for another Drive.
func (f *Flow) authorize(ctx context.Context, tx txmodel.Transaction, p txmodel.Prepared) (txmodel.Transaction, error) {
	a, err := f.transactor.Authorize(ctx, tx, p, f.signers...)
	if err != nil {
		if verdict(err, cherrs.SemReadOnly) {
			return f.failRecord(ctx, tx, txmodel.TxPrepared)
		}
		return tx, err
	}
	if err := f.advance(ctx, tx, txmodel.TxPrepared, txmodel.TxSigned, "", a.ChainData); err != nil {
		return f.afterLostAdvance(ctx, tx, err)
	}
	tx.Status = txmodel.TxSigned
	tx.ChainData = a.ChainData
	return f.dispatch(ctx, tx, a)
}

// dispatch broadcasts the signed bytes and writes the chain reference in
// the same atomic step as the status it belongs to. A decisive rejection
// ends the record and releases the claim. An undecided outcome records
// unknown instead: the bytes may already be on chain, so the claim is
// spent and the recovery worker observes the record (§14.3, §10.4).
func (f *Flow) dispatch(ctx context.Context, tx txmodel.Transaction, a txmodel.Authorized) (txmodel.Transaction, error) {
	// A resumed record arrives with a zero carry-over: the chain data it
	// should keep is the one the record already holds, so a nil from the
	// dispatch step cannot clear the payload an earlier run stored.
	if a.ChainData == nil {
		a.ChainData = tx.ChainData
	}
	ref, err := f.transactor.Dispatch(ctx, tx, a)
	if err != nil {
		if verdict(err, cherrs.SemSubmit) {
			return f.failRecord(ctx, tx, txmodel.TxSigned)
		}
		return f.advanceAndSettle(ctx, tx, txmodel.TxSigned, txmodel.TxUnknown, "", a.ChainData, txmodel.SettleBroadcast)
	}
	return f.advanceAndSettle(ctx, tx, txmodel.TxSigned, txmodel.TxSubmitted, ref, a.ChainData, txmodel.SettleBroadcast)
}

// advance writes one phase with the chain fields that belong to it.
func (f *Flow) advance(ctx context.Context, tx txmodel.Transaction, from, next txmodel.TransactionStatus, ref string, data any) error {
	return f.repo.Attach(ctx, tx.ID, from, next, ref, data, f.now.Now())
}

// advanceAndSettle writes one phase and then reports the outcome to the
// transactor. A lost conditional write is not ours to settle over:
// another caller advanced the record first, so the winner's decision
// stands — and the outcome reported is the one this step already earned,
// not the write's, because broadcast bytes stay broadcast whatever the
// store said.
func (f *Flow) advanceAndSettle(ctx context.Context, tx txmodel.Transaction, from, next txmodel.TransactionStatus, ref string, data any, outcome txmodel.SettleOutcome) (txmodel.Transaction, error) {
	if err := f.advance(ctx, tx, from, next, ref, data); err != nil {
		if f.transientAdvance(err) {
			return f.settleAndReload(ctx, tx, outcome)
		}
		return tx, err
	}
	f.settle(ctx, tx, outcome)
	tx.Status = next
	tx.ChainHash = ref
	tx.ChainData = data
	return tx, nil
}

// failRecord ends a record on a decisive failure and releases the claim
// its prepare made. The chain data stays with the record: a failed
// record is evidence, and the claim's trail belongs in it.
func (f *Flow) failRecord(ctx context.Context, tx txmodel.Transaction, from txmodel.TransactionStatus) (txmodel.Transaction, error) {
	if err := f.advance(ctx, tx, from, txmodel.TxFailed, "", tx.ChainData); err != nil {
		if f.transientAdvance(err) {
			return f.settleAndReload(ctx, tx, txmodel.SettleFailed)
		}
		return tx, err
	}
	f.settle(ctx, tx, txmodel.SettleFailed)
	tx.Status = txmodel.TxFailed
	return tx, nil
}

// afterLostAdvance handles a conditional write the flow lost: the record
// moved on elsewhere, so its state is truth and the flow's own claim was
// never broadcast.
func (f *Flow) afterLostAdvance(ctx context.Context, tx txmodel.Transaction, err error) (txmodel.Transaction, error) {
	if f.transientAdvance(err) {
		return f.settleAndReload(ctx, tx, txmodel.SettleFailed)
	}
	return tx, err
}

// transientAdvance reports the conditions where the repository already
// holds the truth: a stale expectation or a matrix refusal. Persistence
// failures are propagated, because the write may have landed.
func (f *Flow) transientAdvance(err error) bool {
	return errors.Is(err, txmodel.ErrStatusConflict) || errors.Is(err, txmodel.ErrIllegalTransition)
}

// settleAndReload reports the outcome and returns the stored record.
func (f *Flow) settleAndReload(ctx context.Context, tx txmodel.Transaction, outcome txmodel.SettleOutcome) (txmodel.Transaction, error) {
	f.settle(ctx, tx, outcome)
	return f.repo.Get(ctx, tx.ID)
}

// settle reports the outcome to the transactor. It is bookkeeping over
// chain-side claims, so it never blocks the record move that came first.
func (f *Flow) settle(ctx context.Context, tx txmodel.Transaction, outcome txmodel.SettleOutcome) {
	f.transactor.Settle(ctx, tx, outcome)
}

// verdict reports whether the classification policy calls this error a
// failure that will never change its mind. An unclassified error counts
// as decisive: the framework cannot speak for the step, and the record
// is not left to spin on a retry it can never win.
func verdict(err error, sem cherrs.Semantics) bool {
	return cherrs.Policy(err, sem).Hint == cherrs.HintNever
}
