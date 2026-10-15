package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/cuihairu/chame/capability/account"
	"github.com/cuihairu/chame/capability/transaction"
	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

// runNetworkIdentity pins §16.2: the adapter reports the configured
// network identity, and every capability view agrees on it.
func runNetworkIdentity(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	id := s.Net().Network()
	if err := id.Validate(); err != nil {
		t.Fatalf("network id invalid: %v", err)
	}
	if id.Family != f.Family {
		t.Errorf("network family = %s, want %s", id.Family, f.Family)
	}
	if got := s.Accounts().Network(); !got.Equal(id) {
		t.Errorf("account provider network = %s, want %s", got, id)
	}
	if got := s.Assets().Network(); !got.Equal(id) {
		t.Errorf("asset provider network = %s, want %s", got, id)
	}
}

// runAddressFamily pins §16.2: address validation follows chain-family
// semantics — a malformed address and a foreign-family address are both
// rejected before any chain call.
func runAddressFamily(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	if _, err := identifiers.NewAddress(f.Family, f.MalformedAddress); err == nil {
		t.Fatalf("fixture malformed address %q must not parse", f.MalformedAddress)
	}
	malformed := identifiers.Address{Family: f.Family, Value: f.MalformedAddress}
	if _, err := s.Accounts().Balance(context.Background(), malformed); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("malformed address: err = %v, want invalid argument", err)
	}
	if _, err := s.Accounts().Balance(context.Background(), f.ForeignAddress); !errors.Is(err, cherrs.ErrInvalidArgument) {
		t.Errorf("foreign-family address: err = %v, want invalid argument", err)
	}
}

// runNativeBalance pins §16.2 金额不丢失精度: the native balance
// round-trips exactly as the chain reports it, in the family's own
// precision, with no float anywhere in the path.
func runNativeBalance(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	holder := mustAddress(t, f.Family, f.KnownAddress)

	got, err := s.Accounts().Balance(context.Background(), holder)
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("amount invalid: %v", err)
	}
	if got.Raw != f.NativeBalance {
		t.Errorf("balance raw = %q, want %q (exact string round-trip)", got.Raw, f.NativeBalance)
	}
	if got.Decimals != f.NativeDecimals {
		t.Errorf("balance decimals = %d, want %d", got.Decimals, f.NativeDecimals)
	}
	native := nativeAssetID(s.Net().Network())
	if got.AssetID != native.String() {
		t.Errorf("balance asset = %q, want %q", got.AssetID, native.String())
	}
}

// runAccountRecord pins §13.1: the account record validates, pins the
// network and address, carries the native balance, classifies the owner,
// and carries the fixture's detail keys.
func runAccountRecord(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	holder := mustAddress(t, f.Family, f.KnownAddress)

	got, err := s.Accounts().Account(context.Background(), holder)
	if err != nil {
		t.Fatalf("Account: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("account invalid: %v", err)
	}
	if !got.Network.Equal(s.Net().Network()) {
		t.Errorf("account network = %s, want %s", got.Network, s.Net().Network())
	}
	if !got.Address.Equal(holder) {
		t.Errorf("account address = %s, want %s", got.Address, holder)
	}
	if !got.Owner.Valid() {
		t.Errorf("owner kind %q is not a valid kind", got.Owner)
	}
	if got.Owner == account.OwnerHeldKey || got.Owner == account.OwnerUnknown {
		// The fixture's account is chain-reported: the adapter must
		// classify it from the chain, not from process state.
		t.Errorf("owner = %q, want a chain-derived kind", got.Owner)
	}
	if got.NativeBalance == nil || got.NativeBalance.Raw != f.NativeBalance {
		t.Errorf("native balance = %+v, want raw %q", got.NativeBalance, f.NativeBalance)
	}
	have := map[string]bool{}
	for _, d := range got.Details {
		have[d.Key] = true
	}
	for _, key := range f.NativeDetails {
		if !have[key] {
			t.Errorf("account details missing %q: %+v", key, got.Details)
		}
	}
}

// runMissingAccount pins the account-existence contract: chains with an
// existence concept (Solana) report a never-seen address as
// ErrAccountNotFound, chains without one (EVM) report an empty account.
// Either way the native balance of an unfunded address is zero, never an
// error — the chain reports an unfunded balance, it does not fail the
// query.
func runMissingAccount(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	missing := mustAddress(t, f.Family, f.MissingAddress)

	acc, err := s.Accounts().Account(context.Background(), missing)
	if f.ReportsAccountNotFound {
		if !errors.Is(err, account.ErrAccountNotFound) {
			t.Errorf("Account = %v, want ErrAccountNotFound", err)
		}
	} else if err != nil {
		t.Errorf("Account = %v, want a record for an empty address", err)
	} else if acc.NativeBalance == nil || acc.NativeBalance.Raw != "0" {
		t.Errorf("Account native balance = %+v, want zero", acc.NativeBalance)
	}

	bal, err := s.Accounts().Balance(context.Background(), missing)
	if err != nil {
		t.Errorf("Balance of an unfunded address = %v, want zero", err)
	}
	if bal.Raw != "0" {
		t.Errorf("Balance of an unfunded address = %q, want 0", bal.Raw)
	}
}

// runNativeAsset pins §13.2: native-asset metadata comes from the
// adapter's family knowledge, with no symbol invented for it.
func runNativeAsset(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	native := nativeAssetID(s.Net().Network())

	got, err := s.Assets().Asset(context.Background(), native)
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("asset invalid: %v", err)
	}
	if got.Decimals != f.NativeDecimals {
		t.Errorf("native decimals = %d, want %d", got.Decimals, f.NativeDecimals)
	}
	if got.Symbol != "" || got.Name != "" {
		t.Errorf("native asset must carry no invented metadata: %+v", got)
	}
	if !got.Asset.Equal(native) {
		t.Errorf("asset id = %+v, want %+v", got.Asset, native)
	}

	bal, err := s.Assets().Balance(context.Background(), native, mustAddress(t, f.Family, f.KnownAddress))
	if err != nil {
		t.Fatalf("native Balance: %v", err)
	}
	if bal.Raw != f.NativeBalance || bal.Decimals != f.NativeDecimals {
		t.Errorf("native balance = %+v, want %q at %d decimals", bal, f.NativeBalance, f.NativeDecimals)
	}
}

// runToken pins §13.2 for token assets: precision comes from the chain,
// the balance keeps the exact raw amount, and the holder is the address
// the caller named.
func runToken(t *testing.T, f Fixture) {
	t.Helper()
	token := f.Token
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })

	info, err := s.Assets().Asset(context.Background(), token.AssetID)
	if err != nil {
		t.Fatalf("Asset: %v", err)
	}
	if err := info.Validate(); err != nil {
		t.Fatalf("asset invalid: %v", err)
	}
	if info.Decimals != token.Decimals {
		t.Errorf("token decimals = %d, want %d", info.Decimals, token.Decimals)
	}
	if !info.Asset.Equal(token.AssetID) {
		t.Errorf("asset id = %+v, want %+v", info.Asset, token.AssetID)
	}

	bal, err := s.Assets().Balance(context.Background(), token.AssetID, mustAddress(t, f.Family, token.Holder))
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if err := bal.Validate(); err != nil {
		t.Fatalf("amount invalid: %v", err)
	}
	if bal.Raw != token.Balance {
		t.Errorf("token balance raw = %q, want %q", bal.Raw, token.Balance)
	}
	if bal.Decimals != token.Decimals {
		t.Errorf("token balance decimals = %d, want %d", bal.Decimals, token.Decimals)
	}
	if bal.AssetID != token.AssetID.String() {
		t.Errorf("token balance asset = %q, want %q", bal.AssetID, token.AssetID.String())
	}
}

// runForeignKind pins §16.2 不支持的能力返回明确错误 at the asset level:
// an asset kind owned by the other family answers with an explicit
// unsupported error, not a chain call and not a guess.
func runForeignKind(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	if err := f.ForeignKindAsset.Validate(); err != nil {
		t.Fatalf("fixture foreign-kind asset invalid: %v", err)
	}
	if _, err := s.Assets().Asset(context.Background(), f.ForeignKindAsset); !errors.Is(err, cherrs.ErrUnsupportedCapability) {
		t.Errorf("Asset = %v, want unsupported capability", err)
	}
	if _, err := s.Assets().Balance(context.Background(), f.ForeignKindAsset, mustAddress(t, f.Family, f.KnownAddress)); !errors.Is(err, cherrs.ErrUnsupportedCapability) {
		t.Errorf("Balance = %v, want unsupported capability", err)
	}
}

// runUnhealthy pins the health contract: a failing endpoint makes every
// capability view report unreachable with a classified error.
func runUnhealthy(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	s.Break()

	if err := s.Net().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Net.Healthy = %v, want network unavailable", err)
	}
	if err := s.Accounts().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Accounts.Healthy = %v, want network unavailable", err)
	}
	if err := s.Assets().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Assets.Healthy = %v, want network unavailable", err)
	}
}

// runClosed pins §16.2 关闭适配器后不再接受新请求: after Stop the
// adapter answers with a classified error, never a panic and never a
// dial into closed endpoints.
func runClosed(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	if err := s.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	holder := mustAddress(t, f.Family, f.KnownAddress)

	if err := s.Net().Healthy(context.Background()); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Net.Healthy = %v, want network unavailable", err)
	}
	if _, err := s.Accounts().Balance(context.Background(), holder); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Accounts.Balance = %v, want network unavailable", err)
	}
	if _, err := s.Accounts().Account(context.Background(), holder); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Accounts.Account = %v, want network unavailable", err)
	}
	if _, err := s.Assets().Balance(context.Background(), nativeAssetID(s.Net().Network()), holder); !errors.Is(err, cherrs.ErrNetworkUnavailable) {
		t.Errorf("Assets.Balance = %v, want network unavailable", err)
	}
}

// runCancelled pins §16.2 取消上下文能够传递到 RPC 调用: a cancelled
// context aborts the call instead of reaching the endpoint.
func runCancelled(t *testing.T, f Fixture) {
	t.Helper()
	s := f.New(t)
	t.Cleanup(func() { _ = s.Stop(context.Background()) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	holder := mustAddress(t, f.Family, f.KnownAddress)

	if _, err := s.Accounts().Balance(ctx, holder); !errors.Is(err, context.Canceled) {
		t.Errorf("Balance with cancelled context = %v, want context.Canceled", err)
	}
	if _, err := s.Assets().Balance(ctx, nativeAssetID(s.Net().Network()), holder); !errors.Is(err, context.Canceled) {
		t.Errorf("asset Balance with cancelled context = %v, want context.Canceled", err)
	}
}

// runStatusSeam pins the reconciliation contract every adapter shares
// (spec §7.8): a record without a chain reference reports its own status
// without going to the chain, an execution the chain reports as failed is
// terminal whatever depth it reached, a succeeded execution maps to the
// family's settled status, a reference the chain holds nothing for stays
// unknown rather than guessed at, and a failed query is reported as a
// failure, never as a status. The family-specific ladder — pending from a
// live window, confirmation depths — stays in each adapter's own tests.
func runStatusSeam(t *testing.T, f Fixture) {
	t.Helper()
	st := *f.Status
	recordWith := func(id, chainHash string) transaction.Transaction {
		return transaction.Transaction{
			ID:        transaction.TransactionID(id),
			Status:    transaction.TxSubmitted,
			ChainHash: chainHash,
		}
	}

	t.Run("OwnStatusWithoutAReference", func(t *testing.T) {
		s := f.New(t)
		t.Cleanup(func() { _ = s.Stop(context.Background()) })
		// The chain is unreachable: the answer must still come back,
		// because a record with no reference has nothing to ask about.
		s.Break()
		status, err := s.StatusOf(context.Background(), recordWith("conf-nohash", ""))
		if err != nil {
			t.Fatalf("StatusOf without a reference over a broken chain: %v", err)
		}
		if status != transaction.TxSubmitted {
			t.Errorf("status = %s, want the record's own submitted", status)
		}
	})

	t.Run("SettledExecution", func(t *testing.T) {
		s := f.New(t)
		t.Cleanup(func() { _ = s.Stop(context.Background()) })
		status, err := s.StatusOf(context.Background(), recordWith("conf-settled", st.Settled))
		if err != nil {
			t.Fatalf("StatusOf of a settled execution: %v", err)
		}
		if status != st.SettledStatus {
			t.Errorf("status = %s, want %s", status, st.SettledStatus)
		}
	})

	t.Run("FailedExecution", func(t *testing.T) {
		s := f.New(t)
		t.Cleanup(func() { _ = s.Stop(context.Background()) })
		status, err := s.StatusOf(context.Background(), recordWith("conf-reverted", st.Reverted))
		if err != nil {
			t.Fatalf("StatusOf of a failed execution: %v", err)
		}
		if status != transaction.TxReverted {
			t.Errorf("status = %s, want reverted: a failed execution is terminal", status)
		}
	})

	t.Run("UnobservedReference", func(t *testing.T) {
		s := f.New(t)
		t.Cleanup(func() { _ = s.Stop(context.Background()) })
		status, err := s.StatusOf(context.Background(), recordWith("conf-unobserved", st.Unobserved))
		if err != nil {
			t.Fatalf("StatusOf of an unobserved reference: %v", err)
		}
		if status != transaction.TxUnknown {
			t.Errorf("status = %s, want unknown: an open outcome, not a guess", status)
		}
	})

	t.Run("QueryFailurePropagates", func(t *testing.T) {
		s := f.New(t)
		t.Cleanup(func() { _ = s.Stop(context.Background()) })
		s.Break()
		if _, err := s.StatusOf(context.Background(), recordWith("conf-err", st.Settled)); err == nil {
			t.Fatal("StatusOf over a broken chain: err = nil, want error — a failed query is not evidence of anything")
		}
	})
}
