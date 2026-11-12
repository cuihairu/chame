package transaction

import (
	"context"
	"errors"
	"math"
	"strings"
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
// Check reserves the amount; Release returns it when the operation is
// refused after the gate passed. A reserved amount that is never released
// stays spent — the budget errs on the side of refusing.
type Limit struct {
	Single, Daily int64
	mu            sync.Mutex
	spent         map[string]int64
	day           string
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

// budgetKey addresses one (network, action, UTC day) budget bucket.
func budgetKey(r ControlRequest, day string) string {
	return r.Network.String() + "/" + r.Action + "/" + day
}

// Check decides whether the request fits the per-operation and UTC-day
// budgets and, on success, reserves the amount against them. A refused
// request reserves nothing. Call Release for a reserved request whose
// operation does not execute (approval denied, or a later step failed
// before the record left created): a refusal must not consume the budget.
func (l *Limit) Check(r ControlRequest) error {
	if l == nil {
		return nil
	}
	amount, ok := parseAmount(r.Amount)
	if !ok {
		return ErrControlDenied
	}
	day := r.At.UTC().Format("2006-01-02")
	key := budgetKey(r, day)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.spent == nil {
		l.spent = map[string]int64{}
	}
	// Days roll over by UTC date: drop buckets of earlier days so the
	// map stays bounded by the live network/action pairs.
	if l.day != day {
		for k := range l.spent {
			if !strings.HasSuffix(k, "/"+day) {
				delete(l.spent, k)
			}
		}
		l.day = day
	}
	// The remaining budget is a subtraction, not a sum: spent+amount
	// could overflow and pass the gate on a wrapped negative.
	if amount <= 0 || (l.Single > 0 && amount > l.Single) || (l.Daily > 0 && amount > l.Daily-l.spent[key]) {
		return ErrControlDenied
	}
	l.spent[key] += amount
	return nil
}

// Release returns the amount a prior Check reserved for the same request.
// A request whose amount cannot be parsed releases nothing — Check never
// reserved it. Releasing below the day's spend deletes the bucket.
func (l *Limit) Release(r ControlRequest) {
	if l == nil {
		return
	}
	amount, ok := parseAmount(r.Amount)
	if !ok || amount <= 0 {
		return
	}
	day := r.At.UTC().Format("2006-01-02")
	key := budgetKey(r, day)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.spent[key] -= amount
	if l.spent[key] <= 0 {
		delete(l.spent, key)
	}
}
