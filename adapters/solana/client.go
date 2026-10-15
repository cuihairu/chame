package solana

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// rpcClient is the seam over the concrete JSON-RPC client. Unit tests
// fake it; production uses rpc.Client (spec §16: no real RPC in tests).
// The SDK reaches this interface only from the unexported wrapper below,
// so no SDK type crosses the package boundary (ADR-0001).
type rpcClient interface {
	GenesisHash(ctx context.Context) (solana.Hash, error)
	Slot(ctx context.Context, commitment rpc.CommitmentType) (uint64, error)
	BlockHeight(ctx context.Context, commitment rpc.CommitmentType) (uint64, error)
	LatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (*latestBlockhashResult, error)
	Balance(ctx context.Context, account solana.PublicKey, commitment rpc.CommitmentType) (uint64, error)
	AccountInfo(ctx context.Context, account solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error)
	TokenBalance(ctx context.Context, account solana.PublicKey, commitment rpc.CommitmentType) (*rpc.GetTokenAccountBalanceResult, error)
	TokenAccounts(ctx context.Context, owner solana.PublicKey, conf *rpc.GetTokenAccountsConfig, opts *rpc.GetTokenAccountsOpts) (*rpc.GetTokenAccountsResult, error)
	Transaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error)
	Block(ctx context.Context, slot uint64, opts *rpc.GetBlockOpts) (*rpc.GetBlockResult, error)
	Signatures(ctx context.Context, account solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error)
	SignatureStatus(ctx context.Context, sig solana.Signature, searchHistory bool) ([]*rpc.SignatureStatusesResult, error)
	Close() error
}

// solRPC adapts *rpc.Client to the seam.
type solRPC struct{ c *rpc.Client }

func (s solRPC) GenesisHash(ctx context.Context) (solana.Hash, error) { return s.c.GetGenesisHash(ctx) }

func (s solRPC) Slot(ctx context.Context, commitment rpc.CommitmentType) (uint64, error) {
	return s.c.GetSlot(ctx, commitment)
}

func (s solRPC) BlockHeight(ctx context.Context, commitment rpc.CommitmentType) (uint64, error) {
	return s.c.GetBlockHeight(ctx, commitment)
}

func (s solRPC) LatestBlockhash(ctx context.Context, commitment rpc.CommitmentType) (*latestBlockhashResult, error) {
	return s.c.GetLatestBlockhash(ctx, commitment)
}

func (s solRPC) Balance(ctx context.Context, account solana.PublicKey, commitment rpc.CommitmentType) (uint64, error) {
	out, err := s.c.GetBalance(ctx, account, commitment)
	if err != nil {
		return 0, err
	}
	return out.Value, nil
}

func (s solRPC) AccountInfo(ctx context.Context, account solana.PublicKey, opts *rpc.GetAccountInfoOpts) (*rpc.GetAccountInfoResult, error) {
	return s.c.GetAccountInfoWithOpts(ctx, account, opts)
}

func (s solRPC) TokenBalance(ctx context.Context, account solana.PublicKey, commitment rpc.CommitmentType) (*rpc.GetTokenAccountBalanceResult, error) {
	return s.c.GetTokenAccountBalance(ctx, account, commitment)
}

func (s solRPC) TokenAccounts(ctx context.Context, owner solana.PublicKey, conf *rpc.GetTokenAccountsConfig, opts *rpc.GetTokenAccountsOpts) (*rpc.GetTokenAccountsResult, error) {
	return s.c.GetTokenAccountsByOwner(ctx, owner, conf, opts)
}

func (s solRPC) Transaction(ctx context.Context, sig solana.Signature, opts *rpc.GetTransactionOpts) (*rpc.GetTransactionResult, error) {
	return s.c.GetTransaction(ctx, sig, opts)
}

func (s solRPC) Block(ctx context.Context, slot uint64, opts *rpc.GetBlockOpts) (*rpc.GetBlockResult, error) {
	return s.c.GetBlockWithOpts(ctx, slot, opts)
}

func (s solRPC) Signatures(ctx context.Context, account solana.PublicKey, opts *rpc.GetSignaturesForAddressOpts) ([]*rpc.TransactionSignature, error) {
	return s.c.GetSignaturesForAddressWithOpts(ctx, account, opts)
}

// SignatureStatus reports the per-signature entries of getSignatureStatuses.
// A nil slice with a nil error means the node holds no status for the
// signature at all. searchHistory asks the node to search its ledger rather
// than answer from the recent status cache (spec §8.4).
func (s solRPC) SignatureStatus(ctx context.Context, sig solana.Signature, searchHistory bool) ([]*rpc.SignatureStatusesResult, error) {
	out, err := s.c.GetSignatureStatuses(ctx, searchHistory, sig)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	return out.Value, nil
}

func (s solRPC) Close() error { return s.c.Close() }

// Dialer opens one RPC connection. Tests inject a fake; production dials
// the Solana SDK. The per-request deadline travels in the context, so the
// dialer only needs the URL.
type Dialer func(ctx context.Context, url string) (rpcClient, error)

func defaultDialer(_ context.Context, url string) (rpcClient, error) {
	return solRPC{c: rpc.New(url)}, nil
}

// of builds a classified error, keeping the cause in the chain so
// errors.Is and errors.As reach it.
func of(cat cherrs.Category, op, network string, err error) error {
	return &cherrs.Error{Category: cat, Op: op, Network: network, Err: err}
}

// rpcErrNotFound is the SDK's not-found sentinel, imported once so the
// adapter can map "the node has no such object" to a plain nil result
// instead of an error.
var rpcErrNotFound = rpc.ErrNotFound

// classify maps a raw RPC error onto the taxonomy (spec §14.1). It never
// returns nil for non-nil input. Caller cancellation is not a chain error
// and passes through untouched. The Solana client surfaces network
// problems as net.Error / syscall, HTTP-level problems as *HTTPError, and
// protocol-level problems as *RPCError; each class keeps its own meaning.
func classify(op, network string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return of(cherrs.RequestTimeout, op, network, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return of(cherrs.RequestTimeout, op, network, err)
		}
		return of(cherrs.NetworkUnavailable, op, network, err)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return of(cherrs.NetworkUnavailable, op, network, err)
	}
	var httpErr *jsonrpc.HTTPError
	if errors.As(err, &httpErr) {
		return of(httpStatusCategory(httpErr.Code), op, network, err)
	}
	// A JSON-RPC application error means the node understood and rejected
	// the request. Another endpoint would answer the same way, so it is not
	// a transient and never justifies a failover.
	return of(cherrs.InternalError, op, network, err)
}

// httpStatusCategory maps an HTTP status onto the taxonomy (spec §14.3).
func httpStatusCategory(code int) cherrs.Category {
	switch code {
	case 429:
		return cherrs.RateLimited
	case 401:
		return cherrs.AuthenticationFailed
	case 403:
		return cherrs.PermissionDenied
	}
	// A gateway or node outage may well be gone by the next endpoint, so 5xx
	// is transient; anything else 4xx is a request problem we cannot fix.
	if code >= 500 && code <= 599 {
		return cherrs.NetworkUnavailable
	}
	return cherrs.InternalError
}

// failoverEligible reports whether a classified error justifies trying
// the next endpoint. Only transient classes qualify; argument and
// identity errors are deterministic (spec §7.4: reads may retry safely).
func failoverEligible(err error) bool {
	var ce *cherrs.Error
	if !errors.As(err, &ce) {
		return false
	}
	switch ce.Category {
	case cherrs.NetworkUnavailable, cherrs.RequestTimeout, cherrs.RateLimited:
		return true
	default:
		return false
	}
}
