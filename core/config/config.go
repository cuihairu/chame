// Package config loads, merges, and validates application configuration
// (spec §5). Layering is explicit: code defaults, then file, then
// environment variables, then explicit overrides from the composition
// root. Validation runs before the runtime starts and rejects unsafe
// shapes outright.
package config

import "time"

// App holds process-level settings.
type App struct {
	// Name is the application name used in logs and health reports.
	Name string `yaml:"name"`
	// Environment is one of development, staging, production.
	Environment string `yaml:"environment"`
	// ShutdownTimeout bounds the runtime stop sequence.
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
}

// Logging mirrors the core/logging options.
type Logging struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// EVMNetwork is one logical EVM network entry (spec §5.3). ChainID is a
// string so the common layer never depends on one integer width
// (spec §6.2).
type EVMNetwork struct {
	Name    string        `yaml:"name"`
	ChainID string        `yaml:"chain_id"`
	RPCURLs []string      `yaml:"rpc_urls"`
	Timeout time.Duration `yaml:"timeout"`
	// ReadOnly gates write-capable use of this network. The first
	// release supports read-only EVM access only (spec §19 Phase 3).
	ReadOnly bool `yaml:"read_only"`
}

// SolanaNetwork is one logical Solana cluster entry (spec §5.3).
type SolanaNetwork struct {
	Name       string        `yaml:"name"`
	Cluster    string        `yaml:"cluster"`
	RPCURLs    []string      `yaml:"rpc_urls"`
	Commitment string        `yaml:"commitment"`
	Timeout    time.Duration `yaml:"timeout"`
	// ReadOnly gates write-capable use. The first release supports
	// read-only Solana access only (spec §19 Phase 4, ADR-0003).
	ReadOnly bool `yaml:"read_only"`
}

// Chains groups the configured networks of both families. Logical names
// are unique across families, because game code references networks by
// logical name.
type Chains struct {
	EVM    []EVMNetwork    `yaml:"evm"`
	Solana []SolanaNetwork `yaml:"solana"`
}

// Transactions holds the transaction feature gate. Transaction support
// ships in a later phase; enabling it is rejected at validation until
// then (spec §5.4: 不安全的生产环境默认配置).
type Transactions struct {
	Enabled bool `yaml:"enabled"`
}

// Events holds the event feature gate (§11 pipeline shipped).
type Events struct {
	Enabled bool `yaml:"enabled"`
}

// Config is the whole application configuration. Tests construct it
// directly as a plain struct; loading from files is an extra, not the
// primary path (spec §5.1).
type Config struct {
	App          App          `yaml:"app"`
	Logging      Logging      `yaml:"logging"`
	Chains       Chains       `yaml:"chains"`
	Transactions Transactions `yaml:"transactions"`
	Events       Events       `yaml:"events"`
}

// Default returns the built-in defaults (spec §5.2 layer 1).
func Default() Config {
	return Config{
		App: App{
			Name:            "chame",
			Environment:     "development",
			ShutdownTimeout: 20 * time.Second,
		},
		Logging: Logging{Level: "info", Format: "json"},
	}
}

// Clone returns a deep copy so layers never share mutable state.
func (c Config) Clone() Config {
	out := c
	out.Chains.EVM = append([]EVMNetwork(nil), c.Chains.EVM...)
	out.Chains.Solana = append([]SolanaNetwork(nil), c.Chains.Solana...)
	for i := range out.Chains.EVM {
		urls := append([]string(nil), out.Chains.EVM[i].RPCURLs...)
		out.Chains.EVM[i].RPCURLs = urls
	}
	for i := range out.Chains.Solana {
		urls := append([]string(nil), out.Chains.Solana[i].RPCURLs...)
		out.Chains.Solana[i].RPCURLs = urls
	}
	return out
}
