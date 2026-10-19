package transaction

import (
	"context"
	"errors"
	"sync"
	"time"

	txmodel "github.com/cuihairu/chame/capability/transaction"
	"github.com/cuihairu/chame/core/identifiers"
)

// ControlRequest is the host supplied view of a payout or other issuance.
type ControlRequest struct {
	Operator, Action, IdempotencyKey string
	Network                          identifiers.NetworkID
	Amount                           string
	At                               time.Time
}

// Approver is a host supplied human approval gate.
type Approver interface {
	Approve(context.Context, ControlRequest) (bool, error)
}

// AuditLog stores immutable control decisions and supports host queries.
type AuditLog interface {
	Append(context.Context, AuditEntry) error
	Query(context.Context, AuditQuery) ([]AuditEntry, error)
}
type AuditEntry struct {
	Operator, Action, IdempotencyKey, Result, Reason string
	Network                                          identifiers.NetworkID
	At                                               time.Time
}
type AuditQuery struct {
	Operator, IdempotencyKey string
	Since, Until             time.Time
}
type ControlRequestFunc func(txmodel.Transaction) ControlRequest

// Limit applies per-operation and UTC-day budgets to a network/action pair.
type Limit struct {
	Single, Daily int64
	mu            sync.Mutex
	spent         map[string]int64
}

var ErrControlDenied = errors.New("transaction: internal control denied")

func (l *Limit) Check(r ControlRequest) error {
	if l == nil {
		return nil
	}
	var amount int64
	for _, c := range r.Amount {
		if c < '0' || c > '9' {
			return ErrControlDenied
		}
		amount = amount*10 + int64(c-'0')
	}
	key := r.Network.String() + "/" + r.Action + "/" + r.At.UTC().Format("2006-01-02")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.spent == nil {
		l.spent = map[string]int64{}
	}
	if amount <= 0 || (l.Single > 0 && amount > l.Single) || (l.Daily > 0 && l.spent[key]+amount > l.Daily) {
		return ErrControlDenied
	}
	l.spent[key] += amount
	return nil
}
