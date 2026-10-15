package evm

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// fakeClient is the test double for the RPC seam. Unit tests never
// touch a real endpoint (spec §19 Phase 3 acceptance).
type fakeClient struct {
	mu sync.Mutex

	chainID *big.Int
	// errByMethod makes every call of a method fail with this error.
	errByMethod map[string]error

	// Responses.
	header      *types.Header
	blockNumber uint64
	balance     *big.Int
	callReply   []byte
	code        []byte
	logs        []types.Log

	// codeByAddress overrides the code reported for one address, keyed by
	// lowercase hex, so tests can simulate contract and key-backed
	// accounts side by side.
	codeByAddress map[string][]byte

	// selectorReply overrides the reply for one four-byte selector, so
	// tests can answer several functions of one contract at once.
	selectorReply map[string][]byte

	// Captures.
	calls    []ethereum.CallMsg
	queries  []ethereum.FilterQuery
	balances []common.Address
	codes    []common.Address
	closed   bool
	attempts map[string]int
}

func newFakeClient(chainID uint64) *fakeClient {
	return &fakeClient{
		chainID:     big.NewInt(int64(chainID)),
		errByMethod: map[string]error{},
		attempts:    map[string]int{},
	}
}

func (f *fakeClient) fail(method string, err error) *fakeClient {
	f.errByMethod[method] = err
	return f
}

func (f *fakeClient) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts[method]
}

func (f *fakeClient) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeClient) capturedCalls() []ethereum.CallMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ethereum.CallMsg(nil), f.calls...)
}

// withCode pins the code reported for addr.
func (f *fakeClient) withCode(addr common.Address, code []byte) *fakeClient {
	if f.codeByAddress == nil {
		f.codeByAddress = map[string][]byte{}
	}
	f.codeByAddress[addr.Hex()] = code
	return f
}

// withCall pins the reply for one selector, given as hex without the
// 0x prefix.
func (f *fakeClient) withCall(selector string, reply []byte) *fakeClient {
	if f.selectorReply == nil {
		f.selectorReply = map[string][]byte{}
	}
	f.selectorReply[selector] = reply
	return f
}

func (f *fakeClient) capturedQueries() []ethereum.FilterQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ethereum.FilterQuery(nil), f.queries...)
}

func (f *fakeClient) capturedBalances() []common.Address {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]common.Address(nil), f.balances...)
}

func (f *fakeClient) capturedCodes() []common.Address {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]common.Address(nil), f.codes...)
}

func (f *fakeClient) maybeFail(method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts[method]++
	if err, ok := f.errByMethod[method]; ok {
		return err
	}
	return nil
}

func (f *fakeClient) ChainID(ctx context.Context) (*big.Int, error) {
	if err := f.maybeFail("chain_id"); err != nil {
		return nil, err
	}
	return new(big.Int).Set(f.chainID), nil
}

func (f *fakeClient) BlockNumber(ctx context.Context) (uint64, error) {
	if err := f.maybeFail("block_number"); err != nil {
		return 0, err
	}
	return f.blockNumber, nil
}

func (f *fakeClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	if err := f.maybeFail("header_by_number"); err != nil {
		return nil, err
	}
	return f.header, nil
}

func (f *fakeClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	if err := f.maybeFail("balance_at"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.balances = append(f.balances, account)
	bal := f.balance
	f.mu.Unlock()
	if bal == nil {
		return new(big.Int), nil
	}
	return new(big.Int).Set(bal), nil
}

func (f *fakeClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	if err := f.maybeFail("code_at"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.codes = append(f.codes, account)
	code, ok := f.codeByAddress[account.Hex()]
	f.mu.Unlock()
	if !ok {
		return f.code, nil
	}
	return code, nil
}

func (f *fakeClient) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	if err := f.maybeFail("call_contract"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, msg)
	if len(msg.Data) >= 4 {
		if reply, ok := f.selectorReply[hex.EncodeToString(msg.Data[:4])]; ok {
			f.mu.Unlock()
			return reply, nil
		}
	}
	f.mu.Unlock()
	return f.callReply, nil
}

func (f *fakeClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if err := f.maybeFail("filter_logs"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	return f.logs, nil
}

func (f *fakeClient) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

// blockingClient blocks until the context is done, simulating a hung
// endpoint.
type blockingClient struct {
	*fakeClient
}

func (b *blockingClient) ChainID(ctx context.Context) (*big.Int, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

// netErr implements net.Error for classification tests.
type netErr struct {
	msg     string
	timeout bool
}

func (e netErr) Error() string   { return e.msg }
func (e netErr) Timeout() bool   { return e.timeout }
func (e netErr) Temporary() bool { return true }

// dataErr carries structured revert data, like a reverted eth_call.
type dataErr struct{ data any }

func (e dataErr) Error() string  { return "execution reverted" }
func (e dataErr) ErrorData() any { return e.data }

// fakeDialer returns the client registered for a URL, or an error.
func fakeDialer(clients map[string]rpcClient, dialErr error) Dialer {
	return func(ctx context.Context, url string) (rpcClient, error) {
		if dialErr != nil {
			return nil, dialErr
		}
		c, ok := clients[url]
		if !ok {
			return nil, fmt.Errorf("no fake client for %s", url)
		}
		return c, nil
	}
}
