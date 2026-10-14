package transaction

import (
	"errors"
	"testing"
	"time"

	cherrs "github.com/cuihairu/chame/core/errors"
	"github.com/cuihairu/chame/core/identifiers"
)

func TestIdempotencyScopeValidate(t *testing.T) {
	cases := []struct {
		name    string
		scope   IdempotencyScope
		wantErr bool
	}{
		{"valid", IdempotencyScope{Application: "game", OperationType: "payout"}, false},
		{"empty application", IdempotencyScope{OperationType: "payout"}, true},
		{"empty operation type", IdempotencyScope{Application: "game"}, true},
		{"empty both", IdempotencyScope{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.scope.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}

	t.Run("string renders both parts", func(t *testing.T) {
		s := IdempotencyScope{Application: "game", OperationType: "payout"}
		if got := s.String(); got != "game/payout" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestStaleQueryValidate(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		query   StaleQuery
		wantErr bool
	}{
		{"valid", StaleQuery{Statuses: []TransactionStatus{TxUnknown}, OlderThan: base}, false},
		{"no statuses", StaleQuery{OlderThan: base}, true},
		{"unknown status", StaleQuery{Statuses: []TransactionStatus{"bogus"}, OlderThan: base}, true},
		{"negative limit", StaleQuery{Statuses: []TransactionStatus{TxUnknown}, OlderThan: base, Limit: -1}, true},
		{"zero limit is unbounded", StaleQuery{Statuses: []TransactionStatus{TxUnknown}, OlderThan: base, Limit: 0}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.query.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestLeaseValidate(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		lease   Lease
		wantErr bool
	}{
		{"valid", Lease{Holder: "worker-1", ExpiresAt: at}, false},
		{"no holder", Lease{ExpiresAt: at}, true},
		{"no expiry", Lease{Holder: "worker-1"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.lease.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateRecord(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	net := identifiers.NetworkID{Family: identifiers.FamilyEVM, Name: "sepolia", ChainID: "11155111"}

	valid := Transaction{
		ID:          "tx-1",
		OperationID: "op-1",
		Network:     net,
		Status:      TxCreated,
		CreatedAt:   at,
		UpdatedAt:   at,
	}

	t.Run("accepts a plain record", func(t *testing.T) {
		if err := ValidateRecord(valid); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("accepts a keyed record", func(t *testing.T) {
		tx := valid
		tx.IdempotencyKey = "key"
		tx.IdempotencyScope = IdempotencyScope{Application: "game", OperationType: "payout"}
		tx.RequestHash = "hash"
		if err := ValidateRecord(tx); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rejects a keyed record without hash", func(t *testing.T) {
		tx := valid
		tx.IdempotencyKey = "key"
		tx.IdempotencyScope = IdempotencyScope{Application: "game", OperationType: "payout"}
		err := ValidateRecord(tx)
		if err == nil {
			t.Fatalf("want error")
		}
	})

	t.Run("rejects a keyed record without scope", func(t *testing.T) {
		tx := valid
		tx.IdempotencyKey = "key"
		tx.RequestHash = "hash"
		if err := ValidateRecord(tx); err == nil {
			t.Fatalf("want error")
		}
	})

	cases := []struct {
		name string
		mut  func(tx Transaction) Transaction
	}{
		{"empty id", func(tx Transaction) Transaction { tx.ID = ""; return tx }},
		{"invalid network", func(tx Transaction) Transaction { tx.Network.Name = ""; return tx }},
		{"unknown status", func(tx Transaction) Transaction { tx.Status = "bogus"; return tx }},
		{"zero created", func(tx Transaction) Transaction { tx.CreatedAt = time.Time{}; return tx }},
		{"zero updated", func(tx Transaction) Transaction { tx.UpdatedAt = time.Time{}; return tx }},
		{"non-canonical amount", func(tx Transaction) Transaction {
			tx.Amount = identifiers.Amount{Raw: "0x10", AssetID: "native"}
			return tx
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateRecord(tc.mut(valid)); err == nil {
				t.Fatalf("want error")
			}
		})
	}
}

func TestRepositorySentinelsAreDistinct(t *testing.T) {
	if errors.Is(ErrRecordNotFound, ErrStatusConflict) || errors.Is(ErrStatusConflict, ErrRecordNotFound) {
		t.Fatalf("sentinels must be distinct")
	}
	if errors.Is(ErrStatusConflict, cherrs.ErrIdempotencyConflict) {
		t.Fatalf("idempotency conflict is its own taxonomy category")
	}
}
