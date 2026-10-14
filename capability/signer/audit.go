package signer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/cuihairu/chame/core/clock"
	"github.com/cuihairu/chame/core/identifiers"
)

// AuditEntry records one signing attempt (spec §12.3: 签名请求应可审计).
// It carries a digest of the payload — enough to correlate a signature
// with the exact bytes — never the payload itself and never key material.
type AuditEntry struct {
	// At is when the attempt completed.
	At time.Time
	// Signer is the descriptor name of the signer.
	Signer string
	// Network repeats the explicit request target network.
	Network identifiers.NetworkID
	// Account is the explicit request account.
	Account identifiers.Address
	// Type is the requested signing scheme.
	Type SignatureType
	// Digest is the hex SHA-256 of the payload.
	Digest string
	// Denied carries the rejection reason; empty when the request was
	// signed.
	Denied string
}

// PayloadDigest returns the hex SHA-256 of a payload, the form audit
// entries use instead of raw bytes.
func PayloadDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// WithAudit wraps a signer so every Sign call — allowed or denied —
// produces one AuditEntry (spec §12.3: 签名请求应可审计). A nil hook
// returns the signer unchanged; a nil clock uses the system clock.
func WithAudit(s Signer, hook func(AuditEntry), now clock.Clock) Signer {
	if hook == nil {
		return s
	}
	if now == nil {
		now = clock.System{}
	}
	return auditedSigner{next: s, hook: hook, now: now}
}

// auditedSigner is the WithAudit decorator.
type auditedSigner struct {
	next Signer
	hook func(AuditEntry)
	now  clock.Clock
}

var _ Signer = auditedSigner{}

// Descriptor passes through.
func (a auditedSigner) Descriptor() Descriptor { return a.next.Descriptor() }

// Accounts passes through.
func (a auditedSigner) Accounts() []AccountRef { return a.next.Accounts() }

// Sign forwards the request, then records the outcome.
func (a auditedSigner) Sign(ctx context.Context, req Request) (Signed, error) {
	out, err := a.next.Sign(ctx, req)
	entry := AuditEntry{
		At:      a.now.Now(),
		Signer:  a.next.Descriptor().Name,
		Network: req.Network,
		Account: req.Account,
		Type:    req.Type,
		Digest:  PayloadDigest(req.Payload),
	}
	if err != nil {
		entry.Denied = err.Error()
	}
	a.hook(entry)
	return out, err
}
