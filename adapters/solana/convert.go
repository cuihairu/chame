package solana

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// SDK result and option types, aliased once so the query methods can
// name them without importing the SDK at each call site. None of these
// names are exported, so they stay inside the package (ADR-0001).
type (
	rpcCommitmentType              = rpc.CommitmentType
	accountResult                  = rpc.GetAccountInfoResult
	rpcGetAccountInfoOpts          = rpc.GetAccountInfoOpts
	tokenBalanceResult             = rpc.GetTokenAccountBalanceResult
	tokenAccountsResult            = rpc.GetTokenAccountsResult
	rpcGetTokenAccountsConfig      = rpc.GetTokenAccountsConfig
	rpcGetTokenAccountsOpts        = rpc.GetTokenAccountsOpts
	transactionResult              = rpc.GetTransactionResult
	rpcGetTransactionOpts          = rpc.GetTransactionOpts
	rpcTransactionSignature        = rpc.TransactionSignature
	rpcGetSignaturesForAddressOpts = rpc.GetSignaturesForAddressOpts
	rpcSignatureStatusesResult     = rpc.SignatureStatusesResult
)

// toPublicKey converts a chain-agnostic address to the SDK key type. The
// address is revalidated before decoding: an invalid family or value is an
// argument error, never a decode panic.
func toPublicKey(op string, a identifiers.Address) (solana.PublicKey, error) {
	if a.Family != identifiers.FamilySolana {
		return solana.PublicKey{}, cherrs.New(cherrs.InvalidArgument, op,
			"address family %q is not solana", a.Family)
	}
	if err := a.Validate(); err != nil {
		return solana.PublicKey{}, cherrs.New(cherrs.InvalidArgument, op, "%v", err)
	}
	pk, err := solana.PublicKeyFromBase58(a.Value)
	if err != nil {
		return solana.PublicKey{}, cherrs.New(cherrs.InvalidArgument, op,
			"invalid solana address: %v", err)
	}
	return pk, nil
}

// toAddress converts an SDK public key to the chain-agnostic address. The
// SDK's base58 rendering is canonical, so the resulting value revalidates.
func toAddress(op string, pk solana.PublicKey) (identifiers.Address, error) {
	a, err := identifiers.NewSolanaAddress(pk.String())
	if err != nil {
		return identifiers.Address{}, cherrs.New(cherrs.EventDecodeFailure, op,
			"invalid public key from node: %v", err)
	}
	return a, nil
}

// toSignature converts a base58 transaction signature.
func toSignature(op, sig string) (solana.Signature, error) {
	if sig == "" {
		return solana.Signature{}, cherrs.New(cherrs.InvalidArgument, op, "signature must not be empty")
	}
	s, err := solana.SignatureFromBase58(sig)
	if err != nil {
		return solana.Signature{}, cherrs.New(cherrs.InvalidArgument, op,
			"invalid signature %q: %v", sig, err)
	}
	return s, nil
}

// notFound reports whether the node answered that no such object exists.
// A missing account, transaction or signature is a query outcome, not a
// failure (spec §8.2): the adapter maps it to a nil result so callers can
// tell "absent" apart from "unreachable" and "failed".
func notFound(err error) bool {
	return errors.Is(err, rpcErrNotFound)
}

// sigString renders a signature as base58.
func sigString(s solana.Signature) string {
	b, err := s.MarshalText()
	if err != nil {
		return ""
	}
	return string(b)
}

// errDetail renders a node-reported error. The RPC reports it as a typed
// JSON object or a string; both keep their meaning, nothing is dropped
// (spec §7.4: conflicting data must not be quietly resolved).
func errDetail(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// uint64RentEpoch reads an optional *big.Int epoch.
func uint64RentEpoch(v *big.Int) uint64 {
	if v == nil {
		return 0
	}
	if v.Sign() < 0 {
		return 0
	}
	return v.Uint64()
}

// uint64OrZero reads an optional pointer.
func uint64OrZero(v *uint64) uint64 {
	if v == nil {
		return 0
	}
	return *v
}

// capabilityNetwork is the single capability family the read-only adapter
// publishes.
const capabilityNetwork = capability.Network
