package solana

import (
	"context"
	"errors"
	"testing"

	"math/big"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"

	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// newTestNetwork builds a network whose single endpoint is the fake.
func newTestNetwork(f *fakeClient) *Network {
	cfg := NetworkConfig{
		Name:                "mainnet",
		ExpectedGenesisHash: testGenesis(0x10),
		Endpoints:           []string{"https://rpc.example.com"},
	}
	return newNetwork(cfg, []rpcClient{f})
}

func mustAddress(t *testing.T, value string) identifiers.Address {
	t.Helper()
	a, err := identifiers.NewSolanaAddress(value)
	if err != nil {
		t.Fatalf("bad test address %q: %v", value, err)
	}
	return a
}

func TestGenesisHashAndHealthy(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	got, err := n.GenesisHash(context.Background())
	if err != nil {
		t.Fatalf("genesis hash: %v", err)
	}
	if got != testGenesis(0x10) {
		t.Fatalf("got %q, want %q", got, testGenesis(0x10))
	}
	if err := n.Healthy(context.Background()); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	if n.ID().ChainID != testGenesis(0x10) {
		t.Errorf("network id must carry the genesis hash, got %q", n.ID().ChainID)
	}
}

func TestHealthyDetectsMismatch(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x20))
	n := newTestNetwork(f)
	err := n.Healthy(context.Background())
	if !errors.Is(err, cherrs.ErrInvalidConfiguration) {
		t.Fatalf("err = %v, want invalid configuration", err)
	}
	var ce *cherrs.Error
	if !errors.As(err, &ce) {
		t.Fatalf("want a classified error, got %v", err)
	}
}

func TestSlotUsesCommitment(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	if got, err := n.Slot(context.Background(), CommitmentConfirmed); err != nil || got != 0 {
		t.Fatalf("slot = %d, %v", got, err)
	}
	if got := f.capturedCommitments(); len(got) != 1 || got[0] != CommitmentConfirmed {
		t.Fatalf("commitments = %v, want [confirmed]", got)
	}

	if _, err := n.Slot(context.Background(), ""); err != nil {
		t.Fatalf("slot with default: %v", err)
	}
	if got := f.capturedCommitments(); len(got) != 2 || got[1] != DefaultCommitment {
		t.Fatalf("commitments = %v, want [confirmed %s]", got, DefaultCommitment)
	}
}

func TestBalanceUsesCommitment(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.lamports = 1_000_000
	n := newTestNetwork(f)

	got, err := n.Balance(context.Background(), mustAddress(t, sysAccount), CommitmentFinalized)
	if err != nil || got != 1_000_000 {
		t.Fatalf("balance = %d, %v", got, err)
	}
	if cap := f.capturedCommitments(); len(cap) != 1 || cap[0] != CommitmentFinalized {
		t.Fatalf("commitments = %v", cap)
	}
	if accs := f.capturedAccounts(); len(accs) != 1 || accs[0].String() != sysAccount {
		t.Fatalf("accounts = %v", accs)
	}
}

func TestAccountConverts(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	data, err := rpc.DataBytesOrJSONFromBase64("aGVsbG8=")
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	epoch := new(big.Int).SetInt64(373)
	f.account = &accountResult{Value: &rpc.Account{
		Lamports:   2_500_000,
		Owner:      solana.MPK(tokenProgram),
		Data:       data,
		Executable: false,
		RentEpoch:  epoch,
		Space:      8,
	}}
	n := newTestNetwork(f)

	info, err := n.Account(context.Background(), mustAddress(t, sysAccount), CommitmentConfirmed)
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	if info.Lamports != 2_500_000 {
		t.Errorf("lamports = %d", info.Lamports)
	}
	if info.Owner != tokenProgram {
		t.Errorf("owner = %q, want %q", info.Owner, tokenProgram)
	}
	if string(info.Data) != "hello" {
		t.Errorf("data = %q, want hello", string(info.Data))
	}
	if info.RentEpoch != 373 {
		t.Errorf("rent epoch = %d, want 373", info.RentEpoch)
	}
	if info.Address.Value != sysAccount {
		t.Errorf("address = %q, want %q", info.Address.Value, sysAccount)
	}
	if info.Executable {
		t.Error("executable must be false")
	}
}

func TestAccountNilWhenMissing(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("account_info", rpcErrNotFound)
	n := newTestNetwork(f)

	info, err := n.Account(context.Background(), mustAddress(t, sysAccount), CommitmentFinalized)
	if err != nil {
		t.Fatalf("a missing account must not be an error: %v", err)
	}
	if info != nil {
		t.Fatalf("want nil account, got %+v", info)
	}
}

func TestTokenBalance(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.tokenBalance = &tokenBalanceResult{Value: &rpc.UiTokenAmount{
		Amount:         "4200000000",
		Decimals:       6,
		UiAmountString: "4200.000000",
	}}
	n := newTestNetwork(f)

	bal, err := n.TokenBalance(context.Background(), mustAddress(t, tokenAccount), CommitmentConfirmed)
	if err != nil {
		t.Fatalf("token balance: %v", err)
	}
	if bal.RawAmount != "4200000000" || bal.Decimals != 6 || bal.UiAmount != "4200.000000" {
		t.Fatalf("balance = %+v", bal)
	}
	if bal.Account.Value != tokenAccount {
		t.Errorf("account = %q, want %q", bal.Account.Value, tokenAccount)
	}
}

func TestTokenBalanceNilWhenNoValue(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.tokenBalance = &tokenBalanceResult{Value: nil}
	n := newTestNetwork(f)

	bal, err := n.TokenBalance(context.Background(), mustAddress(t, tokenAccount), CommitmentConfirmed)
	if err != nil {
		t.Fatalf("no token amount must not be an error: %v", err)
	}
	if bal != nil {
		t.Fatalf("want nil, got %+v", bal)
	}
}

func TestTokenAccountsLists(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.tokenAccounts = &tokenAccountsResult{Value: []*rpc.TokenAccount{
		{Pubkey: solana.MPK(tokenAccount), Account: rpc.Account{
			Lamports:   9_000,
			Owner:      solana.MPK(sysAccount),
			Executable: false,
			Space:      165,
		}},
	}}
	n := newTestNetwork(f)
	mint := mustAddress(t, tokenAccount)

	recs, err := n.TokenAccounts(context.Background(), mustAddress(t, sysAccount), &mint, CommitmentConfirmed)
	if err != nil {
		t.Fatalf("token accounts: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d accounts, want 1", len(recs))
	}
	if recs[0].Lamports != 9_000 || recs[0].Owner != sysAccount || recs[0].Space != 165 {
		t.Fatalf("record = %+v", recs[0])
	}
}

func TestTransactionConverts(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	blockTime := solana.UnixTimeSeconds(1_700_000_000)
	f.transaction = &transactionResult{
		Slot:      123_456,
		BlockTime: &blockTime,
		Meta:      &rpc.TransactionMeta{Fee: 5_000},
	}
	n := newTestNetwork(f)
	sig := testSignature(0x30)

	tx, err := n.Transaction(context.Background(), sig, CommitmentConfirmed)
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if tx.Signature != sig || tx.Slot != 123_456 {
		t.Fatalf("record = %+v", tx)
	}
	if tx.TimeSec != 1_700_000_000 {
		t.Errorf("time = %d, want 1700000000", tx.TimeSec)
	}
	if tx.Fee != 5_000 {
		t.Errorf("fee = %d, want 5000", tx.Fee)
	}
	if tx.ErrDetail != "" {
		t.Errorf("err = %q, want empty", tx.ErrDetail)
	}
}

func TestTransactionFailureKeepsDetail(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	f.transaction = &transactionResult{
		Slot: 7,
		Meta: &rpc.TransactionMeta{Err: map[string]any{"InstructionError": []any{0, map[string]any{"Custom": 1}}}},
	}
	n := newTestNetwork(f)

	tx, err := n.Transaction(context.Background(), testSignature(0x30), CommitmentFinalized)
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if tx.ErrDetail == "" {
		t.Fatal("the node's failure detail must be preserved")
	}
}

func TestTransactionRejectsProcessedCommitment(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	tx, err := n.Transaction(context.Background(), testSignature(0x30), CommitmentProcessed)
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
	if tx != nil {
		t.Fatalf("want nil record, got %+v", tx)
	}
	if f.callCount("transaction") != 0 {
		t.Error("a rejected commitment must not reach the endpoint")
	}
}

func TestTransactionNilWhenMissing(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("transaction", rpcErrNotFound)
	n := newTestNetwork(f)

	tx, err := n.Transaction(context.Background(), testSignature(0x30), CommitmentFinalized)
	if err != nil {
		t.Fatalf("a missing transaction must not be an error: %v", err)
	}
	if tx != nil {
		t.Fatalf("want nil, got %+v", tx)
	}
}

func TestSignaturesConverts(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	blockTime := solana.UnixTimeSeconds(1_700_000_001)
	memo := "top up"
	f.signatures = []*rpc.TransactionSignature{{
		Signature:          solana.MustSignatureFromBase58(testSignature(0x40)),
		Slot:               99,
		BlockTime:          &blockTime,
		Memo:               &memo,
		ConfirmationStatus: rpc.ConfirmationStatusFinalized,
	}}
	n := newTestNetwork(f)

	recs, err := n.Signatures(context.Background(), mustAddress(t, sysAccount), 10, CommitmentConfirmed)
	if err != nil {
		t.Fatalf("signatures: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d signatures, want 1", len(recs))
	}
	r := recs[0]
	if r.Signature != testSignature(0x40) || r.Slot != 99 || r.Memo != "top up" || r.Status != "finalized" {
		t.Fatalf("record = %+v", r)
	}
	if r.TimeSec != 1_700_000_001 {
		t.Errorf("time = %d", r.TimeSec)
	}
}

func TestSignaturesRejectsProcessedCommitment(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	recs, err := n.Signatures(context.Background(), mustAddress(t, sysAccount), 10, CommitmentProcessed)
	if !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Fatalf("err = %v, want invalid argument", err)
	}
	if recs != nil {
		t.Fatalf("want nil, got %+v", recs)
	}
	if f.callCount("signatures") != 0 {
		t.Error("a rejected commitment must not reach the endpoint")
	}
}

func TestTransactionStatusConverts(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	confirmations := uint64(17)
	f.signatureStat = []*rpcSignatureStatusesResult{{
		Slot:               500,
		Confirmations:      &confirmations,
		ConfirmationStatus: rpc.ConfirmationStatusFinalized,
	}}
	n := newTestNetwork(f)

	st, err := n.TransactionStatus(context.Background(), testSignature(0x50))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Signature != testSignature(0x50) || st.Slot != 500 || st.Confirmations != 17 || st.Status != "finalized" {
		t.Fatalf("status = %+v", st)
	}
}

func TestTransactionStatusNilWhenUnobserved(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("signature_status", rpcErrNotFound)
	n := newTestNetwork(f)

	st, err := n.TransactionStatus(context.Background(), testSignature(0x50))
	if err != nil {
		t.Fatalf("an unobserved signature must not be an error: %v", err)
	}
	if st != nil {
		t.Fatalf("want nil, got %+v", st)
	}
}

func TestWrongAddressFamilyRejected(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	addr, err := identifiers.NewEVMAddress("0x1234567890123456789012345678901234567890")
	if err != nil {
		t.Fatalf("bad evm address: %v", err)
	}
	for name, call := range map[string]func() error{
		"balance": func() error {
			_, err := n.Balance(context.Background(), addr, CommitmentFinalized)
			return err
		},
		"account": func() error {
			_, err := n.Account(context.Background(), addr, CommitmentFinalized)
			return err
		},
		"token balance": func() error {
			_, err := n.TokenBalance(context.Background(), addr, CommitmentFinalized)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, cherrs.ErrInvalidArgument) {
				t.Fatalf("err = %v, want invalid argument", err)
			}
			if f.callCount("balance")+f.callCount("account_info")+f.callCount("token_balance") != 0 {
				t.Error("a wrong-family address must not reach the endpoint")
			}
		})
	}
}

func TestInvalidSignatureRejected(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10))
	n := newTestNetwork(f)

	if _, err := n.Transaction(context.Background(), "not-a-signature", CommitmentFinalized); err == nil {
		t.Fatal("an invalid signature must be rejected")
	}
	if _, err := n.TransactionStatus(context.Background(), ""); err == nil {
		t.Fatal("an empty signature must be rejected")
	}
	if f.callCount("transaction")+f.callCount("signature_status") != 0 {
		t.Error("a bad signature must not reach the endpoint")
	}
}

func TestStatsCountRequestsAndErrors(t *testing.T) {
	f := newFakeClient(solanaHashFor(0x10)).fail("slot", netErr{msg: "down"})
	n := newTestNetwork(f)

	_, err := n.Slot(context.Background(), CommitmentFinalized)
	if err == nil {
		t.Fatal("want an error")
	}
	if s := n.Stats(); s.Requests != 1 || s.Errors != 1 {
		t.Fatalf("stats = %+v", s)
	}
}
