package evm

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/signer"
	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

const txReceiver = "0x00000000000000000000000000000000000000bb"

// devKeyRefFor builds one allow-list entry for the dev key on a named
// network.
func devKeyRefFor(t *testing.T, name, chainID string) signer.AccountRef {
	t.Helper()
	ref, err := signer.NewAccountRef(
		identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: name, ChainID: chainID},
		mustEVMAddress(devKeyAddr),
	)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	return ref
}

// legacyPayload builds an unsigned legacy transaction. A legacy unsigned
// encoding carries no chain id — the chain id is bound by the signature
// at signing time, from the network the request names (spec §7.6).
func legacyPayload(t *testing.T, nonce uint64) []byte {
	t.Helper()
	receiver := common.HexToAddress(txReceiver)
	return marshalTx(t, types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &receiver,
		Value:    big.NewInt(1000),
		Gas:      21000,
		GasPrice: big.NewInt(20_000_000_000),
	}))
}

// dynamicPayload builds an unsigned EIP-1559 transaction. A zero
// chainID builds a payload that declares no chain, which the signer
// must refuse.
func dynamicPayload(t *testing.T, chainID uint64, nonce uint64) []byte {
	t.Helper()
	chainIDField := new(big.Int).SetUint64(chainID)
	receiver := common.HexToAddress(txReceiver)
	return marshalTx(t, types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainIDField,
		Nonce:     nonce,
		To:        &receiver,
		Value:     big.NewInt(500),
		Gas:       21000,
		GasTipCap: big.NewInt(1_000_000_000),
		GasFeeCap: big.NewInt(30_000_000_000),
	}))
}

// accessListPayload builds an unsigned EIP-2930 transaction, a type the
// dev signer does not declare.
func accessListPayload(t *testing.T, chainID uint64) []byte {
	t.Helper()
	receiver := common.HexToAddress(txReceiver)
	return marshalTx(t, types.NewTx(&types.AccessListTx{
		ChainID: new(big.Int).SetUint64(chainID),
		Nonce:   3,
		To:      &receiver,
		Gas:     21000,
	}))
}

func marshalTx(t *testing.T, tx *types.Transaction) []byte {
	t.Helper()
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// decodeSigned decodes a signing reply and returns the decoded tx plus
// the sender the signature recovers to, so a test can assert the
// signature really belongs to the key.
func decodeSigned(t *testing.T, chainID uint64, raw []byte) (*types.Transaction, common.Address) {
	t.Helper()
	var got types.Transaction
	if err := got.UnmarshalBinary(raw); err != nil {
		t.Fatalf("decode reply: %v", err)
	}
	sender, err := types.Sender(types.LatestSignerForChainID(new(big.Int).SetUint64(chainID)), &got)
	if err != nil {
		t.Fatalf("recover sender: %v", err)
	}
	return &got, sender
}

func txRequest(name, chainID string, payload []byte) signer.Request {
	return signer.Request{
		Network: identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: name, ChainID: chainID},
		Account: mustEVMAddress(devKeyAddr),
		Type:    signer.SigEVMTransaction,
		Payload: payload,
	}
}

func TestLocalDevSignerSignsLegacyTransaction(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))

	out, err := s.Sign(context.Background(), txRequest("mainnet", "1", legacyPayload(t, 5)))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if out.Signer != "evm-dev" {
		t.Errorf("signer = %q, want evm-dev", out.Signer)
	}
	got, sender := decodeSigned(t, 1, out.Signature)
	if sender != common.HexToAddress(devKeyAddr) {
		t.Errorf("sender = %s, want the dev key address", sender.Hex())
	}
	if got.Nonce() != 5 || got.To() == nil || *got.To() != common.HexToAddress(txReceiver) {
		t.Errorf("nonce = %d, to = %v; want 5 and the receiver", got.Nonce(), got.To())
	}
	if got.Type() != types.LegacyTxType {
		t.Errorf("tx type = %d, want legacy", got.Type())
	}
	if got.ChainId().Uint64() != 1 {
		t.Errorf("chain id = %s, want 1", got.ChainId())
	}
}

func TestLocalDevSignerSignsDynamicFeeTransaction(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devKeyRefFor(t, "base", "8453"))

	out, err := s.Sign(context.Background(), txRequest("base", "8453", dynamicPayload(t, 8453, 9)))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, sender := decodeSigned(t, 8453, out.Signature)
	if sender != common.HexToAddress(devKeyAddr) {
		t.Errorf("sender = %s, want the dev key address", sender.Hex())
	}
	if got.Type() != types.DynamicFeeTxType {
		t.Errorf("tx type = %d, want dynamic fee", got.Type())
	}
	if got.Nonce() != 9 {
		t.Errorf("nonce = %d, want 9", got.Nonce())
	}
}

func TestLocalDevSignerRefusesSignedPayload(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	first, err := s.Sign(context.Background(), txRequest("mainnet", "1", legacyPayload(t, 5)))
	if err != nil {
		t.Fatalf("first Sign: %v", err)
	}
	// Handing a signed transaction back must fail: a signature is not
	// renewed, only produced once over the unsigned bytes.
	req := txRequest("mainnet", "1", first.Signature)
	if _, err := s.Sign(context.Background(), req); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("err = %v, want invalid request", err)
	}
}

func TestLocalDevSignerRefusesWrongChainID(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	// Built for another chain: signing it would deliver it somewhere
	// the request does not name, so it is refused.
	if _, err := s.Sign(context.Background(), txRequest("mainnet", "1", dynamicPayload(t, 5, 1))); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("foreign chain id: err = %v, want invalid request", err)
	}
	// Declares no chain at all: the signer cannot prove the
	// transaction's target, so it refuses rather than guessing.
	if _, err := s.Sign(context.Background(), txRequest("mainnet", "1", dynamicPayload(t, 0, 1))); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("missing chain id: err = %v, want invalid request", err)
	}
}

func TestLocalDevSignerRefusesUndeclaredFormat(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	if _, err := s.Sign(context.Background(), txRequest("mainnet", "1", accessListPayload(t, 1))); !errors.Is(err, signer.ErrUnsupportedType) {
		t.Errorf("err = %v, want unsupported type", err)
	}
}

func TestLocalDevSignerRefusesAnotherSender(t *testing.T) {
	other := mustEVMAddress(txReceiver)
	ref, err := signer.NewAccountRef(
		identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "mainnet", ChainID: "1"},
		other,
	)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	// The allow-list may name the other account, but the key signs only
	// its own address: a signature over the wrong sender's transaction
	// would not belong to it on chain.
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t), ref)
	req := txRequest("mainnet", "1", legacyPayload(t, 1))
	req.Account = other
	if _, err := s.Sign(context.Background(), req); !errors.Is(err, signer.ErrUnauthorized) {
		t.Errorf("err = %v, want unauthorized: one held key cannot sign for another sender", err)
	}
}

func TestLocalDevSignerRejectsMalformedTransaction(t *testing.T) {
	s := devSigner(t, config.EnvDevelopment, devAccountRef(t))
	if _, err := s.Sign(context.Background(), txRequest("mainnet", "1", []byte("not a transaction"))); !errors.Is(err, signer.ErrInvalidRequest) {
		t.Errorf("err = %v, want invalid request", err)
	}
}

func TestLocalDevSignerDeclaresTransactionSigning(t *testing.T) {
	d := devSigner(t, config.EnvDevelopment, devAccountRef(t)).Descriptor()
	if !d.SupportsType(signer.SigEVMTransaction) || !d.SupportsType(signer.SigEVMMessage) {
		t.Errorf("types = %v, want both message and transaction signing", d.SignatureTypes)
	}
	formats := map[signer.Format]bool{}
	for _, f := range d.Formats {
		formats[f] = true
	}
	if !formats[signer.FormatEIP1559] || !formats[signer.FormatEIP155] {
		t.Errorf("formats = %v, want EIP-1559 and EIP-155", d.Formats)
	}
}
