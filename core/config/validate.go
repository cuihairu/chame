package config

import (
	"fmt"
	"net/url"
	"time"

	"github.com/cuihairu/chame/core/logging"
)

// Environments.
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// Validate applies the spec §5.4 rules that are in force for this phase.
// It rejects the config before the runtime starts; later phases extend
// it (signer requirements, finality policies).
func (c Config) Validate() error {
	if c.App.Name == "" {
		return fmt.Errorf("config: app.name must not be empty")
	}
	switch c.App.Environment {
	case EnvDevelopment, EnvStaging, EnvProduction:
	default:
		return fmt.Errorf("config: app.environment %q is not one of development, staging, production", c.App.Environment)
	}
	if c.App.ShutdownTimeout <= 0 {
		return fmt.Errorf("config: app.shutdown_timeout must be positive, got %s", c.App.ShutdownTimeout)
	}
	if err := (logging.Options{Level: c.Logging.Level, Format: c.Logging.Format}).Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	names := make(map[string]string) // logical name -> family
	for i, n := range c.Chains.EVM {
		if err := validateEVMNetwork(n); err != nil {
			return fmt.Errorf("config: chains.evm[%d]: %w", i, err)
		}
		if err := claimName(names, n.Name, "evm"); err != nil {
			return fmt.Errorf("config: chains.evm[%d]: %w", i, err)
		}
	}
	for i, n := range c.Chains.Solana {
		if err := validateSolanaNetwork(n); err != nil {
			return fmt.Errorf("config: chains.solana[%d]: %w", i, err)
		}
		if err := claimName(names, n.Name, "solana"); err != nil {
			return fmt.Errorf("config: chains.solana[%d]: %w", i, err)
		}
	}

	// Feature gates of later phases must stay off in this release
	// (spec §5.4: 交易功能启用但未配置所需签名能力 / 不安全的生产环境
	// 默认配置 — signer configuration itself arrives with the
	// transaction phase, so enabling either gate is unsafe today).
	if c.Transactions.Enabled {
		return fmt.Errorf("config: transactions.enabled: transaction support ships in a later phase, keep it disabled")
	}
	if c.Events.Enabled {
		return fmt.Errorf("config: events.enabled: event support ships in a later phase, keep it disabled")
	}
	return nil
}

// claimName enforces unique logical network names across families
// (spec §5.4: 重复的逻辑网络名称).
func claimName(names map[string]string, name, family string) error {
	if name == "" {
		return fmt.Errorf("logical network name must not be empty")
	}
	if prev, dup := names[name]; dup {
		return fmt.Errorf("logical network name %q already used by family %q", name, prev)
	}
	names[name] = family
	return nil
}

func validateEVMNetwork(n EVMNetwork) error {
	if err := validLogicalName(n.Name); err != nil {
		return err
	}
	if n.ChainID == "" {
		return fmt.Errorf("network %q: chain_id must not be empty", n.Name)
	}
	if !isDecimal(n.ChainID) {
		return fmt.Errorf("network %q: chain_id %q must be a decimal integer", n.Name, n.ChainID)
	}
	if err := validateRPCURLs(n.Name, n.RPCURLs); err != nil {
		return err
	}
	if n.Timeout < 0 {
		return fmt.Errorf("network %q: timeout must not be negative", n.Name)
	}
	if n.Timeout == 0 {
		// zero means "unset": the default of 10s applies at use time.
		return nil
	}
	if n.Timeout < time.Second {
		return fmt.Errorf("network %q: timeout %s is too tight, want >= 1s", n.Name, n.Timeout)
	}
	if !n.ReadOnly {
		return fmt.Errorf("network %q: read_only must be true; write-capable EVM access ships in a later phase", n.Name)
	}
	return nil
}

func validateSolanaNetwork(n SolanaNetwork) error {
	if err := validLogicalName(n.Name); err != nil {
		return err
	}
	if n.Cluster == "" {
		return fmt.Errorf("network %q: cluster must not be empty", n.Name)
	}
	switch n.Commitment {
	case "", "processed", "confirmed", "finalized":
	default:
		return fmt.Errorf("network %q: commitment %q is not one of processed, confirmed, finalized", n.Name, n.Commitment)
	}
	if err := validateRPCURLs(n.Name, n.RPCURLs); err != nil {
		return err
	}
	if n.Timeout < 0 {
		return fmt.Errorf("network %q: timeout must not be negative", n.Name)
	}
	if n.Timeout != 0 && n.Timeout < time.Second {
		return fmt.Errorf("network %q: timeout %s is too tight, want >= 1s", n.Name, n.Timeout)
	}
	if !n.ReadOnly {
		return fmt.Errorf("network %q: read_only must be true; write-capable Solana access ships in a later phase", n.Name)
	}
	return nil
}

// validLogicalName enforces the shared name pattern so logical names are
// unambiguous in config references (spec §6.2).
func validLogicalName(name string) error {
	if name == "" || len(name) > 63 {
		return fmt.Errorf("logical network name %q must be 1-63 characters", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		default:
			return fmt.Errorf("logical network name %q must use lowercase letters, digits, and dashes", name)
		}
	}
	return nil
}

// validateRPCURLs rejects empty lists and non-URL entries. Query
// strings carrying API keys never reach logs; use RedactURL when a URL
// must appear in an error (spec §5.4).
func validateRPCURLs(name string, urls []string) error {
	if len(urls) == 0 {
		return fmt.Errorf("network %q: rpc_urls must not be empty", name)
	}
	for i, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("network %q: rpc_urls[%d] %q is not a valid URL", name, i, RedactURL(raw))
		}
		switch u.Scheme {
		case "http", "https", "ws", "wss":
		default:
			return fmt.Errorf("network %q: rpc_urls[%d] %q must use http, https, ws, or wss", name, i, RedactURL(raw))
		}
	}
	return nil
}

func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
