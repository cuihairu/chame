package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Source says where a setting came from (spec §5.1: 配置来源追踪).
type Source uint8

const (
	// SourceDefault marks built-in defaults (layer 1).
	SourceDefault Source = iota
	// SourceFile marks values from a config file (layer 2).
	SourceFile
	// SourceEnv marks values from environment variables (layer 3).
	SourceEnv
	// SourceOverride marks explicit code overrides (layer 4).
	SourceOverride
)

// String names the source.
func (s Source) String() string {
	switch s {
	case SourceDefault:
		return "default"
	case SourceFile:
		return "file"
	case SourceEnv:
		return "env"
	case SourceOverride:
		return "override"
	default:
		return "unknown"
	}
}

// Result carries the merged config and the source of every explicitly
// set field, keyed by dotted path ("app.name").
type Result struct {
	Config  Config
	Sources map[string]Source
}

// Loader applies configuration layers in the spec §5.2 order.
type Loader struct {
	defaults  Config
	haveDef   bool
	filePath  string
	useEnv    bool
	overrides []func(*Config)
	getenv    func(string) string
}

// NewLoader returns a loader with no layers applied yet.
func NewLoader() *Loader {
	return &Loader{getenv: os.Getenv}
}

// Defaults sets the code-level defaults (layer 1). Without it,
// config.Default() is used.
func (l *Loader) Defaults(c Config) *Loader {
	l.defaults = c
	l.haveDef = true
	return l
}

// File adds a YAML config file (layer 2).
func (l *Loader) File(path string) *Loader {
	l.filePath = path
	return l
}

// Env enables environment-variable overrides (layer 3): scalar fields
// via CHAME_* names, e.g. CHAME_APP_NAME, CHAME_LOGGING_LEVEL,
// CHAME_LOGGING_FORMAT, CHAME_APP_SHUTDOWN_TIMEOUT (Go duration form),
// CHAME_TRANSACTIONS_ENABLED, CHAME_EVENTS_ENABLED (1/true).
func (l *Loader) Env() *Loader {
	l.useEnv = true
	return l
}

// WithGetenv replaces the environment lookup, for tests.
func (l *Loader) WithGetenv(f func(string) string) *Loader {
	l.getenv = f
	return l
}

// Override adds a code-level mutation (layer 4), applied last.
func (l *Loader) Override(mutate func(*Config)) *Loader {
	if mutate != nil {
		l.overrides = append(l.overrides, mutate)
	}
	return l
}

// Load merges all layers and validates the result.
func (l *Loader) Load() (Result, error) {
	sources := make(map[string]Source)

	cfg := Default()
	if l.haveDef {
		cfg = l.defaults.Clone()
	}

	if l.filePath != "" {
		raw, err := os.ReadFile(l.filePath)
		if err != nil {
			return Result{}, fmt.Errorf("config: read file: %w", err)
		}
		raw = expandEnvRefs(raw, l.getenv)
		var fileCfg Config
		if err := yaml.Unmarshal(raw, &fileCfg); err != nil {
			return Result{}, fmt.Errorf("config: parse %s: %w", l.filePath, err)
		}
		applyFile(&cfg, &fileCfg, sources, l.filePath)
	}

	if l.useEnv {
		applyEnv(&cfg, l.getenv, sources)
	}

	for i, mutate := range l.overrides {
		before := cfg.Clone()
		mutate(&cfg)
		recordOverrides(&before, &cfg, sources)
		_ = i
	}

	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	return Result{Config: cfg, Sources: sources}, nil
}

// applyFile merges fields that the file explicitly set, remembering the
// source of each.
func applyFile(dst *Config, src *Config, sources map[string]Source, path string) {
	if src.App.Name != "" {
		dst.App.Name = src.App.Name
		sources["app.name"] = SourceFile
	}
	if src.App.Environment != "" {
		dst.App.Environment = src.App.Environment
		sources["app.environment"] = SourceFile
	}
	if src.App.ShutdownTimeout != 0 {
		dst.App.ShutdownTimeout = src.App.ShutdownTimeout
		sources["app.shutdown_timeout"] = SourceFile
	}
	if src.Logging.Level != "" {
		dst.Logging.Level = src.Logging.Level
		sources["logging.level"] = SourceFile
	}
	if src.Logging.Format != "" {
		dst.Logging.Format = src.Logging.Format
		sources["logging.format"] = SourceFile
	}
	if len(src.Chains.EVM) > 0 {
		dst.Chains.EVM = src.Chains.EVM
		sources["chains.evm"] = SourceFile
	}
	if len(src.Chains.Solana) > 0 {
		dst.Chains.Solana = src.Chains.Solana
		sources["chains.solana"] = SourceFile
	}
	if src.Transactions.Enabled {
		dst.Transactions.Enabled = true
		sources["transactions.enabled"] = SourceFile
	}
	if src.Events.Enabled {
		dst.Events.Enabled = true
		sources["events.enabled"] = SourceFile
	}
	_ = path
}

// envEntry maps an environment variable to a config field.
type envEntry struct {
	key   string
	apply func(*Config, string) bool // returns whether the value parsed
	path  string
}

var envEntries = []envEntry{
	{"CHAME_APP_NAME", func(c *Config, v string) bool { c.App.Name = v; return true }, "app.name"},
	{"CHAME_APP_ENVIRONMENT", func(c *Config, v string) bool { c.App.Environment = v; return true }, "app.environment"},
	{"CHAME_APP_SHUTDOWN_TIMEOUT", func(c *Config, v string) bool {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return false
		}
		c.App.ShutdownTimeout = d
		return true
	}, "app.shutdown_timeout"},
	{"CHAME_LOGGING_LEVEL", func(c *Config, v string) bool { c.Logging.Level = v; return true }, "logging.level"},
	{"CHAME_LOGGING_FORMAT", func(c *Config, v string) bool { c.Logging.Format = v; return true }, "logging.format"},
	{"CHAME_TRANSACTIONS_ENABLED", func(c *Config, v string) bool { b, ok := parseBool(v); c.Transactions.Enabled = b; return ok }, "transactions.enabled"},
	{"CHAME_EVENTS_ENABLED", func(c *Config, v string) bool { b, ok := parseBool(v); c.Events.Enabled = b; return ok }, "events.enabled"},
}

// applyEnv overlays the supported environment variables.
func applyEnv(cfg *Config, getenv func(string) string, sources map[string]Source) {
	for _, e := range envEntries {
		if v := getenv(e.key); v != "" {
			if e.apply(cfg, v) {
				sources[e.path] = SourceEnv
			}
		}
	}
}

func parseBool(v string) (bool, bool) {
	switch strings.ToLower(v) {
	case "1", "true", "yes":
		return true, true
	case "0", "false", "no":
		return false, true
	default:
		return false, false
	}
}

// recordOverrides marks every field an override changed.
func recordOverrides(before *Config, after *Config, sources map[string]Source) {
	if before.App.Name != after.App.Name {
		sources["app.name"] = SourceOverride
	}
	if before.App.Environment != after.App.Environment {
		sources["app.environment"] = SourceOverride
	}
	if before.App.ShutdownTimeout != after.App.ShutdownTimeout {
		sources["app.shutdown_timeout"] = SourceOverride
	}
	if before.Logging.Level != after.Logging.Level {
		sources["logging.level"] = SourceOverride
	}
	if before.Logging.Format != after.Logging.Format {
		sources["logging.format"] = SourceOverride
	}
	if len(before.Chains.EVM) != len(after.Chains.EVM) {
		sources["chains.evm"] = SourceOverride
	}
	if len(before.Chains.Solana) != len(after.Chains.Solana) {
		sources["chains.solana"] = SourceOverride
	}
	if before.Transactions.Enabled != after.Transactions.Enabled {
		sources["transactions.enabled"] = SourceOverride
	}
	if before.Events.Enabled != after.Events.Enabled {
		sources["events.enabled"] = SourceOverride
	}
}

// expandEnvRefs replaces ${VAR} references in file bytes with the
// environment value (spec §5.3 uses ${EVM_RPC_URL} in rpc_urls). A
// missing variable expands to the empty string; the URL validation then
// rejects the unusable value with its normal message.
func expandEnvRefs(raw []byte, getenv func(string) string) []byte {
	s := string(raw)
	for {
		i := strings.Index(s, "${")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			break
		}
		name := s[i+2 : i+j]
		val := ""
		if name != "" {
			val = getenv(name)
		}
		s = s[:i] + val + s[i+j+1:]
	}
	return []byte(s)
}
