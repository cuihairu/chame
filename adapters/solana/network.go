package solana

import (
	"context"
	"sync"

	"github.com/gagliardetto/solana-go"

	"github.com/cuihairu/chame/capability/network"
	"github.com/cuihairu/chame/core/capability"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// Chain-agnostic record types. SDK types never cross this boundary
// (ADR-0001). Solana semantics are preserved, not flattened: a lamport
// count is not an integer amount of SOL, and a signature is a 64-byte
// value, not a transaction hash.

// AccountInfo is a read-only view of one account (spec §8.2).
type AccountInfo struct {
	Address identifiers.Address
	// Lamports is the native balance. One SOL is 1e9 lamports; the adapter
	// never converts, so precision policy stays with the caller.
	Lamports uint64
	// Owner is the base58 program id the account belongs to.
	Owner string
	// Data is the account's binary content, base64-decoded. Nil when the
	// node reported no data.
	Data       []byte
	Executable bool
	// RentEpoch is the epoch at which the account next owes rent.
	RentEpoch uint64
	// Space is the storage the account occupies.
	Space uint64
}

// TokenAmount is one SPL token balance (spec §8.2).
type TokenAmount struct {
	// Account is the token account the balance belongs to, base58.
	Account identifiers.Address
	// Decimals is the mint's decimal count.
	Decimals uint8
	// RawAmount is the integer base-unit count exactly as the node
	// reported it. Solana token amounts exceed float64 precision, so the
	// adapter never rounds through a float.
	RawAmount string
	// UiAmount is the node's human-readable form, for display only.
	UiAmount string
}

// TransactionRecord is one confirmed transaction summary (spec §8.2).
type TransactionRecord struct {
	// Signature is the base58 transaction signature.
	Signature string
	Slot      uint64
	// TimeSec is the estimated processing time; zero when the node
	// reported none.
	TimeSec uint64
	// Fee is the transaction fee in lamports.
	Fee uint64
	// ErrDetail describes the execution failure. Empty means the node
	// recorded no failure for the transaction.
	ErrDetail string
}

// SignatureRecord is one entry in an address's signature history (spec
// §8.2).
type SignatureRecord struct {
	Signature string
	Slot      uint64
	TimeSec   uint64
	Memo      string
	ErrDetail string
	// Status is the node's confirmation level: processed, confirmed or
	// finalized.
	Status string
}

// ConfirmationStatus is the observed confirmation state of one signature
// (spec §8.2, §8.5: record what was observed).
type ConfirmationStatus struct {
	Signature string
	Slot      uint64
	// Confirmations is the block depth reported by the node; zero when the
	// node says it is rooted or finalised.
	Confirmations uint64
	ErrDetail     string
	Status        string
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

// Network is one connected Solana cluster instance (spec §8.1). All query
// methods are read-only in Phase 4.
type Network struct {
	cfg    NetworkConfig
	id     identifiers.NetworkID
	pool   *endpointPool
	stats  *counters
	opBase string
}

// newNetwork wires the instance around the dialed endpoint clients.
func newNetwork(cfg NetworkConfig, clients []rpcClient) *Network {
	stats := &counters{}
	var pool *endpointPool
	if len(clients) > 0 {
		pool = newEndpointPool(clients, stats)
	}
	return &Network{
		cfg:    cfg,
		id:     cfg.networkID(),
		pool:   pool,
		stats:  stats,
		opBase: "solana." + cfg.Name,
	}
}

// ID returns the logical network identity.
func (n *Network) ID() identifiers.NetworkID { return n.id }

// Network implements network.Provider.
func (n *Network) Network() identifiers.NetworkID { return n.id }

// Healthy verifies the endpoints still report the configured genesis hash
// (spec §8.2). It implements network.Provider.
func (n *Network) Healthy(ctx context.Context) error {
	got, err := n.GenesisHash(ctx)
	if err != nil {
		return err
	}
	if got != n.cfg.ExpectedGenesisHash {
		return cherrs.New(cherrs.InvalidConfiguration, n.opBase+".health",
			"genesis hash mismatch: want %s, got %s", n.cfg.ExpectedGenesisHash, got)
	}
	return nil
}

// GenesisHash returns the cluster identity reported by the endpoints.
func (n *Network) GenesisHash(ctx context.Context) (string, error) {
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var hash solana.Hash
	err := n.pool.call(ctx, n.id.String(), n.opBase+".genesis_hash", func(c rpcClient) error {
		var e error
		hash, e = c.GenesisHash(ctx)
		return e
	})
	if err != nil {
		n.stats.countError()
		return "", err
	}
	return hash.String(), nil
}

// Slot returns the current slot at the given commitment level. An empty
// commitment uses the network default (spec §8.5).
func (n *Network) Slot(ctx context.Context, commitment Commitment) (uint64, error) {
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var slot uint64
	err := n.pool.call(ctx, n.id.String(), n.opBase+".slot", func(rc rpcClient) error {
		var e error
		slot, e = rc.Slot(ctx, c.rpc())
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	return slot, nil
}

// Balance returns the native balance of account in lamports.
func (n *Network) Balance(ctx context.Context, account identifiers.Address, commitment Commitment) (uint64, error) {
	addr, err := toPublicKey(n.opBase+".balance", account)
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var lamports uint64
	err = n.pool.call(ctx, n.id.String(), n.opBase+".balance", func(rc rpcClient) error {
		var e error
		lamports, e = rc.Balance(ctx, addr, c.rpc())
		return e
	})
	if err != nil {
		n.stats.countError()
		return 0, err
	}
	return lamports, nil
}

// Account returns the account's state. A nil result with a nil error means
// the account does not exist, which the node reports rather than failing
// the query.
func (n *Network) Account(ctx context.Context, account identifiers.Address, commitment Commitment) (*AccountInfo, error) {
	addr, err := toPublicKey(n.opBase+".account", account)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out *accountResult
	err = n.pool.call(ctx, n.id.String(), n.opBase+".account", func(rc rpcClient) error {
		var e error
		out, e = rc.AccountInfo(ctx, addr, &rpcGetAccountInfoOpts{Commitment: c.rpc()})
		return e
	})
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		n.stats.countError()
		return nil, err
	}
	if out == nil || out.Value == nil {
		return nil, nil
	}
	owner, err := toAddress(n.opBase+".account", out.Value.Owner)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	return &AccountInfo{
		Address:    account,
		Lamports:   out.Value.Lamports,
		Owner:      owner.Value,
		Data:       out.GetBinary(),
		Executable: out.Value.Executable,
		RentEpoch:  uint64RentEpoch(out.Value.RentEpoch),
		Space:      out.Value.Space,
	}, nil
}

// TokenBalance returns the SPL balance of one token account. A nil result
// with a nil error means the account holds no token amount.
func (n *Network) TokenBalance(ctx context.Context, tokenAccount identifiers.Address, commitment Commitment) (*TokenAmount, error) {
	addr, err := toPublicKey(n.opBase+".token_balance", tokenAccount)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out *tokenBalanceResult
	err = n.pool.call(ctx, n.id.String(), n.opBase+".token_balance", func(rc rpcClient) error {
		var e error
		out, e = rc.TokenBalance(ctx, addr, c.rpc())
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	if out == nil || out.Value == nil {
		return nil, nil
	}
	return &TokenAmount{
		Account:   tokenAccount,
		Decimals:  out.Value.Decimals,
		RawAmount: out.Value.Amount,
		UiAmount:  out.Value.UiAmountString,
	}, nil
}

// TokenAccounts lists the SPL token accounts owned by owner. mint narrows
// the listing to one token; a nil mint lists every token account. This is
// the read side of the SPL Token account model (spec §8.2).
func (n *Network) TokenAccounts(ctx context.Context, owner identifiers.Address, mint *identifiers.Address, commitment Commitment) ([]AccountInfo, error) {
	own, err := toPublicKey(n.opBase+".token_accounts", owner)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	var mintKey *solana.PublicKey
	if mint != nil {
		mk, err := toPublicKey(n.opBase+".token_accounts", *mint)
		if err != nil {
			n.stats.countError()
			return nil, err
		}
		mintKey = &mk
	}
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out *tokenAccountsResult
	err = n.pool.call(ctx, n.id.String(), n.opBase+".token_accounts", func(rc rpcClient) error {
		var e error
		out, e = rc.TokenAccounts(ctx, own, &rpcGetTokenAccountsConfig{Mint: mintKey}, &rpcGetTokenAccountsOpts{Commitment: c.rpc()})
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	if out == nil || len(out.Value) == 0 {
		return nil, nil
	}
	recs := make([]AccountInfo, 0, len(out.Value))
	for _, ta := range out.Value {
		addr, err := toAddress(n.opBase+".token_accounts", ta.Pubkey)
		if err != nil {
			n.stats.countError()
			return nil, err
		}
		o, err := toAddress(n.opBase+".token_accounts", ta.Account.Owner)
		if err != nil {
			n.stats.countError()
			return nil, err
		}
		recs = append(recs, AccountInfo{
			Address:    addr,
			Lamports:   ta.Account.Lamports,
			Owner:      o.Value,
			Executable: ta.Account.Executable,
			RentEpoch:  uint64RentEpoch(ta.Account.RentEpoch),
			Space:      ta.Account.Space,
		})
	}
	return recs, nil
}

// Transaction returns the confirmed transaction summary for sig. A nil
// result with a nil error means the node holds no such transaction.
func (n *Network) Transaction(ctx context.Context, sig string, commitment Commitment) (*TransactionRecord, error) {
	s, err := toSignature(n.opBase+".transaction", sig)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	// getTransaction does not accept the processed level: the node refuses
	// it, so the adapter rejects it before the round trip (spec §8.5: the
	// level is an application choice, but a per-method one).
	if commitment == CommitmentProcessed {
		n.stats.countError()
		return nil, cherrs.New(cherrs.InvalidArgument, n.opBase+".transaction",
			"commitment %q is not supported by the transaction query", commitment)
	}
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out *transactionResult
	err = n.pool.call(ctx, n.id.String(), n.opBase+".transaction", func(rc rpcClient) error {
		var e error
		out, e = rc.Transaction(ctx, s, &rpcGetTransactionOpts{Commitment: c.rpc()})
		return e
	})
	if err != nil {
		if notFound(err) {
			return nil, nil
		}
		n.stats.countError()
		return nil, err
	}
	if out == nil {
		return nil, nil
	}
	rec := &TransactionRecord{Signature: sig, Slot: out.Slot}
	if out.BlockTime != nil {
		rec.TimeSec = uint64(*out.BlockTime)
	}
	// The node omits meta when it has no execution record, so both the fee
	// and the failure detail are absent together.
	if out.Meta != nil {
		rec.Fee = out.Meta.Fee
		rec.ErrDetail = errDetail(out.Meta.Err)
	}
	return rec, nil
}

// Signatures returns up to limit signatures touching account, newest first.
// A limit of zero uses the node default (1000).
func (n *Network) Signatures(ctx context.Context, account identifiers.Address, limit int, commitment Commitment) ([]SignatureRecord, error) {
	addr, err := toPublicKey(n.opBase+".signatures", account)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	if commitment == CommitmentProcessed {
		n.stats.countError()
		return nil, cherrs.New(cherrs.InvalidArgument, n.opBase+".signatures",
			"commitment %q is not supported by the signature history query", commitment)
	}
	c := n.commit(commitment)
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var out []*rpcTransactionSignature
	err = n.pool.call(ctx, n.id.String(), n.opBase+".signatures", func(rc rpcClient) error {
		var e error
		out, e = rc.Signatures(ctx, addr, &rpcGetSignaturesForAddressOpts{
			Commitment: c.rpc(),
			Limit:      &limit,
		})
		return e
	})
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	recs := make([]SignatureRecord, 0, len(out))
	for _, s := range out {
		if s == nil {
			continue
		}
		rec := SignatureRecord{
			Signature: sigString(s.Signature),
			Slot:      s.Slot,
			ErrDetail: errDetail(s.Err),
			Status:    string(s.ConfirmationStatus),
		}
		if s.BlockTime != nil {
			rec.TimeSec = uint64(*s.BlockTime)
		}
		if s.Memo != nil {
			rec.Memo = *s.Memo
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// TransactionStatus reports the node's confirmation state for sig. A nil
// result with a nil error means the node holds no status: the signature
// was never observed, which is not the same as a failed transaction
// (spec §8.4).
func (n *Network) TransactionStatus(ctx context.Context, sig string) (*ConfirmationStatus, error) {
	s, err := toSignature(n.opBase+".transaction_status", sig)
	if err != nil {
		n.stats.countError()
		return nil, err
	}
	ctx, cancel := n.withTimeout(ctx)
	defer cancel()
	n.stats.countRequest()
	var entries []*rpcSignatureStatusesResult
	err = n.pool.call(ctx, n.id.String(), n.opBase+".transaction_status", func(rc rpcClient) error {
		var e error
		entries, e = rc.SignatureStatus(ctx, s)
		return e
	})
	if err != nil {
		// The node answering "no such status" is a query outcome, not a
		// failure (spec §8.2).
		if notFound(err) {
			return nil, nil
		}
		n.stats.countError()
		return nil, err
	}
	if len(entries) == 0 || entries[0] == nil {
		return nil, nil
	}
	e := entries[0]
	return &ConfirmationStatus{
		Signature:     sig,
		Slot:          e.Slot,
		Confirmations: uint64OrZero(e.Confirmations),
		ErrDetail:     errDetail(e.Err),
		Status:        string(e.ConfirmationStatus),
	}, nil
}

// Stats returns the network's counters.
func (n *Network) Stats() NetworkStats { return n.stats.snapshot() }

// Capabilities lists the capability families this network instance can
// serve. Phase 4 is read-only (ADR-0003).
func (n *Network) Capabilities() []capability.Name {
	return []capability.Name{capabilityNetwork}
}

// commit resolves a caller commitment against the network default (spec
// §8.5: the application states the level it needs).
func (n *Network) commit(c Commitment) Commitment {
	if !c.Valid() {
		return n.cfg.commitment()
	}
	return c
}

// withTimeout bounds one request.
func (n *Network) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, n.cfg.timeout())
}

var _ network.Provider = (*Network)(nil)
