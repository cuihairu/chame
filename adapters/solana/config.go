package solana

import (
	"fmt"
	"net/url"
	"time"

	"github.com/gagliardetto/solana-go"

	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// DefaultRequestTimeout bounds one RPC request when the config does not
// say otherwise.
const DefaultRequestTimeout = 10 * time.Second

// Commitment is the Solana confirmation level (spec §8.5). It is a plain
// string type rather than the SDK's, so the SDK stays inside this package
// (ADR-0001). Solana's processed/confirmed/finalized are NOT equivalent to
// EVM confirmation depth; an application picks the level its business needs
// and records what it actually observed (spec §8.5).
type Commitment string

const (
	// CommitmentProcessed observes the slot the node has just processed.
	CommitmentProcessed Commitment = "processed"
	// CommitmentConfirmed observes supermajority confirmation.
	CommitmentConfirmed Commitment = "confirmed"
	// CommitmentFinalized observes the cluster's finalised state.
	CommitmentFinalized Commitment = "finalized"
)

// DefaultCommitment applies when a network does not configure one. Reads
// are not latency-critical here; finalised state avoids serving a slot a
// later fork could discard.
const DefaultCommitment = CommitmentFinalized

// Valid reports whether c names a level the Solana RPC understands.
func (c Commitment) Valid() bool {
	switch c {
	case CommitmentProcessed, CommitmentConfirmed, CommitmentFinalized:
		return true
	default:
		return false
	}
}

// rpc converts to the SDK's parameter type at the seam.
func (c Commitment) rpc() rpcCommitmentType { return rpcCommitmentType(c) }

// NetworkConfig is one logical Solana network instance (spec §8.1).
type NetworkConfig struct {
	// Name is the logical network name used in NetworkID.
	Name string
	// ExpectedGenesisHash is the cluster identity, base58 of a 32-byte
	// value. It is never hardcoded: the application declares the cluster it
	// talks to, and every endpoint is verified against it (spec §8.2).
	ExpectedGenesisHash string
	// Endpoints is the RPC endpoint pool. More than one enables read
	// failover (spec §7.4).
	Endpoints []string
	// RequestTimeout bounds one RPC request. Zero uses
	// DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Commitment is the confirmation level read calls default to.
	Commitment Commitment
}

// networkID builds the chain-agnostic identity for the instance. For
// Solana there is no integer chain id; the genesis hash is the cluster
// identity, so it is what carries the identifier (spec §6.2: ChainID is the
// in-family protocol identifier, not always an integer).
func (c NetworkConfig) networkID() identifiers.NetworkID {
	return identifiers.NetworkID{
		Family:  identifiers.FamilySolana,
		Name:    c.Name,
		ChainID: c.ExpectedGenesisHash,
	}
}

// timeout returns the effective per-request timeout.
func (c NetworkConfig) timeout() time.Duration {
	if c.RequestTimeout <= 0 {
		return DefaultRequestTimeout
	}
	return c.RequestTimeout
}

// commitment returns the effective default confirmation level.
func (c NetworkConfig) commitment() Commitment {
	if !c.Commitment.Valid() {
		return DefaultCommitment
	}
	return c.Commitment
}

// Config is the whole adapter configuration.
type Config struct {
	Networks []NetworkConfig
}

// Validate checks the configuration (spec §5.4 spirit: fail at startup,
// not mid-request).
func (c Config) Validate() error {
	if len(c.Networks) == 0 {
		return fmt.Errorf("solana: at least one network is required")
	}
	seen := make(map[string]bool, len(c.Networks))
	for _, n := range c.Networks {
		if n.ExpectedGenesisHash == "" {
			return fmt.Errorf("solana: network %q: expected genesis hash is required", n.Name)
		}
		if err := n.networkID().Validate(); err != nil {
			return fmt.Errorf("solana: %w", err)
		}
		if seen[n.Name] {
			return fmt.Errorf("solana: duplicate network name %q", n.Name)
		}
		seen[n.Name] = true
		if _, err := genesisHash(n.ExpectedGenesisHash); err != nil {
			return fmt.Errorf("solana: network %q: %v", n.Name, err)
		}
		if len(n.Endpoints) == 0 {
			return fmt.Errorf("solana: network %q: at least one endpoint is required", n.Name)
		}
		for _, ep := range n.Endpoints {
			u, err := url.Parse(ep)
			if err != nil {
				return fmt.Errorf("solana: network %q: bad endpoint %q: %v", n.Name, config.RedactURL(ep), err)
			}
			switch u.Scheme {
			case "http", "https":
			default:
				return fmt.Errorf("solana: network %q: endpoint scheme must be http(s), got %q", n.Name, u.Scheme)
			}
		}
		if n.RequestTimeout < 0 {
			return fmt.Errorf("solana: network %q: request timeout must not be negative", n.Name)
		}
		if n.Commitment != "" && !n.Commitment.Valid() {
			return fmt.Errorf("solana: network %q: commitment must be processed/confirmed/finalized, got %q", n.Name, n.Commitment)
		}
	}
	return nil
}

// genesisHash validates a base58 cluster identifier.
func genesisHash(s string) (solana.Hash, error) {
	pk, err := solana.PublicKeyFromBase58(s)
	if err != nil {
		return solana.Hash{}, fmt.Errorf("expected genesis hash must be base58 of 32 bytes: %v", err)
	}
	return solana.Hash(pk), nil
}
