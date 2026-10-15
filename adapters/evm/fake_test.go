package evm

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

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

	// receiptsByHash pins the receipt served for a transaction hash,
	// keyed by lowercase hex, so tests can serve mined and reverted
	// outcomes side by side.
	receiptsByHash map[string]*types.Receipt

	// pooledByHash pins a transaction the node still holds in its
	// mempool, so tests can tell "not mined yet" from "never known".
	pooledByHash map[string]*types.Transaction

	sentTxs []*types.Transaction

	// headersByNumber overrides the header reported for one height, so
	// tests can serve several distinct blocks side by side.
	headersByNumber map[uint64]*types.Header

	// codeByAddress overrides the code reported for one address, keyed by
	// lowercase hex, so tests can simulate contract and key-backed
	// accounts side by side.
	codeByAddress map[string][]byte

	// balanceByAddress overrides the balance reported for one address,
	// keyed by lowercase hex, so tests can simulate funded and empty
	// accounts side by side.
	balanceByAddress map[string]*big.Int

	// pendingNonceByAddress overrides the next unconsumed nonce reported
	// for one address, keyed by lowercase hex, so tests can simulate
	// accounts with on-chain history and advance them as transactions
	// land.
	pendingNonceByAddress map[string]uint64

	// selectorReply overrides the reply for one four-byte selector, so
	// tests can answer several functions of one contract at once.
	selectorReply map[string][]byte

	// estimateGas pins the gas a node estimates for a call, and
	// estimateCalls captures the queries that asked for it.
	estimateGas   uint64
	estimateCalls []ethereum.CallMsg
	// gasPrice and gasTipCap pin the fee suggestion a node reports.
	gasPrice  *big.Int
	gasTipCap *big.Int
	// slowEstimate makes every estimate block for this long, so a test
	// can watch a deadline expire mid-build.
	slowEstimate time.Duration

	// Captures.
	calls    []ethereum.CallMsg
	queries  []ethereum.FilterQuery
	balances []common.Address
	codes    []common.Address
	nonces   []common.Address
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

// clear lifts a pinned failure, so a test can watch a node recover.
func (f *fakeClient) clear(method string) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.errByMethod, method)
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

// withBalance pins the balance reported for addr.
func (f *fakeClient) withBalance(addr common.Address, bal *big.Int) *fakeClient {
	if f.balanceByAddress == nil {
		f.balanceByAddress = map[string]*big.Int{}
	}
	f.balanceByAddress[addr.Hex()] = bal
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

// withPendingNonce pins the next unconsumed nonce reported for addr, so
// tests can move the account forward as transactions land.
func (f *fakeClient) withPendingNonce(addr common.Address, n uint64) *fakeClient {
	if f.pendingNonceByAddress == nil {
		f.pendingNonceByAddress = map[string]uint64{}
	}
	f.pendingNonceByAddress[addr.Hex()] = n
	return f
}

// withBlock pins the header reported for height and returns its derived
// hash, so tests can assert exact block positions and build log records
// that agree with the chain.
func (f *fakeClient) withBlock(height uint64, parent common.Hash) string {
	h := &types.Header{Number: new(big.Int).SetUint64(height), ParentHash: parent}
	if f.headersByNumber == nil {
		f.headersByNumber = map[uint64]*types.Header{}
	}
	f.headersByNumber[height] = h
	return h.Hash().Hex()
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

func (f *fakeClient) capturedNonces() []common.Address {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]common.Address(nil), f.nonces...)
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
	var n uint64
	if number != nil {
		n = number.Uint64()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if h, ok := f.headersByNumber[n]; ok {
		return h, nil
	}
	return f.header, nil
}

func (f *fakeClient) BalanceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (*big.Int, error) {
	if err := f.maybeFail("balance_at"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.balances = append(f.balances, account)
	bal, ok := f.balanceByAddress[account.Hex()]
	if !ok {
		bal = f.balance
	}
	f.mu.Unlock()
	if bal == nil {
		return new(big.Int), nil
	}
	return new(big.Int).Set(bal), nil
}

func (f *fakeClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	if err := f.maybeFail("pending_nonce_at"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.nonces = append(f.nonces, account)
	n := f.pendingNonceByAddress[account.Hex()]
	f.mu.Unlock()
	return n, nil
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

// FilterLogs returns the pinned logs inside the query's block range,
// like a node does: the event source's range discipline depends on the
// node filtering by FromBlock/ToBlock.
func (f *fakeClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	if err := f.maybeFail("filter_logs"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.queries = append(f.queries, q)
	f.mu.Unlock()
	out := make([]types.Log, 0, len(f.logs))
	for _, l := range f.logs {
		if q.FromBlock != nil && l.BlockNumber < q.FromBlock.Uint64() {
			continue
		}
		if q.ToBlock != nil && l.BlockNumber > q.ToBlock.Uint64() {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// withReceipt pins the receipt served for a transaction hash.
func (f *fakeClient) withReceipt(hash string, r *types.Receipt) *fakeClient {
	if f.receiptsByHash == nil {
		f.receiptsByHash = map[string]*types.Receipt{}
	}
	f.receiptsByHash[strings.ToLower(hash)] = r
	return f
}

// withPooled pins a transaction the node still holds in its mempool,
// so a receipt lookup reports "not mined yet" instead of "never known".
func (f *fakeClient) withPooled(hash string, tx *types.Transaction) *fakeClient {
	if f.pooledByHash == nil {
		f.pooledByHash = map[string]*types.Transaction{}
	}
	f.pooledByHash[strings.ToLower(hash)] = tx
	return f
}

func (f *fakeClient) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if err := f.maybeFail("send_transaction"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentTxs = append(f.sentTxs, tx)
	return nil
}

func (f *fakeClient) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	if err := f.maybeFail("transaction_by_hash"); err != nil {
		return nil, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	tx, pooled := f.pooledByHash[hash.Hex()]
	if !pooled {
		return nil, false, nil
	}
	return tx, true, nil
}

func (f *fakeClient) TransactionReceipt(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	if err := f.maybeFail("transaction_receipt"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.receiptsByHash[hash.Hex()], nil
}

func (f *fakeClient) capturedSent() []*types.Transaction {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*types.Transaction(nil), f.sentTxs...)
}

// withEstimateGas pins the gas a node estimates for a call.
func (f *fakeClient) withEstimateGas(gas uint64) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.estimateGas = gas
	return f
}

// withGasPrice pins the gas price a node suggests.
func (f *fakeClient) withGasPrice(price *big.Int) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gasPrice = price
	return f
}

// withGasTipCap pins the priority fee a node suggests. A node that
// cannot name one must fail "suggest_gas_tip_cap" instead.
func (f *fakeClient) withGasTipCap(tip *big.Int) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gasTipCap = tip
	return f
}

// withSlowEstimate makes every estimate block for this long.
func (f *fakeClient) withSlowEstimate(d time.Duration) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slowEstimate = d
	return f
}

func (f *fakeClient) capturedEstimates() []ethereum.CallMsg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ethereum.CallMsg(nil), f.estimateCalls...)
}

// EstimateGas answers the pinned estimate after any configured delay,
// like a node thinking about a call.
func (f *fakeClient) EstimateGas(ctx context.Context, msg ethereum.CallMsg) (uint64, error) {
	if err := f.maybeFail("estimate_gas"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.estimateCalls = append(f.estimateCalls, msg)
	d, gas := f.slowEstimate, f.estimateGas
	f.mu.Unlock()
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return gas, nil
}

func (f *fakeClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	if err := f.maybeFail("suggest_gas_price"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gasPrice == nil {
		return nil, errors.New("no gas price pinned")
	}
	return new(big.Int).Set(f.gasPrice), nil
}

// SuggestGasTipCap reports no priority fee on a node that does not run
// EIP-1559, the way a legacy chain's node answers the request.
func (f *fakeClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	if err := f.maybeFail("suggest_gas_tip_cap"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gasTipCap == nil {
		return nil, errors.New("maxPriorityFeePerGas is not supported")
	}
	return new(big.Int).Set(f.gasTipCap), nil
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
