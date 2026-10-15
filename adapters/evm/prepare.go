package evm

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"github.com/cuihairu/chame/capability/signer"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// AccessListEntry pre-warms one storage slot of one contract (spec §7.6,
// optional). An access list is an EIP-2930 feature and travels inside an
// EIP-1559 transaction, so a legacy encoding cannot carry one.
type AccessListEntry struct {
	// To is the contract whose slot is pre-warmed.
	To identifiers.Address
	// Key is the 32-byte storage slot, in hex.
	Key string
}

// BuildQuery is the application's intent for one state-changing
// transaction, stated in chain-agnostic terms (spec §7.6). Nothing here
// is an SDK type: the adapter translates this into an EVM transaction.
type BuildQuery struct {
	// Sender pays for the transaction and signs it.
	Sender identifiers.Address
	// To is the recipient or the contract being called. A build does not
	// deploy a contract: creation is an explicit application decision with
	// its own failure mode.
	To identifiers.Address
	// Value is the native amount in base units; nil or zero sends none.
	Value *big.Int
	// Data is the calldata.
	Data []byte
	// GasLimit caps execution. Zero lets the node estimate it.
	GasLimit uint64
	// GasPrice names a legacy transaction at exactly this price.
	GasPrice *big.Int
	// MaxFeePerGas names an EIP-1559 transaction with this ceiling.
	MaxFeePerGas *big.Int
	// MaxPriorityFeePerGas caps the priority fee of an EIP-1559 build;
	// nil takes MaxFeePerGas, so naming only the ceiling is complete.
	MaxPriorityFeePerGas *big.Int
	// Nonce pins the nonce. Nil lets this instance's coordinator allocate
	// one, which is what keeps concurrent builds from claiming the same
	// value (spec §7.5).
	Nonce *uint64
	// AccessList pre-warms storage slots and requires EIP-1559.
	AccessList []AccessListEntry
	// Deadline bounds the whole build, not one request. Zero leaves the
	// caller's context in charge. The same bound applies to submitting the
	// built transaction.
	Deadline time.Duration
	// IdempotencyKey is the application's key for this operation (spec
	// §10.1): the repository's unique scope, not the chain's identity.
	IdempotencyKey string
}

// Built is one prepared transaction, ready to sign and submit. The
// payload is the unsigned canonical encoding a signer takes; the fields
// repeat what the application needs to record and resume the operation.
type Built struct {
	// Payload is the unsigned transaction encoding: RLP for a legacy
	// transaction, the typed envelope for EIP-1559.
	Payload []byte
	// Format names the encoding, for the signer request.
	Format   signer.Format
	Sender   identifiers.Address
	To       identifiers.Address
	Value    *big.Int
	Nonce    uint64
	GasLimit uint64
	// GasPrice is set for a legacy build, the two fee fields for an
	// EIP-1559 one.
	GasPrice             *big.Int
	MaxFeePerGas         *big.Int
	MaxPriorityFeePerGas *big.Int
	Deadline             time.Duration
	IdempotencyKey       string
}

// Build prepares one transaction from an intent (spec §7.6). It reads
// from the chain — the nonce, the gas estimate, the fee suggestion — and
// returns bytes to sign. It never signs and never broadcasts, so a build
// that fails has left the chain untouched and gives its nonce back.
func (n *Network) Build(ctx context.Context, q BuildQuery) (Built, error) {
	if err := q.validate(); err != nil {
		return Built{}, of(cherrs.InvalidArgument, n.opBase+".build", n.id.String(), err)
	}
	// The deadline bounds the whole build, not just one request.
	deadlineCtx := ctx
	if q.Deadline > 0 {
		var cancel context.CancelFunc
		deadlineCtx, cancel = context.WithTimeout(ctx, q.Deadline)
		defer cancel()
	}
	ctx = deadlineCtx

	// The fee mechanism is decided before anything else is spent, so a
	// build that cannot fit its access list fails before the node is
	// asked for anything but the probe.
	fees, err := n.quoteFees(ctx, q)
	if err != nil {
		return Built{}, err
	}

	nonce, own := q.Nonce, false
	if nonce == nil {
		allocated, e := n.nonces.Next(ctx, q.Sender)
		if e != nil {
			return Built{}, e
		}
		nonce, own = &allocated, true
	}
	// A build never broadcasts, so one that fails gives the nonce back and
	// the account does not wait out a transaction it never sent.
	fail := func(err error) (Built, error) {
		if own {
			_ = n.nonces.Abandon(q.Sender, *nonce)
		}
		return Built{}, err
	}

	gasLimit, err := n.gasLimit(ctx, q)
	if err != nil {
		return fail(err)
	}
	payload, err := encodeTx(n.cfg.ChainID, q, *nonce, gasLimit, fees)
	if err != nil {
		return fail(of(cherrs.InvalidArgument, n.opBase+".build", n.id.String(), err))
	}
	return Built{
		Payload:              payload,
		Format:               fees.format(),
		Sender:               q.Sender,
		To:                   q.To,
		Value:                valueOf(q.Value),
		Nonce:                *nonce,
		GasLimit:             gasLimit,
		GasPrice:             fees.gasPrice,
		MaxFeePerGas:         fees.maxFee,
		MaxPriorityFeePerGas: fees.tipCap,
		Deadline:             q.Deadline,
		IdempotencyKey:       q.IdempotencyKey,
	}, nil
}

// validate checks the intent before any chain call, so a malformed query
// is cheap and never touches the endpoint.
func (q BuildQuery) validate() error {
	if q.Sender.Family != identifiers.FamilyEVM {
		return fmt.Errorf("sender must be an evm address, got %s", q.Sender.Family)
	}
	if err := q.Sender.Validate(); err != nil {
		return fmt.Errorf("sender: %w", err)
	}
	if q.To.Family != identifiers.FamilyEVM {
		return fmt.Errorf("recipient must be an evm address, got %s", q.To.Family)
	}
	if err := q.To.Validate(); err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	if q.Value != nil && q.Value.Sign() < 0 {
		return fmt.Errorf("value must not be negative")
	}
	if q.Deadline < 0 {
		return fmt.Errorf("deadline must not be negative")
	}
	if q.GasPrice != nil && q.MaxFeePerGas != nil {
		return fmt.Errorf("name one fee mechanism, not both a gas price and a max fee")
	}
	if q.AccessList != nil && q.GasPrice != nil {
		return fmt.Errorf("an access list is an EIP-1559 feature and cannot ride a legacy gas price")
	}
	for i, e := range q.AccessList {
		if e.To.Family != identifiers.FamilyEVM {
			return fmt.Errorf("access list %d: target must be an evm address, got %s", i, e.To.Family)
		}
		if err := e.To.Validate(); err != nil {
			return fmt.Errorf("access list %d: %w", i, err)
		}
		if _, err := parseStorageKey(e.Key); err != nil {
			return fmt.Errorf("access list %d: %v", i, err)
		}
	}
	return nil
}

// feeQuote is one resolved fee mechanism: either a legacy price or an
// EIP-1559 pair.
type feeQuote struct {
	dynamic  bool
	gasPrice *big.Int
	maxFee   *big.Int
	tipCap   *big.Int
}

func (f feeQuote) format() signer.Format {
	if f.dynamic {
		return signer.FormatEIP1559
	}
	return signer.FormatEIP155
}

// feeQuote decides which fee mechanism this network takes (spec §7.6:
// EIP-1559 fields must never be forced onto a network that does not run
// them). An explicit field is taken as given; otherwise the node is
// asked for a priority fee, and only a node that answers one gets an
// EIP-1559 transaction.
func (n *Network) quoteFees(ctx context.Context, q BuildQuery) (feeQuote, error) {
	if q.GasPrice != nil {
		return feeQuote{gasPrice: q.GasPrice}, nil
	}
	if q.MaxFeePerGas != nil {
		tip := q.MaxFeePerGas
		if q.MaxPriorityFeePerGas != nil {
			tip = q.MaxPriorityFeePerGas
		}
		return feeQuote{dynamic: true, maxFee: q.MaxFeePerGas, tipCap: tip}, nil
	}

	tip, err := n.suggestTipCap(ctx)
	if err != nil {
		if cat, ok := cherrs.CategoryOf(err); ok && retryable(cat) {
			// The node was reachable, not uncooperative: report the
			// failure rather than guessing the mechanism.
			return feeQuote{}, err
		}
		// The node answers no priority fee, so EIP-1559 would be the
		// wrong encoding. A legacy price is what it does support.
		if q.AccessList != nil {
			return feeQuote{}, of(cherrs.InvalidArgument, n.opBase+".build", n.id.String(),
				fmt.Errorf("an access list is an EIP-1559 feature and this network answers no priority fee"))
		}
		price, e := n.suggestGasPrice(ctx)
		if e != nil {
			return feeQuote{}, e
		}
		if price.Sign() <= 0 {
			return feeQuote{}, of(cherrs.InternalError, n.opBase+".suggest_gas_price", n.id.String(),
				fmt.Errorf("node suggested a zero gas price"))
		}
		return feeQuote{gasPrice: price}, nil
	}
	price, err := n.suggestGasPrice(ctx)
	if err != nil {
		return feeQuote{}, err
	}
	if price.Sign() <= 0 {
		return feeQuote{}, of(cherrs.InternalError, n.opBase+".suggest_gas_price", n.id.String(),
			fmt.Errorf("node suggested a zero max fee"))
	}
	if tip.Cmp(price) > 0 {
		// A priority fee above the ceiling is not encodable; cap it.
		tip = new(big.Int).Set(price)
	}
	return feeQuote{dynamic: true, maxFee: price, tipCap: tip}, nil
}

// gasLimit takes the caller's limit or asks the node to measure the
// call.
func (n *Network) gasLimit(ctx context.Context, q BuildQuery) (uint64, error) {
	if q.GasLimit != 0 {
		return q.GasLimit, nil
	}
	return n.estimateGas(ctx, q)
}

// estimateGas asks the node to measure this exact call: same sender,
// recipient, value and calldata, and no gas limit so the node measures
// rather than checks.
func (n *Network) estimateGas(ctx context.Context, q BuildQuery) (uint64, error) {
	to := common.HexToAddress(q.To.Value)
	msg := ethereum.CallMsg{
		From:  common.HexToAddress(q.Sender.Value),
		To:    &to,
		Value: q.Value,
		Data:  q.Data,
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var gas uint64
	err := n.pool.call(ctx, n.id.String(), n.opBase+".estimate_gas", func(c rpcClient) error {
		var e error
		gas, e = c.EstimateGas(ctx, msg)
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	return gas, nil
}

// suggestGasPrice asks the node for the current gas price.
func (n *Network) suggestGasPrice(ctx context.Context) (*big.Int, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var price *big.Int
	err := n.pool.call(ctx, n.id.String(), n.opBase+".suggest_gas_price", func(c rpcClient) error {
		var e error
		price, e = c.SuggestGasPrice(ctx)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return price, nil
}

// suggestTipCap asks the node for the priority fee it suggests. A node
// that does not run EIP-1559 answers with an error, which is how this
// adapter tells the two mechanisms apart.
func (n *Network) suggestTipCap(ctx context.Context) (*big.Int, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var tip *big.Int
	err := n.pool.call(ctx, n.id.String(), n.opBase+".suggest_gas_tip_cap", func(c rpcClient) error {
		var e error
		tip, e = c.SuggestGasTipCap(ctx)
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return tip, nil
}

// encodeTx renders one unsigned transaction. The chain id is bound to
// the EIP-1559 envelope; a legacy encoding carries no chain id field, so
// that one is bound at signing time, from the network the request names.
func encodeTx(chainID uint64, q BuildQuery, nonce, gasLimit uint64, fees feeQuote) ([]byte, error) {
	to := common.HexToAddress(q.To.Value)
	value := valueOf(q.Value)
	if fees.dynamic {
		return types.NewTx(&types.DynamicFeeTx{
			ChainID:    new(big.Int).SetUint64(chainID),
			Nonce:      nonce,
			To:         &to,
			Value:      value,
			Gas:        gasLimit,
			GasFeeCap:  fees.maxFee,
			GasTipCap:  fees.tipCap,
			Data:       q.Data,
			AccessList: accessList(q.AccessList),
		}).MarshalBinary()
	}
	return types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &to,
		Value:    value,
		Gas:      gasLimit,
		GasPrice: fees.gasPrice,
		Data:     q.Data,
	}).MarshalBinary()
}

// accessList renders the pre-warmed slots, leaving the field empty when
// none were named.
func accessList(entries []AccessListEntry) types.AccessList {
	if len(entries) == 0 {
		return nil
	}
	al := make(types.AccessList, 0, len(entries))
	for _, e := range entries {
		al = append(al, types.AccessTuple{
			Address:     common.HexToAddress(e.To.Value),
			StorageKeys: []common.Hash{common.HexToHash(e.Key)},
		})
	}
	return al
}

// valueOf normalises a nil amount to zero.
func valueOf(v *big.Int) *big.Int {
	if v == nil {
		return new(big.Int)
	}
	return v
}

// parseStorageKey decodes a 32-byte storage slot in hex. A zero slot is
// a legal key, unlike a zero transaction hash.
func parseStorageKey(raw string) (common.Hash, error) {
	s := strings.TrimSpace(raw)
	if len(s) != 66 || s[0] != '0' || (s[1] != 'x' && s[1] != 'X') {
		return common.Hash{}, fmt.Errorf("storage key must be 32 bytes in hex with a 0x prefix, got %q", raw)
	}
	if _, err := hex.DecodeString(s[2:]); err != nil {
		return common.Hash{}, fmt.Errorf("storage key %q is not hex", raw)
	}
	return common.HexToHash(raw), nil
}

// retryable reports whether a classified category says the node may be
// tried again later. Such an error never means "this network has no
// EIP-1559" — it means the node was unreachable at the moment.
func retryable(cat cherrs.Category) bool {
	switch cat {
	case cherrs.RequestTimeout, cherrs.NetworkUnavailable, cherrs.RateLimited:
		return true
	default:
		return false
	}
}
