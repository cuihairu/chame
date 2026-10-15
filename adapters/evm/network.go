package evm

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strings"
	"sync"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/network"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// Chain-agnostic record types. SDK types never cross this boundary
// (ADR-0001).

// BlockInfo is a block header summary.
type BlockInfo struct {
	Number     uint64
	Hash       string
	ParentHash string
	TimeSec    uint64
}

// LogRecord is one event log entry.
type LogRecord struct {
	BlockNumber uint64
	// BlockHash locates the block the log belongs to (spec §11.3: the
	// event identity needs the block location, not just the height).
	// A node that omits it reports the zero hash, which the event source
	// treats as an error rather than inventing an identity.
	BlockHash string
	TxHash    string
	LogIndex  uint32
	Address   identifiers.Address
	// Topics are 32-byte hex strings, the ABI event signature first.
	Topics []string
	// Data is the non-indexed ABI payload.
	Data []byte
}

// CallQuery is a read-only contract call (spec §7.7). The result is raw
// ABI output; decoding belongs to the caller or a ContractAdapter.
type CallQuery struct {
	To   identifiers.Address
	From identifiers.Address // optional
	Data []byte
}

// LogFilter selects logs by block range, emitter address, and topics.
type LogFilter struct {
	FromBlock uint64
	// ToBlock nil means latest.
	ToBlock *uint64
	// Addresses filters by emitting contract; empty accepts all.
	Addresses []identifiers.Address
	// Topics matches positionally; a nil position matches anything.
	Topics [][]string
}

// NetworkStats are the adapter's basic counters for one network
// (spec §15.2). A real metrics backend plugs in later.
type NetworkStats struct {
	Requests  uint64
	Errors    uint64
	Failovers uint64
}

type counters struct {
	mu        sync.Mutex
	requests  uint64
	errors    uint64
	failovers uint64
}

func (c *counters) countRequest() {
	c.mu.Lock()
	c.requests++
	c.mu.Unlock()
}

func (c *counters) countError() {
	c.mu.Lock()
	c.errors++
	c.mu.Unlock()
}

func (c *counters) countFailover() {
	c.mu.Lock()
	c.failovers++
	c.mu.Unlock()
}

func (c *counters) snapshot() NetworkStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return NetworkStats{Requests: c.requests, Errors: c.errors, Failovers: c.failovers}
}

// Network is one connected EVM network instance (spec §7.3). All query
// methods are read-only in Phase 3.
type Network struct {
	cfg    NetworkConfig
	id     identifiers.NetworkID
	pool   *endpointPool
	stats  *counters
	opBase string
	// nonces allocates account nonces for this instance (spec §7.5).
	nonces *NonceCoordinator
}

// newNetwork wires the instance around the dialed endpoint clients.
func newNetwork(cfg NetworkConfig, clients []rpcClient) *Network {
	stats := &counters{}
	n := &Network{
		cfg:    cfg,
		id:     cfg.networkID(),
		stats:  stats,
		opBase: "evm." + cfg.Name,
	}
	if len(clients) > 0 {
		n.pool = newEndpointPool(clients, stats)
	}
	// The coordinator needs the instance, and the instance needs the
	// coordinator; each is built off the other, so wire it here rather
	// than in the constructor's argument list.
	n.nonces = NewNonceCoordinator(n)
	return n
}

// NonceCoordinator returns this instance's nonce coordinator (spec §7.5).
// Builds allocate through it so concurrent callers never claim the same
// nonce.
func (n *Network) NonceCoordinator() *NonceCoordinator { return n.nonces }

// ID returns the logical network identity.
func (n *Network) ID() identifiers.NetworkID { return n.id }

// Network implements network.Provider.
func (n *Network) Network() identifiers.NetworkID { return n.id }

// Healthy verifies the endpoint still reports the configured chain ID
// (spec §7.2). It implements network.Provider.
func (n *Network) Healthy(ctx context.Context) error {
	got, err := n.ChainID(ctx)
	if err != nil {
		return err
	}
	if got != n.cfg.ChainID {
		return cherrs.New(cherrs.InvalidConfiguration, n.opBase+".health",
			"chain id mismatch: want %d, got %d", n.cfg.ChainID, got)
	}
	return nil
}

// ChainID returns the chain identifier reported by the endpoints.
func (n *Network) ChainID(ctx context.Context) (uint64, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var id *big.Int
	err := n.pool.call(ctx, n.id.String(), n.opBase+".chain_id", func(c rpcClient) error {
		var e error
		id, e = c.ChainID(ctx)
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	if id == nil {
		n.stats.countError()
		return 0, of(cherrs.InternalError, n.opBase+".chain_id", n.id.String(),
			fmt.Errorf("endpoint returned no chain id"))
	}
	return id.Uint64(), nil
}

// BlockNumber returns the latest block height.
func (n *Network) BlockNumber(ctx context.Context) (uint64, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var num uint64
	err := n.pool.call(ctx, n.id.String(), n.opBase+".block_number", func(c rpcClient) error {
		var e error
		num, e = c.BlockNumber(ctx)
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	return num, nil
}

// BlockByNumber returns a block summary; zero means latest.
func (n *Network) BlockByNumber(ctx context.Context, number uint64) (BlockInfo, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var header *types.Header
	var tag *big.Int
	if number != 0 {
		tag = new(big.Int).SetUint64(number)
	}
	err := n.pool.call(ctx, n.id.String(), n.opBase+".header_by_number", func(c rpcClient) error {
		var e error
		header, e = c.HeaderByNumber(ctx, tag)
		return e
	})
	if err != nil {
		n.stats.countError()
		return BlockInfo{}, err
	}
	if header == nil {
		// A height the node does not hold answers with no header: the
		// position is unverifiable, never a fabricated block.
		n.stats.countError()
		return BlockInfo{}, cherrs.New(cherrs.EventDecodeFailure, n.opBase+".header_by_number",
			"node returned no header for the requested block")
	}
	return BlockInfo{
		Number:     header.Number.Uint64(),
		Hash:       header.Hash().Hex(),
		ParentHash: header.ParentHash.Hex(),
		TimeSec:    header.Time,
	}, nil
}

// BalanceAt returns the native asset balance of account in wei.
func (n *Network) BalanceAt(ctx context.Context, account identifiers.Address) (*big.Int, error) {
	addr, err := toCommon(n.opBase+".balance_at", account)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var bal *big.Int
	err = n.pool.call(ctx, n.id.String(), n.opBase+".balance_at", func(c rpcClient) error {
		var e error
		bal, e = c.BalanceAt(ctx, addr, nil)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return bal, nil
}

// PendingNonceAt returns the account's next unconsumed nonce as the node
// sees it: transactions mined or pooled both count (spec §7.5's chain
// observation the coordinator must distinguish from local allocations).
func (n *Network) PendingNonceAt(ctx context.Context, account identifiers.Address) (uint64, error) {
	addr, err := toCommon(n.opBase+".pending_nonce_at", account)
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var nonce uint64
	err = n.pool.call(ctx, n.id.String(), n.opBase+".pending_nonce_at", func(c rpcClient) error {
		var e error
		nonce, e = c.PendingNonceAt(ctx, addr)
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	return nonce, nil
}

// TokenBalance returns the ERC-20 balance of holder on the token
// contract, in the token's base units.
func (n *Network) TokenBalance(ctx context.Context, token, holder identifiers.Address) (*big.Int, error) {
	tok, err := toCommon(n.opBase+".token_balance", token)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	own, err := toCommon(n.opBase+".token_balance", holder)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out []byte
	err = n.pool.call(ctx, n.id.String(), n.opBase+".token_balance", func(c rpcClient) error {
		var e error
		out, e = c.CallContract(ctx, ethereum.CallMsg{To: &tok, Data: balanceOfCall(own)}, nil)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	bal, err := abiWord(n.opBase+".token_balance", out)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return bal, nil
}

// CodeAt returns the code stored at account. Code-bearing addresses are
// contracts (or delegated signers); addresses without code are
// externally owned (spec §13.1).
func (n *Network) CodeAt(ctx context.Context, account identifiers.Address) ([]byte, error) {
	addr, err := toCommon(n.opBase+".code_at", account)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out []byte
	err = n.pool.call(ctx, n.id.String(), n.opBase+".code_at", func(c rpcClient) error {
		var e error
		out, e = c.CodeAt(ctx, addr, nil)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return out, nil
}

// Call performs a read-only contract call (eth_call).
func (n *Network) Call(ctx context.Context, q CallQuery) ([]byte, error) {
	to, err := toCommon(n.opBase+".call", q.To)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	msg := ethereum.CallMsg{To: &to, Data: q.Data}
	if q.From.Value != "" {
		from, err := toCommon(n.opBase+".call", q.From)
		if err != nil {
			n.stats.countError()
			return nil, err
		}
		msg.From = from
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out []byte
	err = n.pool.call(ctx, n.id.String(), n.opBase+".call", func(c rpcClient) error {
		var e error
		out, e = c.CallContract(ctx, msg, nil)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return out, nil
}

// Logs reads event logs for the filter.
func (n *Network) Logs(ctx context.Context, f LogFilter) ([]LogRecord, error) {
	q, err := n.buildFilter(f)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var logs []types.Log
	err = n.pool.call(ctx, n.id.String(), n.opBase+".logs", func(c rpcClient) error {
		var e error
		logs, e = c.FilterLogs(ctx, q)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	recs, err := toRecords(n.opBase+".logs", logs)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return recs, nil
}

// Stats returns the network's counters.
func (n *Network) Stats() NetworkStats { return n.stats.snapshot() }

// withTimeout bounds one request.
func (n *Network) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, n.cfg.timeout())
}

var _ network.Provider = (*Network)(nil)

// buildFilter converts the chain-agnostic filter to the SDK query.
func (n *Network) buildFilter(f LogFilter) (ethereum.FilterQuery, error) {
	q := ethereum.FilterQuery{FromBlock: new(big.Int).SetUint64(f.FromBlock)}
	if f.ToBlock != nil {
		q.ToBlock = new(big.Int).SetUint64(*f.ToBlock)
	}
	for _, a := range f.Addresses {
		c, err := toCommon(n.opBase+".logs", a)
		if err != nil {
			return q, err
		}
		q.Addresses = append(q.Addresses, c)
	}
	for _, pos := range f.Topics {
		if pos == nil {
			q.Topics = append(q.Topics, nil)
			continue
		}
		var row []common.Hash
		for _, t := range pos {
			h, err := toHash(n.opBase+".logs", t)
			if err != nil {
				return q, err
			}
			row = append(row, h)
		}
		q.Topics = append(q.Topics, row)
	}
	return q, nil
}

// Address and topic conversion helpers.

func toCommon(op string, a identifiers.Address) (common.Address, error) {
	if a.Family != identifiers.FamilyEVM {
		return common.Address{}, cherrs.New(cherrs.InvalidArgument, op,
			"address family %q is not evm", a.Family)
	}
	if err := a.Validate(); err != nil {
		return common.Address{}, cherrs.New(cherrs.InvalidArgument, op, "%v", err)
	}
	return common.HexToAddress(a.Value), nil
}

func toHash(op, topic string) (common.Hash, error) {
	h := strings.TrimPrefix(topic, "0x")
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) != 32 {
		return common.Hash{}, cherrs.New(cherrs.InvalidArgument, op,
			"topic must be 32 hex bytes, got %q", topic)
	}
	return common.BytesToHash(raw), nil
}

func fromCommon(op string, a common.Address) (identifiers.Address, error) {
	// common.Address.Hex() is EIP-55 checksummed, so construction
	// revalidates by construction.
	return identifiers.NewEVMAddress(a.Hex())
}

// balanceOfCall encodes ERC-20 balanceOf(address).
func balanceOfCall(holder common.Address) []byte {
	data := make([]byte, 4+32)
	copy(data[:4], []byte{0x70, 0xa0, 0x82, 0x31}) // keccak("balanceOf(address)")[:4]
	copy(data[4:], common.BytesToHash(holder.Bytes()).Bytes())
	return data
}

// selectorCall encodes a no-argument function by selector only.
func selectorCall(sel [4]byte) []byte {
	data := make([]byte, 4)
	copy(data, sel[:])
	return data
}

// abiUint8 decodes a no-argument call that returns a single uint value,
// taking its last byte as the uint8 payload.
func abiUint8(op string, out []byte) (uint8, error) {
	if len(out) != 32 {
		return 0, cherrs.New(cherrs.EventDecodeFailure, op,
			"want 32-byte response, got %d bytes", len(out))
	}
	return out[31], nil
}

// abiString decodes a no-argument call that returns a single ABI string:
// offset word, length word, then the zero-padded value.
func abiString(op string, out []byte) (string, error) {
	if len(out) < 64 {
		return "", cherrs.New(cherrs.EventDecodeFailure, op,
			"want at least a 64-byte response, got %d bytes", len(out))
	}
	length := new(big.Int).SetBytes(out[32:64]).Uint64()
	if uint64(len(out))-64 < length || length > 32*1024 {
		return "", cherrs.New(cherrs.EventDecodeFailure, op,
			"string length %d overruns the %d-byte response", length, uint64(len(out))-64)
	}
	return string(out[64 : 64+length]), nil
}

// abiWord decodes a single 32-byte ABI word response.
func abiWord(op string, out []byte) (*big.Int, error) {
	if len(out) != 32 {
		return nil, cherrs.New(cherrs.EventDecodeFailure, op,
			"want 32-byte response, got %d bytes", len(out))
	}
	return new(big.Int).SetBytes(out), nil
}

// toRecords converts SDK logs to the chain-agnostic record. A malformed
// entry is a decode failure, never silently dropped (spec §7.4:
// conflicting data must not be quietly resolved).
func toRecords(op string, logs []types.Log) ([]LogRecord, error) {
	out := make([]LogRecord, 0, len(logs))
	for i, l := range logs {
		addr, err := fromCommon(op, l.Address)
		if err != nil {
			return nil, cherrs.New(cherrs.EventDecodeFailure, op, "log %d: %v", i, err)
		}
		if l.Index > math.MaxUint32 {
			return nil, cherrs.New(cherrs.EventDecodeFailure, op, "log %d: index overflow", i)
		}
		topics := make([]string, 0, len(l.Topics))
		for _, t := range l.Topics {
			topics = append(topics, t.Hex())
		}
		out = append(out, LogRecord{
			BlockNumber: l.BlockNumber,
			BlockHash:   l.BlockHash.Hex(),
			TxHash:      l.TxHash.Hex(),
			LogIndex:    uint32(l.Index),
			Address:     addr,
			Topics:      topics,
			Data:        l.Data,
		})
	}
	return out, nil
}
