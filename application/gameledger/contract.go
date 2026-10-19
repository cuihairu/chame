// Package gameledger defines small, host-facing contracts for projecting
// chain observations into a game-owned ledger. It is not a production ledger.
package gameledger

import (
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/cuihairu/chame/core/identifiers"
)

// Standard preserves the token standard in the identity. Similar-looking
// assets under different standards must never be collapsed into one key.
type Standard string

const (
	StandardNative  Standard = "native"
	StandardERC20   Standard = "erc20"
	StandardERC721  Standard = "erc721"
	StandardERC1155 Standard = "erc1155"
	StandardSPL     Standard = "spl-token"
	StandardSPLNFT  Standard = "spl-nft"
)

// AssetIdentity is chain-qualified. Contract is an EVM contract or Solana
// mint; TokenID is required only for token-id standards. TokenID is a
// canonical unsigned decimal string, avoiding integer-width assumptions.
type AssetIdentity struct {
	Network  identifiers.NetworkID `json:"network"`
	Standard Standard              `json:"standard"`
	Contract string                `json:"contract,omitempty"`
	TokenID  string                `json:"token_id,omitempty"`
}

// Normalize validates and returns a canonical identity. EVM addresses are
// lowercased; Solana base58 values remain case-sensitive and unchanged.
func (a AssetIdentity) Normalize() (AssetIdentity, error) {
	if err := a.Network.Validate(); err != nil {
		return AssetIdentity{}, err
	}
	switch a.Standard {
	case StandardNative:
		if a.Contract != "" || a.TokenID != "" {
			return AssetIdentity{}, errors.New("native identity must not carry contract or token id")
		}
	case StandardERC20, StandardERC721, StandardERC1155:
		if a.Network.Family != identifiers.FamilyEVM {
			return AssetIdentity{}, errors.New("EVM standard requires EVM network")
		}
		if a.Contract == "" {
			return AssetIdentity{}, errors.New("contract is required")
		}
		addr, err := identifiers.NewEVMAddress(a.Contract)
		if err != nil {
			return AssetIdentity{}, err
		}
		a.Contract = "0x" + strings.ToLower(strings.TrimPrefix(addr.Value, "0x"))
		if a.Standard == StandardERC20 && a.TokenID != "" {
			return AssetIdentity{}, errors.New("ERC-20 has no token id")
		}
		if a.Standard == StandardERC721 || a.Standard == StandardERC1155 {
			a.TokenID, err = canonicalUint(a.TokenID)
			if err != nil {
				return AssetIdentity{}, err
			}
		}
	case StandardSPL, StandardSPLNFT:
		if a.Network.Family != identifiers.FamilySolana {
			return AssetIdentity{}, errors.New("SPL standard requires Solana network")
		}
		if a.Contract == "" {
			return AssetIdentity{}, errors.New("mint is required")
		}
		if _, err := identifiers.NewSolanaAddress(a.Contract); err != nil {
			return AssetIdentity{}, err
		}
		if a.Standard == StandardSPL && a.TokenID != "" {
			return AssetIdentity{}, errors.New("fungible SPL identity has no token id")
		}
		if a.Standard == StandardSPLNFT {
			var err error
			a.TokenID, err = canonicalUint(a.TokenID)
			if err != nil {
				return AssetIdentity{}, err
			}
		}
	default:
		return AssetIdentity{}, fmt.Errorf("unsupported asset standard %q", a.Standard)
	}
	return a, nil
}

func canonicalUint(s string) (string, error) {
	if s == "" {
		return "", errors.New("token id is required")
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return "", fmt.Errorf("token id must be an unsigned decimal integer")
	}
	return n.String(), nil
}

// ObservationState is the projection's treatment of a chain event.
type ObservationState string

const (
	Pending   ObservationState = "pending"
	Confirmed ObservationState = "confirmed"
	Final     ObservationState = "final"
	Reorged   ObservationState = "reorged"
)

// EventKey must identify one canonical chain event, e.g. network + tx hash +
// log index (EVM) or network + signature + instruction/event index (Solana).
// It is deliberately independent of a player's request idempotency key.
type EventKey string

// Entry is a minimal example projection record. Business schemas and storage
// remain owned by the host game.
type Entry struct {
	Key         EventKey
	Player      string
	Asset       AssetIdentity
	Delta       int64
	State       ObservationState
	Compensated bool
}

// Ledger is an executable reference contract for exactly-once projection.
// Production hosts should enforce equivalent uniqueness transactionally.
type Ledger struct{ entries map[EventKey]Entry }

func NewLedger() *Ledger { return &Ledger{entries: make(map[EventKey]Entry)} }

// Apply inserts an observation once. A duplicate key is a no-op, matching
// repository CreateInserted/CreateSameKeyExisting semantics.
func (l *Ledger) Apply(e Entry) (inserted bool, err error) {
	if e.Key == "" || e.Player == "" {
		return false, errors.New("event key and player are required")
	}
	a, err := e.Asset.Normalize()
	if err != nil {
		return false, err
	}
	e.Asset = a
	if e.State != Pending && e.State != Confirmed && e.State != Final {
		return false, errors.New("new entry state must be pending, confirmed, or final")
	}
	if old, ok := l.entries[e.Key]; ok {
		if old.Player != e.Player || old.Asset != e.Asset || old.Delta != e.Delta {
			return false, errors.New("event key reused with different payload")
		}
		return false, nil
	}
	l.entries[e.Key] = e
	return true, nil
}

// SetState tracks finality/reorg. Reorg compensation is marked once; host
// code must apply the inverse business operation in the same DB transaction.
func (l *Ledger) SetState(key EventKey, state ObservationState) (compensate int64, err error) {
	e, ok := l.entries[key]
	if !ok {
		return 0, errors.New("entry not found")
	}
	if state != Pending && state != Confirmed && state != Final && state != Reorged {
		return 0, errors.New("invalid observation state")
	}
	if e.State == Reorged && state != Reorged {
		return 0, errors.New("reorged event cannot be resurrected")
	}
	if state != Reorged && observationRank(state) < observationRank(e.State) {
		return 0, errors.New("observation state cannot move backwards")
	}
	if state == Reorged && !e.Compensated && (e.State == Confirmed || e.State == Final) {
		e.Compensated = true
		compensate = -e.Delta
	}
	if state == Reorged {
		e.Compensated = true
	}
	e.State = state
	l.entries[key] = e
	return compensate, nil
}

func observationRank(s ObservationState) int {
	switch s {
	case Pending:
		return 0
	case Confirmed:
		return 1
	case Final:
		return 2
	default:
		return -1
	}
}

// SpendableDelta excludes pending and reorged observations.
func (l *Ledger) SpendableDelta(key EventKey) (int64, bool) {
	e, ok := l.entries[key]
	if !ok || (e.State != Confirmed && e.State != Final) || e.Compensated {
		return 0, false
	}
	return e.Delta, true
}

func (l *Ledger) Entry(key EventKey) (Entry, bool) { e, ok := l.entries[key]; return e, ok }
