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
	// reserved holds one live reservation per idempotency key, mapped to
	// the budget bucket it charged. A record re-driven while its
	// reservation is held passes the gate again without a second charge:
	// a transient stop must not stack charges on the day budget, and two
	// drives of one record share the reservation the first drive placed.
	reserved map[string]string
	day      string
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
// request reserves nothing. It reports whether this call placed a
// charge: a request whose idempotency key already holds a reservation
// passes the gate free, and only the call that charged may return the
// reservation later. Call Release for a charged request whose operation
// does not execute (approval denied, or a later step stopped before the
// record left created): a refusal must not consume the budget.
func (l *Limit) Check(r ControlRequest) (bool, error) {
	if l == nil {
		return false, nil
	}
	amount, ok := parseAmount(r.Amount)
	if !ok {
		return false, ErrControlDenied
	}
	day := r.At.UTC().Format("2006-01-02")
	key := budgetKey(r, day)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.spent == nil {
		l.spent = map[string]int64{}
	}
	// Days roll over by UTC date: drop buckets of earlier days so the
	// maps stay bounded by the live network/action pairs. Reservations
	// die with their day — a record still created on the new day is
	// charged against the new day's budget when it is driven again.
	if l.day != day {
		for k := range l.spent {
			if !strings.HasSuffix(k, "/"+day) {
				delete(l.spent, k)
			}
		}
		l.reserved = nil
		l.day = day
	}
	if r.IdempotencyKey != "" {
		if _, held := l.reserved[r.IdempotencyKey]; held {
			return false, nil // this record's reservation is already live
		}
	}
	// The remaining budget is a subtraction, not a sum: spent+amount
	// could overflow and pass the gate on a wrapped negative.
	if amount <= 0 || (l.Single > 0 && amount > l.Single) || (l.Daily > 0 && amount > l.Daily-l.spent[key]) {
		return false, ErrControlDenied
	}
	l.spent[key] += amount
	if r.IdempotencyKey != "" {
		if l.reserved == nil {
			l.reserved = map[string]string{}
		}
		l.reserved[r.IdempotencyKey] = key
	}
	return true, nil
}

// Release returns the reservation a prior Check placed for the same
// request. Only the call that charged may release: a request whose
// reservation is not live (never charged, already released, or held by
// another drive of the same record) releases nothing. A request without
// an idempotency key cannot be tracked, so it releases the amount its
// Check charged, once. Releasing below the day's spend deletes the
// bucket.
func (l *Limit) Release(r ControlRequest) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if r.IdempotencyKey != "" {
		key, held := l.reserved[r.IdempotencyKey]
		if !held {
			return
		}
		delete(l.reserved, r.IdempotencyKey)
		if amount, ok := parseAmount(r.Amount); ok {
			l.spent[key] -= amount
			if l.spent[key] <= 0 {
				delete(l.spent, key)
			}
		}
		return
	}
	amount, ok := parseAmount(r.Amount)
	if !ok || amount <= 0 {
		return
	}
	day := r.At.UTC().Format("2006-01-02")
	key := budgetKey(r, day)
	l.spent[key] -= amount
	if l.spent[key] <= 0 {
		delete(l.spent, key)
	}
}
