package solana

import (
	"context"
	"fmt"
	"sync"

	"github.com/gagliardetto/solana-go"
)

// Canonical Solana program ids, used as valid 32-byte base58 accounts.
const (
	sysAccount   = "So11111111111111111111111111111111111111112"
	tokenAccount = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	tokenProgram = "TokenkegQfeZyiNwAJbNbGKPFXCWuBvf9Ss623VQ5DA"
	voteAccount  = "Vote111111111111111111111111111111111111111"
)

// solanaHashFor builds a deterministic cluster identity.
func solanaHashFor(seed byte) solana.Hash {
	var h solana.Hash
	for i := range h {
		h[i] = seed + byte(i)
	}
	return h
}

// testGenesis builds a deterministic cluster identifier.
func testGenesis(seed byte) string { return solanaHashFor(seed).String() }

// testSignature builds a deterministic 64-byte base58 signature.
func testSignature(seed byte) string {
	var s solana.Signature
	for i := range s {
		s[i] = seed + byte(i)
	}
	b, _ := s.MarshalText()
	return string(b)
}

// fakeClient is the test double for the RPC seam. Unit tests never touch
// a real endpoint (spec §16, §19 Phase 4 acceptance).
type fakeClient struct {
	mu sync.Mutex

	// hash is the cluster identity the node reports.
	hash solana.Hash

	// errByMethod makes every call of a method fail with this error.
	errByMethod map[string]error

	// Responses.
	slot          uint64
	lamports      uint64
	account       *accountResult
	tokenBalance  *tokenBalanceResult
	tokenAccounts *tokenAccountsResult
	transaction   *transactionResult
	signatures    []*rpcTransactionSignature
	signatureStat []*rpcSignatureStatusesResult

	// missingAccounts makes the node report null for these addresses,
	// like an address it has never seen.
	missingAccounts map[solana.PublicKey]bool

	// Captures.
	commitments []Commitment
	accounts    []solana.PublicKey
	closed      bool
	attempts    map[string]int
}

func newFakeClient(hash solana.Hash) *fakeClient {
	return &fakeClient{
		hash:        hash,
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

func (f *fakeClient) capturedCommitments() []Commitment {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Commitment(nil), f.commitments...)
}

func (f *fakeClient) capturedAccounts() []solana.PublicKey {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]solana.PublicKey(nil), f.accounts...)
}

// withMissing makes the node report null for addr: an address it has
// never seen.
func (f *fakeClient) withMissing(addr solana.PublicKey) *fakeClient {
	if f.missingAccounts == nil {
		f.missingAccounts = map[solana.PublicKey]bool{}
	}
	f.missingAccounts[addr] = true
	return f
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

func (f *fakeClient) GenesisHash(ctx context.Context) (solana.Hash, error) {
	if err := f.maybeFail("genesis_hash"); err != nil {
		return solana.Hash{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hash, nil
}

func (f *fakeClient) Slot(ctx context.Context, commitment rpcCommitmentType) (uint64, error) {
	if err := f.maybeFail("slot"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.commitments = append(f.commitments, Commitment(commitment))
	f.mu.Unlock()
	return f.slot, nil
}

func (f *fakeClient) Balance(ctx context.Context, account solana.PublicKey, commitment rpcCommitmentType) (uint64, error) {
	if err := f.maybeFail("balance"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	f.commitments = append(f.commitments, Commitment(commitment))
	f.accounts = append(f.accounts, account)
	missing := f.missingAccounts[account]
	f.mu.Unlock()
	if missing {
		return 0, nil
	}
	return f.lamports, nil
}

func (f *fakeClient) AccountInfo(ctx context.Context, account solana.PublicKey, opts *rpcGetAccountInfoOpts) (*accountResult, error) {
	if err := f.maybeFail("account_info"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if opts != nil {
		f.commitments = append(f.commitments, Commitment(opts.Commitment))
	}
	f.accounts = append(f.accounts, account)
	missing := f.missingAccounts[account]
	f.mu.Unlock()
	if missing {
		return nil, nil
	}
	return f.account, nil
}

func (f *fakeClient) TokenBalance(ctx context.Context, account solana.PublicKey, commitment rpcCommitmentType) (*tokenBalanceResult, error) {
	if err := f.maybeFail("token_balance"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.commitments = append(f.commitments, Commitment(commitment))
	f.accounts = append(f.accounts, account)
	f.mu.Unlock()
	return f.tokenBalance, nil
}

func (f *fakeClient) TokenAccounts(ctx context.Context, owner solana.PublicKey, conf *rpcGetTokenAccountsConfig, opts *rpcGetTokenAccountsOpts) (*tokenAccountsResult, error) {
	if err := f.maybeFail("token_accounts"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if opts != nil {
		f.commitments = append(f.commitments, Commitment(opts.Commitment))
	}
	f.accounts = append(f.accounts, owner)
	f.mu.Unlock()
	return f.tokenAccounts, nil
}

func (f *fakeClient) Transaction(ctx context.Context, sig solana.Signature, opts *rpcGetTransactionOpts) (*transactionResult, error) {
	if err := f.maybeFail("transaction"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if opts != nil {
		f.commitments = append(f.commitments, Commitment(opts.Commitment))
	}
	f.mu.Unlock()
	return f.transaction, nil
}

func (f *fakeClient) Signatures(ctx context.Context, account solana.PublicKey, opts *rpcGetSignaturesForAddressOpts) ([]*rpcTransactionSignature, error) {
	if err := f.maybeFail("signatures"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if opts != nil {
		f.commitments = append(f.commitments, Commitment(opts.Commitment))
	}
	f.accounts = append(f.accounts, account)
	f.mu.Unlock()
	return f.signatures, nil
}

func (f *fakeClient) SignatureStatus(ctx context.Context, sig solana.Signature) ([]*rpcSignatureStatusesResult, error) {
	if err := f.maybeFail("signature_status"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.signatureStat, nil
}

func (f *fakeClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// netErr implements net.Error for classification tests.
type netErr struct {
	msg     string
	timeout bool
}

func (e netErr) Error() string   { return e.msg }
func (e netErr) Timeout() bool   { return e.timeout }
func (e netErr) Temporary() bool { return true }

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
