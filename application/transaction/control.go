package transaction

import (
	"context"
	"errors"
	"math"
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

// parseAmount reads a plain decimal string. Anything non-numeric or too
// large for int64 is a refusal, never a wrapped value: an overflow would
// silently turn a huge payout into a small one that passes the budgets.
func parseAmount(s string) (int64, bool) {
	var amount int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int64(c - '0')
		if amount > (math.MaxInt64-d)/10 {
			return 0, false
		}
		amount = amount*10 + d
	}
	return amount, true
}

func (l *Limit) Check(r ControlRequest) error {
	if l == nil {
		return nil
	}
	amount, ok := parseAmount(r.Amount)
	if !ok {
		return ErrControlDenied
	}
	key := r.Network.String() + "/" + r.Action + "/" + r.At.UTC().Format("2006-01-02")
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.spent == nil {
		l.spent = map[string]int64{}
	}
	// The remaining budget is a subtraction, not a sum: spent+amount
	// could overflow and pass the gate on a wrapped negative.
	if amount <= 0 || (l.Single > 0 && amount > l.Single) || (l.Daily > 0 && amount > l.Daily-l.spent[key]) {
		return ErrControlDenied
	}
	l.spent[key] += amount
	return nil
}
