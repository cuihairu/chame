// Package devchain provides a self-contained mock EVM JSON-RPC chain
// for load testing. It speaks the HTTP JSON-RPC surface the chame EVM
// adapter actually uses (chain id, block number, fee suggestions,
// pending nonce, gas estimate, raw submission, receipts), mines
// submitted transactions into blocks on an interval, and injects
// faults per endpoint: RPC error rate, dropped transactions, reverted
// receipts, added latency, and a hard down switch.
//
// It is the mock RPC path the txload tool runs against locally: the
// adapter dials it with its production ethclient dialer, so endpoint
// pooling, failover, identity verification, and the transaction state
// machine all run the real code paths (spec §16: no real RPC in tests
// — a local mock is the same discipline at tool scale).
package devchain

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// bigInt is a uint64 as *big.Int for header and receipt fields.
func bigInt(v uint64) *big.Int { return new(big.Int).SetUint64(v) }

// Faults is one endpoint's injected failure profile.
type Faults struct {
	// Down refuses every request with a 503: an endpoint outage the
	// pool must fail over from.
	Down bool `json:"down"`
	// ErrorRate is the probability any one call answers with a
	// JSON-RPC server error (transient, failover-eligible).
	ErrorRate float64 `json:"error_rate"`
	// DropRate is the probability a submitted transaction is accepted
	// but never mined: the submission "succeeds" and the outcome stays
	// open, which drives records into unknown/timeout paths.
	DropRate float64 `json:"drop_rate"`
	// RevertRate is the probability a mined transaction executes with
	// a failing result (terminal reverted).
	RevertRate float64 `json:"revert_rate"`
	// LatencyMS is added to every response.
	LatencyMS int `json:"latency_ms"`
}

// Validate rejects profiles that cannot run.
func (f Faults) Validate() error {
	for _, r := range []struct {
		name string
		v    float64
	}{{"error_rate", f.ErrorRate}, {"drop_rate", f.DropRate}, {"revert_rate", f.RevertRate}} {
		if r.v < 0 || r.v > 1 {
			return fmt.Errorf("devchain: %s must be within [0,1], got %v", r.name, r.v)
		}
	}
	if f.LatencyMS < 0 {
		return fmt.Errorf("devchain: latency_ms must not be negative")
	}
	return nil
}

// receipt is the chain's record of one mined transaction.
type receipt struct {
	blockNumber uint64
	blockHash   common.Hash
	txIndex     uint64
	gasUsed     uint64
	status      uint64
	tx          *types.Transaction
}

// Chain is the shared ledger behind one or more mock endpoints. Every
// endpoint serves the same state — failing one over never changes what
// the chain knows, which is the semantics the endpoint pool relies on.
type Chain struct {
	chainID   uint64
	blockTime time.Duration
	gasPrice  int64

	mu           sync.Mutex
	blocks       []common.Hash
	blockTx      [][]common.Hash
	receipts     map[common.Hash]*receipt
	pooled       map[common.Hash]*types.Transaction
	senders      map[common.Hash]common.Address
	pending      []common.Hash
	accountNonce map[common.Address]uint64
	dropped      map[common.Hash]bool
	revertRate   float64

	stop chan struct{}
	done sync.WaitGroup
}

// NewChain builds a chain with a mined genesis and starts its block
// production. Stop stops the miner.
func NewChain(chainID uint64, blockTime time.Duration) *Chain {
	if blockTime <= 0 {
		blockTime = 250 * time.Millisecond
	}
	c := &Chain{
		chainID:      chainID,
		blockTime:    blockTime,
		gasPrice:     1_000_000_000,
		blocks:       []common.Hash{common.HexToHash(fmt.Sprintf("0x%064x", chainID))},
		blockTx:      [][]common.Hash{nil},
		receipts:     map[common.Hash]*receipt{},
		pooled:       map[common.Hash]*types.Transaction{},
		senders:      map[common.Hash]common.Address{},
		accountNonce: map[common.Address]uint64{},
		dropped:      map[common.Hash]bool{},
		stop:         make(chan struct{}),
	}
	c.done.Add(1)
	go c.mine()
	return c
}

// Stop halts block production.
func (c *Chain) Stop() {
	close(c.stop)
	c.done.Wait()
}

// Head returns the latest block number.
func (c *Chain) Head() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return uint64(len(c.blocks) - 1)
}

// PendingCount reports transactions accepted but not yet mined (the
// drop-injection population included).
func (c *Chain) PendingCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.pending)
}

func (c *Chain) mine() {
	defer c.done.Done()
	ticker := time.NewTicker(c.blockTime)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		if len(c.pending) == 0 {
			c.mu.Unlock()
			continue
		}
		number := uint64(len(c.blocks))
		hash := common.BytesToHash(crypto.Keccak256(
			[]byte(fmt.Sprintf("block:%d:%d", number, time.Now().UnixNano()))))
		var txs []common.Hash
		for i, h := range c.pending {
			tx := c.pooled[h]
			status := uint64(1)
			if rand.Float64() < c.dropRevertRateLocked() {
				status = 0
			}
			c.receipts[h] = &receipt{
				blockNumber: number,
				blockHash:   hash,
				txIndex:     uint64(i),
				gasUsed:     21_000,
				status:      status,
				tx:          tx,
			}
			txs = append(txs, h)
		}
		for _, h := range c.pending {
			delete(c.pooled, h)
		}
		c.pending = nil
		c.blocks = append(c.blocks, hash)
		c.blockTx = append(c.blockTx, txs)
		c.mu.Unlock()
	}
}

// dropRevertRateLocked reads the mining-time revert probability. The
// chain samples per mined transaction; the first endpoint's fault
// profile owns it (the rate is a chain-level property in this mock).
func (c *Chain) dropRevertRateLocked() float64 { return c.revertRate }

// SetRevertRate sets the chain-level revert probability.
func (c *Chain) SetRevertRate(r float64) {
	c.mu.Lock()
	c.revertRate = r
	c.mu.Unlock()
}

// Node is one endpoint serving the chain: the chain's state plus its
// own fault profile.
type Node struct {
	chain  *Chain
	faults Faults
	mu     sync.Mutex
}

// NewNode attaches a serving endpoint to the chain. The revert rate is
// a chain-level property, so the initial profile applies it here.
func (c *Chain) NewNode(faults Faults) *Node {
	if faults.RevertRate > 0 {
		c.SetRevertRate(faults.RevertRate)
	}
	return &Node{chain: c, faults: faults}
}

// Faults snapshots the node's current fault profile.
func (n *Node) Faults() Faults {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.faults
}

// SetFaults replaces the node's fault profile (live failure injection).
func (n *Node) SetFaults(f Faults) error {
	if err := f.Validate(); err != nil {
		return err
	}
	n.mu.Lock()
	n.faults = f
	n.mu.Unlock()
	n.chain.SetRevertRate(f.RevertRate)
	return nil
}

// ServeHTTP implements the JSON-RPC endpoint with the fault middleware.
func (n *Node) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	f := n.faults
	n.mu.Unlock()
	if f.Down {
		http.Error(w, "mock endpoint down", http.StatusServiceUnavailable)
		return
	}
	if f.LatencyMS > 0 {
		time.Sleep(time.Duration(f.LatencyMS) * time.Millisecond)
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var reqs []rpcRequest
	if len(body) > 0 && body[0] == '[' {
		if err := json.Unmarshal(body, &reqs); err != nil {
			writeRPC(w, rpcResponse{Version: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			return
		}
	} else {
		var one rpcRequest
		if err := json.Unmarshal(body, &one); err != nil {
			writeRPC(w, rpcResponse{Version: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
			return
		}
		reqs = []rpcRequest{one}
	}
	out := make([]rpcResponse, 0, len(reqs))
	for _, req := range reqs {
		out = append(out, n.handle(req))
	}
	w.Header().Set("Content-Type", "application/json")
	if len(reqs) == 1 {
		writeRPC(w, out[0])
		return
	}
	_ = json.NewEncoder(w).Encode(out)
}

type rpcRequest struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Version string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	resp.Version = "2.0"
	_ = json.NewEncoder(w).Encode(resp)
}

func fail(req rpcRequest, code int, format string, args ...any) rpcResponse {
	return rpcResponse{Version: "2.0", ID: req.ID, Error: &rpcError{Code: code, Message: fmt.Sprintf(format, args...)}}
}

func result(req rpcRequest, v any) rpcResponse {
	raw, err := json.Marshal(v)
	if err != nil {
		return fail(req, -32603, "marshal result: %v", err)
	}
	return rpcResponse{Version: "2.0", ID: req.ID, Result: raw}
}

func quantity(v uint64) string { return hexutil.EncodeUint64(v) }

// handle dispatches one call. The injected error rate applies to every
// method: any endpoint can fail any read, which is what the pool's
// failover treats as transient.
func (n *Node) handle(req rpcRequest) rpcResponse {
	c := n.chain
	n.mu.Lock()
	f := n.faults
	n.mu.Unlock()
	if rand.Float64() < f.ErrorRate {
		return fail(req, -32000, "injected endpoint failure")
	}

	var params []json.RawMessage
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return fail(req, -32602, "bad params: %v", err)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	switch req.Method {
	case "eth_chainId":
		return result(req, quantity(c.chainID))
	case "net_version":
		return result(req, fmt.Sprintf("%d", c.chainID))
	case "eth_blockNumber":
		return result(req, quantity(uint64(len(c.blocks)-1)))
	case "eth_gasPrice":
		return result(req, quantity(uint64(c.gasPrice)))
	case "eth_maxPriorityFeePerGas":
		return result(req, quantity(uint64(c.gasPrice)))
	case "eth_getBlockByNumber":
		var number uint64
		if len(params) == 0 {
			return fail(req, -32602, "getBlockByNumber needs a block number")
		}
		var tag string
		if err := json.Unmarshal(params[0], &tag); err != nil {
			return fail(req, -32602, "bad block tag: %v", err)
		}
		switch tag {
		case "latest", "safe", "finalized", "pending":
			number = uint64(len(c.blocks) - 1)
		default:
			parsed, err := hexutil.DecodeUint64(tag)
			if err != nil {
				return fail(req, -32602, "bad block number %q: %v", tag, err)
			}
			number = parsed
		}
		if number >= uint64(len(c.blocks)) {
			return result(req, nil)
		}
		parent := common.Hash{}
		if number > 0 {
			parent = c.blocks[number-1]
		}
		header := &types.Header{
			ParentHash: parent,
			Number:     bigInt(number),
			GasLimit:   30_000_000,
			Time:       uint64(time.Now().Unix()),
		}
		return result(req, header)
	case "eth_getTransactionCount":
		if len(params) < 1 {
			return fail(req, -32602, "getTransactionCount needs an address")
		}
		var addr string
		if err := json.Unmarshal(params[0], &addr); err != nil {
			return fail(req, -32602, "bad address: %v", err)
		}
		account := common.HexToAddress(addr)
		count := c.accountNonce[account]
		for _, h := range c.pending {
			if c.senders[h] == account {
				count++
			}
		}
		return result(req, quantity(count))
	case "eth_estimateGas":
		var msg struct {
			Input hexutil.Bytes `json:"input"`
			Data  hexutil.Bytes `json:"data"`
		}
		if len(params) > 0 {
			_ = json.Unmarshal(params[0], &msg)
		}
		payload := msg.Input
		if len(payload) == 0 {
			payload = msg.Data
		}
		return result(req, quantity(21_000+uint64(len(payload))*16))
	case "eth_sendRawTransaction":
		if len(params) < 1 {
			return fail(req, -32602, "sendRawTransaction needs raw bytes")
		}
		var raw string
		if err := json.Unmarshal(params[0], &raw); err != nil {
			return fail(req, -32602, "bad raw transaction: %v", err)
		}
		blob, err := hexutil.Decode(raw)
		if err != nil {
			return fail(req, -32602, "bad raw transaction: %v", err)
		}
		tx := &types.Transaction{}
		if err := tx.UnmarshalBinary(blob); err != nil {
			return fail(req, -32010, "already known or undecodable: %v", err)
		}
		sender, err := types.Sender(types.LatestSignerForChainID(new(big.Int).SetUint64(c.chainID)), tx)
		if err != nil {
			return fail(req, -32010, "sender recovery failed: %v", err)
		}
		hash := tx.Hash()
		// A dropped transaction is "accepted" and never mined: the
		// submission response carries a hash the chain later forgets,
		// which is exactly the lost-outcome path (spec §14.3).
		if rand.Float64() < f.DropRate {
			c.dropped[hash] = true
			return result(req, hash.Hex())
		}
		c.pooled[hash] = tx
		c.senders[hash] = sender
		c.pending = append(c.pending, hash)
		return result(req, hash.Hex())
	case "eth_getTransactionByHash":
		if len(params) < 1 {
			return fail(req, -32602, "getTransactionByHash needs a hash")
		}
		var raw string
		if err := json.Unmarshal(params[0], &raw); err != nil {
			return fail(req, -32602, "bad hash: %v", err)
		}
		hash := common.HexToHash(raw)
		if tx, ok := c.pooled[hash]; ok {
			return result(req, pendingTxJSON(tx))
		}
		if rec, ok := c.receipts[hash]; ok {
			return result(req, rec.tx)
		}
		return result(req, nil)
	case "eth_getTransactionReceipt":
		if len(params) < 1 {
			return fail(req, -32602, "getTransactionReceipt needs a hash")
		}
		var raw string
		if err := json.Unmarshal(params[0], &raw); err != nil {
			return fail(req, -32602, "bad hash: %v", err)
		}
		hash := common.HexToHash(raw)
		rec, ok := c.receipts[hash]
		if !ok {
			return result(req, nil)
		}
		r := &types.Receipt{
			Status:            rec.status,
			CumulativeGasUsed: rec.gasUsed,
			Logs:              []*types.Log{},
			TxHash:            hash,
			BlockHash:         rec.blockHash,
			BlockNumber:       bigInt(rec.blockNumber),
			GasUsed:           rec.gasUsed,
		}
		return result(req, r)
	default:
		return fail(req, -32601, "method %q not found", req.Method)
	}
}

// pendingTxJSON renders a pooled transaction the way a node serves an
// unmined one: the canonical transaction JSON plus null block fields,
// which is what ethclient reads to answer "still pending?".
func pendingTxJSON(tx *types.Transaction) json.RawMessage {
	body, err := json.Marshal(tx)
	if err != nil {
		return json.RawMessage("null")
	}
	merged := map[string]json.RawMessage{
		"blockHash":        json.RawMessage("null"),
		"blockNumber":      json.RawMessage("null"),
		"transactionIndex": json.RawMessage("null"),
	}
	_ = json.Unmarshal(body, &merged)
	out, err := json.Marshal(merged)
	if err != nil {
		return json.RawMessage("null")
	}
	return out
}
