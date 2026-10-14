package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultAndValidate(t *testing.T) {
	res, err := NewLoader().Load()
	if err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	c := res.Config
	if c.App.Name != "chame" || c.App.Environment != EnvDevelopment {
		t.Errorf("app defaults = %+v", c.App)
	}
	if c.App.ShutdownTimeout != 20*time.Second {
		t.Errorf("shutdown timeout = %s", c.App.ShutdownTimeout)
	}
	if c.Logging.Level != "info" || c.Logging.Format != "json" {
		t.Errorf("logging defaults = %+v", c.Logging)
	}
	if len(res.Sources) != 0 {
		t.Errorf("pure defaults should set no sources, got %v", res.Sources)
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const validChainYAML = `
app:
  name: example-game
  environment: staging
logging:
  level: debug
  format: text
chains:
  evm:
    - name: ethereum-mainnet
      chain_id: 1
      rpc_urls: ["${TEST_EVM_RPC}"]
      timeout: 10s
      read_only: true
`

func TestFileLayer(t *testing.T) {
	path := writeTemp(t, validChainYAML)
	t.Setenv("TEST_EVM_RPC", "https://eth.example.internal/v1")
	res, err := NewLoader().File(path).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c := res.Config
	if c.App.Name != "example-game" || c.App.Environment != EnvStaging {
		t.Errorf("app = %+v", c.App)
	}
	if c.Logging.Level != "debug" || c.Logging.Format != "text" {
		t.Errorf("logging = %+v", c.Logging)
	}
	if len(c.Chains.EVM) != 1 {
		t.Fatalf("chains = %+v", c.Chains)
	}
	n := c.Chains.EVM[0]
	if n.Name != "ethereum-mainnet" || n.ChainID != "1" || n.Timeout != 10*time.Second || !n.ReadOnly {
		t.Errorf("evm network = %+v", n)
	}
	// ${VAR} in rpc_urls was expanded before parsing.
	if len(n.RPCURLs) != 1 || n.RPCURLs[0] != "https://eth.example.internal/v1" {
		t.Errorf("rpc urls = %v", n.RPCURLs)
	}
	// Fields the file did not set keep the defaults.
	if c.App.ShutdownTimeout != 20*time.Second {
		t.Errorf("shutdown timeout = %s, want default", c.App.ShutdownTimeout)
	}
	// Source tracking.
	for path, want := range map[string]Source{
		"app.name":        SourceFile,
		"app.environment": SourceFile,
		"logging.level":   SourceFile,
		"chains.evm":      SourceFile,
	} {
		if got := res.Sources[path]; got != want {
			t.Errorf("source[%s] = %s, want %s", path, got, want)
		}
	}
	if _, tracked := res.Sources["app.shutdown_timeout"]; tracked {
		t.Error("unset field should not be tracked")
	}
}

func TestPrecedenceOrder(t *testing.T) {
	path := writeTemp(t, validChainYAML)
	env := func(key string) string {
		switch key {
		case "TEST_EVM_RPC":
			return "https://eth.example.internal"
		case "CHAME_APP_NAME":
			return "from-env"
		case "CHAME_LOGGING_LEVEL":
			return "warn"
		default:
			return ""
		}
	}
	res, err := NewLoader().
		File(path).
		Env().
		WithGetenv(env).
		Override(func(c *Config) { c.App.Name = "from-override" }).
		Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	c := res.Config
	// override > env > file > default.
	if c.App.Name != "from-override" {
		t.Errorf("app.name = %q, want override to win", c.App.Name)
	}
	if c.Logging.Level != "warn" {
		t.Errorf("logging.level = %q, want env to beat file", c.Logging.Level)
	}
	if res.Sources["app.name"] != SourceOverride || res.Sources["logging.level"] != SourceEnv {
		t.Errorf("sources = %v", res.Sources)
	}
}

func TestEnvParsing(t *testing.T) {
	env := func(key string) string {
		switch key {
		case "CHAME_APP_SHUTDOWN_TIMEOUT":
			return "35s"
		case "CHAME_TRANSACTIONS_ENABLED":
			return "1"
		default:
			return ""
		}
	}
	_, err := NewLoader().Env().WithGetenv(env).Load()
	if err == nil || !strings.Contains(err.Error(), "transactions") {
		t.Fatalf("load = %v, want transactions gate rejection", err)
	}
	env2 := func(key string) string {
		if key == "CHAME_APP_SHUTDOWN_TIMEOUT" {
			return "35s"
		}
		return ""
	}
	res, err := NewLoader().Env().WithGetenv(env2).Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if res.Config.App.ShutdownTimeout != 35*time.Second {
		t.Errorf("timeout = %s", res.Config.App.ShutdownTimeout)
	}
	if res.Sources["app.shutdown_timeout"] != SourceEnv {
		t.Errorf("source = %v", res.Sources["app.shutdown_timeout"])
	}
	// Invalid duration is ignored, not fatal: the layer is a best-effort
	// override and bad shapes surface through validation of defaults.
	env3 := func(key string) string {
		if key == "CHAME_APP_SHUTDOWN_TIMEOUT" {
			return "not-a-duration"
		}
		return ""
	}
	if _, err := NewLoader().Env().WithGetenv(env3).Load(); err != nil {
		t.Fatalf("invalid duration should fall back silently, got %v", err)
	}
}

func TestValidationRules(t *testing.T) {
	base := func() Config {
		c := Default()
		c.Chains.EVM = []EVMNetwork{{
			Name: "mainnet", ChainID: "1",
			RPCURLs: []string{"https://rpc.example"},
			Timeout: 10 * time.Second, ReadOnly: true,
		}}
		return c
	}
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"empty app name", func(c *Config) { c.App.Name = "" }, "app.name"},
		{"bad environment", func(c *Config) { c.App.Environment = "prod" }, "app.environment"},
		{"zero timeout", func(c *Config) { c.App.ShutdownTimeout = 0 }, "shutdown_timeout"},
		{"bad log level", func(c *Config) { c.Logging.Level = "trace" }, "logging"},
		{"empty evm name", func(c *Config) { c.Chains.EVM[0].Name = "" }, "name"},
		{"non-decimal chain id", func(c *Config) { c.Chains.EVM[0].ChainID = "one" }, "chain_id"},
		{"no rpc urls", func(c *Config) { c.Chains.EVM[0].RPCURLs = nil }, "rpc_urls"},
		{"bad rpc scheme", func(c *Config) { c.Chains.EVM[0].RPCURLs = []string{"ftp://x"} }, "rpc_urls[0]"},
		{"not a url", func(c *Config) { c.Chains.EVM[0].RPCURLs = []string{"nope"} }, "rpc_urls[0]"},
		{"tight timeout", func(c *Config) { c.Chains.EVM[0].Timeout = 10 * time.Millisecond }, "too tight"},
		{"negative timeout", func(c *Config) { c.Chains.EVM[0].Timeout = -time.Second }, "negative"},
		{"write-enabled evm", func(c *Config) { c.Chains.EVM[0].ReadOnly = false }, "read_only"},
		{"duplicate names across families", func(c *Config) {
			c.Chains.Solana = []SolanaNetwork{{
				Name: "mainnet", Cluster: "mainnet-beta",
				RPCURLs: []string{"https://rpc.example"}, ReadOnly: true,
			}}
		}, "already used"},
		{"duplicate names in family", func(c *Config) {
			c.Chains.EVM = append(c.Chains.EVM, c.Chains.EVM[0])
		}, "already used"},
		{"solana cluster empty", func(c *Config) {
			c.Chains.Solana = []SolanaNetwork{{
				Name: "sol", RPCURLs: []string{"https://rpc.example"}, ReadOnly: true,
			}}
		}, "cluster"},
		{"bad commitment", func(c *Config) {
			c.Chains.Solana = []SolanaNetwork{{
				Name: "sol", Cluster: "mainnet-beta", Commitment: "instant",
				RPCURLs: []string{"https://rpc.example"}, ReadOnly: true,
			}}
		}, "commitment"},
		{"write-enabled solana", func(c *Config) {
			c.Chains.Solana = []SolanaNetwork{{
				Name: "sol", Cluster: "mainnet-beta",
				RPCURLs: []string{"https://rpc.example"},
			}}
		}, "read_only"},
		{"transactions enabled", func(c *Config) { c.Transactions.Enabled = true }, "transactions"},
		{"events enabled", func(c *Config) { c.Events.Enabled = true }, "events"},
	}
	for _, tc := range cases {
		c := base()
		tc.mut(&c)
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want error containing %q", tc.name, err, tc.want)
		}
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestRedactURL(t *testing.T) {
	cases := map[string]string{
		"https://eth.example/v1/key/abc123": "https://eth.example",
		"http://localhost:8545":             "http://localhost:8545",
		"wss://node.example?apikey=SECRET":  "wss://node.example",
		"garbage":                           "<redacted>",
		"weird://host/path":                 "weird://host",
	}
	for in, want := range cases {
		if got := RedactURL(in); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidationErrorsRedactURLs(t *testing.T) {
	c := Default()
	c.Chains.EVM = []EVMNetwork{{
		Name: "mainnet", ChainID: "1",
		// Bad scheme, and a secret in the path.
		RPCURLs:  []string{"ftp://rpc.example/secretkey"},
		ReadOnly: true,
	}}
	err := c.Validate()
	if err == nil {
		t.Fatal("bad scheme should fail validation")
	}
	if !strings.Contains(err.Error(), "ftp://rpc.example") {
		t.Fatalf("err = %v, want the redacted host", err)
	}
	if strings.Contains(err.Error(), "secretkey") {
		t.Errorf("error leaked the secret path: %v", err)
	}
}

func TestCloneIsDeep(t *testing.T) {
	c := Default()
	c.Chains.EVM = []EVMNetwork{{Name: "a", RPCURLs: []string{"u1"}}}
	clone := c.Clone()
	clone.Chains.EVM[0].RPCURLs[0] = "changed"
	if c.Chains.EVM[0].RPCURLs[0] != "u1" {
		t.Error("clone shares slice memory")
	}
}

func TestMissingFileFails(t *testing.T) {
	if _, err := NewLoader().File("/nonexistent/config.yaml").Load(); err == nil {
		t.Error("missing file should fail")
	}
}

func TestBrokenYAMLFails(t *testing.T) {
	path := writeTemp(t, "app: [unclosed")
	if _, err := NewLoader().File(path).Load(); err == nil {
		t.Error("broken yaml should fail")
	}
}
