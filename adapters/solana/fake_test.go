package solana

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
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
	b, _ := sigFor(seed).MarshalText()
	return string(b)
}

// sigFor builds the deterministic signature value behind testSignature.
func sigFor(seed byte) solana.Signature {
	var s solana.Signature
	for i := range s {
		s[i] = seed + byte(i)
	}
	return s
}

// fakeClient is the test double for the RPC seam. Unit tests never touch
// a real endpoint (spec §16, §19 Phase 4 acceptance).
type fakeClient struct {
	mu sync.Mutex

	// hash is the cluster identity the node reports.
	hash solana.Hash

	// errByMethod makes every call of a method fail with this error.
	errByMethod map[string]error

	// broken makes every call fail, whatever the method: the
	// conformance suite's unreachable-network path.
	broken bool

	// Responses.
	slot          uint64
	lamports      uint64
	account       *accountResult
	tokenBalance  *tokenBalanceResult
	tokenAccounts *tokenAccountsResult
	transaction   *transactionResult
	signatures    []*rpcTransactionSignature
	signatureStat []*rpcSignatureStatusesResult

	// sigStatus serves one signature's status answer instead of the
	// shared signatureStat, so a test can stage several signatures at
	// once with different outcomes.
	sigStatus map[string]*rpcSignatureStatusesResult

	// missingAccounts makes the node report null for these addresses,
	// like an address it has never seen.
	missingAccounts map[solana.PublicKey]bool

	// blocksBySlot overrides the block reported for one slot; a slot
	// without one answers nil, like a skipped slot.
	blocksBySlot map[uint64]*blockResult

	// blockhash and lastValidBlockHeight pin the getLatestBlockhash
	// answer; blockHeight pins getBlockHeight.
	blockhash            solana.Hash
	lastValidBlockHeight uint64
	blockHeight          uint64
	// historySearchCalls counts ledger-wide signature status queries.
	historySearchCalls int

	// sentTx is the last transaction the node was handed, and
	// sentSignature the answer it gave.
	sentTx        []byte
	sentSignature solana.Signature

	// slowByMethod sleeps before answering, for deadline tests.
	slowByMethod map[string]time.Duration

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

// breakEndpoints makes every subsequent call fail, the way an
// unreachable node does, so the conformance suite can reach any method's
// failure path at once.
func (f *fakeClient) breakEndpoints() *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken = true
	return f
}

// withSignatureStatus pins the status answer one signature gets.
func (f *fakeClient) withSignatureStatus(sig string, r *rpcSignatureStatusesResult) *fakeClient {
	if f.sigStatus == nil {
		f.sigStatus = map[string]*rpcSignatureStatusesResult{}
	}
	f.sigStatus[sig] = r
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
	f.attempts[method]++
	slow := f.slowByMethod[method]
	err, fail := f.errByMethod[method]
	broken := f.broken
	f.mu.Unlock()
	if slow > 0 {
		time.Sleep(slow)
	}
	if fail {
		return err
	}
	if broken {
		return netErr{msg: "connection reset"}
	}
	return nil
}

// withSlow makes every call of method wait before answering.
func (f *fakeClient) withSlow(method string, d time.Duration) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.slowByMethod == nil {
		f.slowByMethod = map[string]time.Duration{}
	}
	f.slowByMethod[method] = d
	return f
}

// withSentSignature pins the answer the node gives to a submission.
func (f *fakeClient) withSentSignature(sig solana.Signature) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentSignature = sig
	return f
}

// capturedSentTransaction reports the last submitted transaction bytes.
func (f *fakeClient) capturedSentTransaction() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.sentTx...)
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

// withBlock pins the block reported for slot and returns its hash, so
// tests can assert exact cursor anchors.
func (f *fakeClient) withBlock(slot uint64, hash solana.Hash) string {
	if f.blocksBySlot == nil {
		f.blocksBySlot = map[uint64]*blockResult{}
	}
	f.blocksBySlot[slot] = &blockResult{Blockhash: hash}
	return hash.String()
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

func (f *fakeClient) Block(ctx context.Context, slot uint64, opts *rpcGetBlockOpts) (*blockResult, error) {
	if err := f.maybeFail("block"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if opts != nil {
		f.commitments = append(f.commitments, Commitment(opts.Commitment))
	}
	b, ok := f.blocksBySlot[slot]
	f.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return b, nil
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

func (f *fakeClient) SignatureStatus(ctx context.Context, sig solana.Signature, searchHistory bool) ([]*rpcSignatureStatusesResult, error) {
	if err := f.maybeFail("signature_status"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if searchHistory {
		f.historySearchCalls++
	}
	if r, ok := f.sigStatus[sig.String()]; ok {
		return []*rpcSignatureStatusesResult{r}, nil
	}
	return f.signatureStat, nil
}

// LatestBlockhash answers the pinned blockhash at the caller's
// commitment. Unlike the other fixtures it honours a context that
// expires while it waits, because a build's deadline is judged here.
func (f *fakeClient) LatestBlockhash(ctx context.Context, commitment rpcCommitmentType) (*latestBlockhashResult, error) {
	if err := f.maybeFail("latest_blockhash"); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitments = append(f.commitments, Commitment(commitment))
	return &latestBlockhashResult{
		Value: &rpc.LatestBlockhashResult{Blockhash: f.blockhash, LastValidBlockHeight: f.lastValidBlockHeight},
	}, nil
}

// BlockHeight answers the pinned block height at the caller's commitment.
func (f *fakeClient) BlockHeight(ctx context.Context, commitment rpcCommitmentType) (uint64, error) {
	if err := f.maybeFail("block_height"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitments = append(f.commitments, Commitment(commitment))
	return f.blockHeight, nil
}

func (f *fakeClient) historySearches() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.historySearchCalls
}

// withBlockhash pins the getLatestBlockhash answer.
func (f *fakeClient) withBlockhash(hash solana.Hash, lastValidBlockHeight uint64) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockhash = hash
	f.lastValidBlockHeight = lastValidBlockHeight
	return f
}

// withBlockHeight pins the getBlockHeight answer.
func (f *fakeClient) withBlockHeight(height uint64) *fakeClient {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blockHeight = height
	return f
}

// SendTransaction records the submitted bytes and answers the pinned
// signature.
func (f *fakeClient) SendTransaction(ctx context.Context, tx []byte) (solana.Signature, error) {
	if err := f.maybeFail("send_transaction"); err != nil {
		return solana.Signature{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sentTx = append([]byte(nil), tx...)
	return f.sentSignature, nil
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
