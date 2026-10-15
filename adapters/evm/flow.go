package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// BuildIntent is the storable form of a build query: the amounts travel
// as decimal strings so a durable store round-trips the intent the
// record carries (§10.2, §10.3). The application stores it in the
// record's chain data when it opens the operation; the adapter builds
// from it on every run, including a resumed one.
type BuildIntent struct {
	Sender   identifiers.Address `json:"sender"`
	To       identifiers.Address `json:"to"`
	Value    string              `json:"value,omitempty"`
	Data     []byte              `json:"data,omitempty"`
	GasLimit uint64              `json:"gas_limit,omitempty"`
	// GasPrice names a legacy build; the two fee fields name an EIP-1559
	// one. Empty means the build decides (§7.6: a fee is a query, not an
	// assumption).
	GasPrice             string            `json:"gas_price,omitempty"`
	MaxFeePerGas         string            `json:"max_fee_per_gas,omitempty"`
	MaxPriorityFeePerGas string            `json:"max_priority_fee_per_gas,omitempty"`
	Nonce                *uint64           `json:"nonce,omitempty"`
	AccessList           []AccessListEntry `json:"access_list,omitempty"`
	Deadline             time.Duration     `json:"deadline,omitempty"`
	IdempotencyKey       string            `json:"idempotency_key,omitempty"`
}

// IntentFromQuery copies a build query into the storable intent. An
// amount that overflows a decimal string is rejected at the source,
// before any record exists.
func IntentFromQuery(q BuildQuery) (*BuildIntent, error) {
	i := &BuildIntent{
		Sender:         q.Sender,
		To:             q.To,
		Data:           q.Data,
		GasLimit:       q.GasLimit,
		Nonce:          q.Nonce,
		AccessList:     q.AccessList,
		Deadline:       q.Deadline,
		IdempotencyKey: q.IdempotencyKey,
	}
	var err error
	if i.Value, err = decimalAmount(q.Value, "value"); err != nil {
		return nil, err
	}
	if i.GasPrice, err = decimalAmount(q.GasPrice, "gas price"); err != nil {
		return nil, err
	}
	if i.MaxFeePerGas, err = decimalAmount(q.MaxFeePerGas, "max fee per gas"); err != nil {
		return nil, err
	}
	if i.MaxPriorityFeePerGas, err = decimalAmount(q.MaxPriorityFeePerGas, "max priority fee per gas"); err != nil {
		return nil, err
	}
	return i, nil
}

// query converts the intent back into a build query. A decimal string
// that does not parse means the record was corrupted or written by
// someone else: an invalid argument, never a chain fact.
func (i *BuildIntent) query() (BuildQuery, error) {
	q := BuildQuery{
		Sender:         i.Sender,
		To:             i.To,
		Data:           i.Data,
		GasLimit:       i.GasLimit,
		Nonce:          i.Nonce,
		AccessList:     i.AccessList,
		Deadline:       i.Deadline,
		IdempotencyKey: i.IdempotencyKey,
	}
	var err error
	if q.Value, err = bigAmount(i.Value, "value"); err != nil {
		return BuildQuery{}, err
	}
	if q.GasPrice, err = bigAmount(i.GasPrice, "gas price"); err != nil {
		return BuildQuery{}, err
	}
	if q.MaxFeePerGas, err = bigAmount(i.MaxFeePerGas, "max fee per gas"); err != nil {
		return BuildQuery{}, err
	}
	if q.MaxPriorityFeePerGas, err = bigAmount(i.MaxPriorityFeePerGas, "max priority fee per gas"); err != nil {
		return BuildQuery{}, err
	}
	return q, nil
}

// decimalAmount renders a wei amount as a decimal string, or "" when
// absent.
func decimalAmount(v *big.Int, what string) (string, error) {
	if v == nil {
		return "", nil
	}
	if v.Sign() < 0 {
		return "", cherrs.New(cherrs.InvalidArgument, "evm.transaction.flow",
			"%s must not be negative", what)
	}
	return v.Text(10), nil
}

// bigAmount reads a decimal string back into wei, or nil when absent.
func bigAmount(raw, what string) (*big.Int, error) {
	if raw == "" {
		return nil, nil
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		return nil, cherrs.New(cherrs.InvalidArgument, "evm.transaction.flow",
			"%s %q is not a decimal amount", what, raw)
	}
	return v, nil
}

// ChainRecord is the adapter-owned payload an EVM transaction record
// carries in ChainData: the intent as built, the nonce the build
// claimed, and the encodings each later phase needs — the recovery
// worker and a resumed flow read them instead of re-asking the chain.
type ChainRecord struct {
	Intent   *BuildIntent  `json:"intent,omitempty"`
	Nonce    *uint64       `json:"nonce,omitempty"`
	Format   signer.Format `json:"format,omitempty"`
	Unsigned []byte        `json:"unsigned,omitempty"`
	Signed   []byte        `json:"signed,omitempty"`
}

// recordOf reads the adapter's record out of the model record. A durable
// store hands chain data back as JSON, so the payload round-trips through
// decode; the concrete pointer is copied so two readers never share one
// working payload. Chain data this adapter did not write is refused, not
// guessed at.
func (n *Network) recordOf(tx txmodel.Transaction) (*ChainRecord, error) {
	switch v := tx.ChainData.(type) {
	case nil:
		return &ChainRecord{}, nil
	case *ChainRecord:
		if v == nil {
			return &ChainRecord{}, nil
		}
		cp := *v
		return &cp, nil
	case json.RawMessage:
		return n.decodeRecord(tx.ID, []byte(v))
	case []byte:
		return n.decodeRecord(tx.ID, v)
	default:
		return nil, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries chain data this adapter did not write", tx.ID))
	}
}

func (n *Network) decodeRecord(id txmodel.TransactionID, raw []byte) (*ChainRecord, error) {
	if len(raw) == 0 {
		return &ChainRecord{}, nil
	}
	rec := &ChainRecord{}
	if err := json.Unmarshal(raw, rec); err != nil {
		return nil, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries unreadable chain data: %v", id, err))
	}
	return rec, nil
}

// Prepare turns the intent the application stored on the record into an
// unsigned payload (spec §9). The build claims a nonce; its own failure
// handling releases that claim, so a prepare that fails leaves no hole
// (§7.5).
func (n *Network) Prepare(ctx context.Context, tx txmodel.Transaction) (txmodel.Prepared, error) {
	rec, err := n.recordOf(tx)
	if err != nil {
		return txmodel.Prepared{}, err
	}
	if rec.Intent == nil {
		return txmodel.Prepared{}, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries no build intent", tx.ID))
	}
	q, err := rec.Intent.query()
	if err != nil {
		return txmodel.Prepared{}, err
	}
	built, err := n.Build(ctx, q)
	if err != nil {
		return txmodel.Prepared{}, err
	}
	nonce := built.Nonce
	rec.Nonce = &nonce
	rec.Format = built.Format
	rec.Unsigned = built.Payload
	return txmodel.Prepared{Payload: built.Payload, ChainData: rec}, nil
}

// Authorize signs the payload with the signer whose own allow-list
// covers the sender (§12.3). A missing cover stops the flow here: a
// payload nobody signs is not a transaction to broadcast.
func (n *Network) Authorize(ctx context.Context, tx txmodel.Transaction, p txmodel.Prepared, signers ...signer.Signer) (txmodel.Authorized, error) {
	rec, err := n.recordOf(tx)
	if err != nil {
		return txmodel.Authorized{}, err
	}
	payload := p.Payload
	if len(payload) == 0 {
		payload = rec.Unsigned
	}
	if len(payload) == 0 {
		return txmodel.Authorized{}, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries no payload to sign", tx.ID))
	}
	if rec.Intent == nil {
		return txmodel.Authorized{}, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries no build intent", tx.ID))
	}
	s := coveringSigner(signers, n.id, rec.Intent.Sender)
	if s == nil {
		return txmodel.Authorized{}, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("no provided signer covers %s", rec.Intent.Sender))
	}
	signed, err := s.Sign(ctx, signer.Request{
		Network: n.id,
		Account: rec.Intent.Sender,
		Type:    signer.SigEVMTransaction,
		Payload: payload,
	})
	if err != nil {
		return txmodel.Authorized{}, err
	}
	// The EVM signer answers with the broadcastable encoding itself.
	rec.Unsigned = payload
	rec.Signed = signed.Signature
	return txmodel.Authorized{Signed: rec.Signed, ChainData: rec}, nil
}

// Dispatch broadcasts the signed encoding and reports the chain's
// reference for it.
func (n *Network) Dispatch(ctx context.Context, tx txmodel.Transaction, a txmodel.Authorized) (string, error) {
	rec, err := n.recordOf(tx)
	if err != nil {
		return "", err
	}
	signed := a.Signed
	if len(signed) == 0 {
		signed = rec.Signed
	}
	if len(signed) == 0 {
		return "", of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries nothing to broadcast", tx.ID))
	}
	rec.Signed = signed
	return n.Submit(ctx, signed)
}

// Settle closes the claim the build made (§7.5). Broadcast — or an
// undecided outcome — spends the nonce, because the bytes may still
// land; a terminal failure without a broadcast releases it. A claim the
// coordinator does not know (an intent that pinned its own nonce) is
// tolerated: settling is bookkeeping, not a precondition.
func (n *Network) Settle(ctx context.Context, tx txmodel.Transaction, outcome txmodel.SettleOutcome) {
	rec, err := n.recordOf(tx)
	if err != nil || rec.Nonce == nil || rec.Intent == nil {
		return
	}
	switch outcome {
	case txmodel.SettleBroadcast:
		_ = n.NonceCoordinator().Submitted(rec.Intent.Sender, *rec.Nonce)
	case txmodel.SettleFailed:
		_ = n.NonceCoordinator().Abandon(rec.Intent.Sender, *rec.Nonce)
	}
}

// coveringSigner answers who may sign for one account: the signer whose
// own allow-list covers it (§12.3). Authorization never comes from the
// caller's word.
func coveringSigner(signers []signer.Signer, network identifiers.NetworkID, account identifiers.Address) signer.Signer {
	for _, s := range signers {
		for _, ref := range s.Accounts() {
			if ref.Network == network && ref.Address.Equal(account) {
				return s
			}
		}
	}
	return nil
}
