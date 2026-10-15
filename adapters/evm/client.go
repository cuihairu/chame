package evm

import (
	"context"
	"errors"
	"math/big"
	"net"
	"os"
	"syscall"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"

	cherrs "github.com/cuihairu/chame/core/errors"
)

// rpcClient is the seam over the concrete JSON-RPC client. Unit tests
// fake it; production uses ethclient (spec §16: no real RPC in tests).
type rpcClient interface {
	ChainID(ctx context.Context) (*big.Int, error)
	BlockNumber(ctx context.Context) (uint64, error)
	HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
	BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error)
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error)
	CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
	FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error)
	Close()
}

// ethRPC adapts *ethclient.Client to the seam.
type ethRPC struct{ c *ethclient.Client }

func (e ethRPC) ChainID(ctx context.Context) (*big.Int, error) { return e.c.ChainID(ctx) }
func (e ethRPC) BlockNumber(ctx context.Context) (uint64, error) {
	return e.c.BlockNumber(ctx)
}
func (e ethRPC) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return e.c.HeaderByNumber(ctx, number)
}
func (e ethRPC) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	return e.c.BalanceAt(ctx, account, blockNumber)
}
func (e ethRPC) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return e.c.PendingNonceAt(ctx, account)
}
func (e ethRPC) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	return e.c.CodeAt(ctx, account, blockNumber)
}
func (e ethRPC) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	return e.c.CallContract(ctx, msg, blockNumber)
}
func (e ethRPC) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return e.c.FilterLogs(ctx, q)
}
func (e ethRPC) Close() { e.c.Close() }

// Dialer opens one RPC connection. Tests inject a fake; production
// dials through ethclient.
type Dialer func(ctx context.Context, url string) (rpcClient, error)

func defaultDialer(ctx context.Context, url string) (rpcClient, error) {
	c, err := ethclient.DialContext(ctx, url)
	if err != nil {
		return nil, err
	}
	return ethRPC{c: c}, nil
}

// of builds a classified error, keeping the cause in the chain so
// errors.Is and errors.As reach it.
func of(cat cherrs.Category, op, network string, err error) error {
	return &cherrs.Error{Category: cat, Op: op, Network: network, Err: err}
}

// classify maps a raw RPC error onto the taxonomy (spec §14.1). It
// never returns nil for non-nil input. Caller cancellation is not a
// chain error and passes through untouched.
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
	var httpErr *rpc.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 429:
			return of(cherrs.RateLimited, op, network, err)
		case 401:
			return of(cherrs.AuthenticationFailed, op, network, err)
		case 403:
			return of(cherrs.PermissionDenied, op, network, err)
		default:
			return of(cherrs.NetworkUnavailable, op, network, err)
		}
	}
	// A reverted eth_call carries structured data; it is an execution
	// failure, not an internal one.
	var dataErr interface{ ErrorData() any }
	if errors.As(err, &dataErr) {
		return of(cherrs.TransactionExecutionFailed, op, network, err)
	}
	return of(cherrs.InternalError, op, network, err)
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
