package evm

import (
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/cuihairu/chame/core/config"
	"github.com/cuihairu/chame/core/identifiers"
)

// DefaultRequestTimeout bounds one RPC request when the config does not
// say otherwise.
const DefaultRequestTimeout = 10 * time.Second

// FinalityConfig is the per-network finality policy (spec §7.8). Reads
// in Phase 3 always target the latest block; the policy governs the
// confirmation and finality tracking that arrives with the transaction
// capability.
type FinalityConfig struct {
	// UseFinalizedTag prefers the finalized/safe block tags on networks
	// that support them.
	UseFinalizedTag bool
	// Confirmations is the fallback depth when the network offers no
	// finalized tag. An application-layer policy, not an absolute
	// irreversibility guarantee (spec §7.8).
	Confirmations uint64
}

const defaultConfirmations = 12

func (f FinalityConfig) withDefaults() FinalityConfig {
	if f.Confirmations == 0 {
		f.Confirmations = defaultConfirmations
	}
	return f
}

// NetworkConfig is one logical EVM network instance (spec §7.3).
type NetworkConfig struct {
	// Name is the logical network name used in NetworkID.
	Name string
	// ChainID is the expected EIP-155 chain identifier, verified at
	// start and on every health check.
	ChainID uint64
	// Endpoints is the RPC endpoint pool. More than one enables
	// read failover (spec §7.4).
	Endpoints []string
	// RequestTimeout bounds one RPC request. Zero uses
	// DefaultRequestTimeout.
	RequestTimeout time.Duration
	// Finality is the per-network finality policy.
	Finality FinalityConfig
}

// networkID builds the chain-agnostic identity for the instance.
func (c NetworkConfig) networkID() identifiers.NetworkID {
	return identifiers.NetworkID{
		Family:  identifiers.FamilyEVM,
		Name:    c.Name,
		ChainID: strconv.FormatUint(c.ChainID, 10),
	}
}

// timeout returns the effective per-request timeout.
func (c NetworkConfig) timeout() time.Duration {
	if c.RequestTimeout <= 0 {
		return DefaultRequestTimeout
	}
	return c.RequestTimeout
}

// Config is the whole adapter configuration.
type Config struct {
	Networks []NetworkConfig
}

// Validate checks the configuration (spec §5.4 spirit: fail at startup,
// not mid-request).
func (c Config) Validate() error {
	if len(c.Networks) == 0 {
		return fmt.Errorf("evm: at least one network is required")
	}
	seen := make(map[string]bool, len(c.Networks))
	for _, n := range c.Networks {
		if err := n.networkID().Validate(); err != nil {
			return fmt.Errorf("evm: %w", err)
		}
		if seen[n.Name] {
			return fmt.Errorf("evm: duplicate network name %q", n.Name)
		}
		seen[n.Name] = true
		if n.ChainID == 0 {
			return fmt.Errorf("evm: network %q: chain id must not be 0", n.Name)
		}
		if len(n.Endpoints) == 0 {
			return fmt.Errorf("evm: network %q: at least one endpoint is required", n.Name)
		}
		for _, ep := range n.Endpoints {
			u, err := url.Parse(ep)
			if err != nil {
				return fmt.Errorf("evm: network %q: bad endpoint %q: %v", n.Name, config.RedactURL(ep), err)
			}
			switch u.Scheme {
			case "http", "https", "ws", "wss":
			default:
				return fmt.Errorf("evm: network %q: endpoint scheme must be http(s)/ws(s), got %q", n.Name, u.Scheme)
			}
		}
		if n.RequestTimeout < 0 {
			return fmt.Errorf("evm: network %q: request timeout must not be negative", n.Name)
		}
	}
	return nil
}
