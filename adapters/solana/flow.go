package solana

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cuihairu/chame/capability/signer"
	txmodel "github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
)

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
// unsigned message (spec §8.2 §8.3). The build reads a fresh blockhash
// window, which lands on the record with the payload — a flow that dies
// after this point leaves the window behind for its recovery (§8.4).
func (n *Network) Prepare(ctx context.Context, tx txmodel.Transaction) (txmodel.Prepared, error) {
	rec, err := n.recordOf(tx)
	if err != nil {
		return txmodel.Prepared{}, err
	}
	if rec.Intent == nil {
		return txmodel.Prepared{}, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries no build intent", tx.ID))
	}
	built, err := n.Build(ctx, *rec.Intent)
	if err != nil {
		return txmodel.Prepared{}, err
	}
	rec.Pin = built.Pin
	rec.Commitment = built.Commitment
	rec.Signers = built.Signers
	rec.Unsigned = built.Payload
	return txmodel.Prepared{Payload: built.Payload, ChainData: rec}, nil
}

// Authorize signs the message with the signers whose own allow-lists
// cover the required set, in build order (§12.3): the fee payer signs
// first. A signature hole stops the flow before anything is broadcast.
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
	if len(rec.Signers) == 0 {
		return txmodel.Authorized{}, of(cherrs.InvalidArgument, n.opBase+".flow", n.id.String(),
			fmt.Errorf("record %s carries no required signers", tx.ID))
	}
	signed, err := n.Sign(ctx, Built{
		Payload:    payload,
		Format:     signer.FormatSolanaV0,
		Signers:    rec.Signers,
		Pin:        rec.Pin,
		Commitment: rec.Commitment,
	}, signers...)
	if err != nil {
		return txmodel.Authorized{}, err
	}
	rec.Unsigned = payload
	rec.Signed = signed
	return txmodel.Authorized{Signed: signed, ChainData: rec}, nil
}

// Dispatch broadcasts the signed transaction and reports the signature
// that names it on chain.
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

// Settle is a no-op: Solana claims nothing across steps — the blockhash
// window is recorded on the record, not leased from the adapter — so
// there is nothing to spend or release.
func (n *Network) Settle(ctx context.Context, tx txmodel.Transaction, outcome txmodel.SettleOutcome) {
}
