package solana

import (
	"strings"
	"testing"
	"time"
)

func goodNetwork() NetworkConfig {
	return NetworkConfig{
		Name:                "mainnet",
		ExpectedGenesisHash: testGenesis(0x10),
		Endpoints:           []string{"https://rpc.example.com"},
	}
}

func TestConfigValidateOK(t *testing.T) {
	cfg := Config{Networks: []NetworkConfig{
		goodNetwork(),
		{Name: "devnet", ExpectedGenesisHash: testGenesis(0x20),
			Endpoints: []string{"http://localhost:8899"}, Commitment: CommitmentConfirmed},
	}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestConfigValidateDefaults(t *testing.T) {
	n := goodNetwork()
	if got := n.timeout(); got != DefaultRequestTimeout {
		t.Fatalf("want default timeout %v, got %v", DefaultRequestTimeout, got)
	}
	if got := n.commitment(); got != DefaultCommitment {
		t.Fatalf("want default commitment %v, got %v", DefaultCommitment, got)
	}
	n.Commitment = CommitmentConfirmed
	if got := n.commitment(); got != CommitmentConfirmed {
		t.Fatalf("configured commitment must win, got %v", got)
	}
	n.Commitment = "bogus"
	if got := n.commitment(); got != DefaultCommitment {
		t.Fatalf("an invalid commitment must fall back to the default, got %v", got)
	}
}

func TestConfigValidateRejects(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"no networks", func(c *Config) { c.Networks = nil }, "at least one network"},
		{"empty name", func(c *Config) { c.Networks[0].Name = "" }, "invalid logical name"},
		{"uppercase name", func(c *Config) { c.Networks[0].Name = "Mainnet" }, "invalid logical name"},
		{"duplicate name", func(c *Config) {
			c.Networks = append(c.Networks, goodNetwork())
		}, "duplicate network name"},
		{"no genesis hash", func(c *Config) { c.Networks[0].ExpectedGenesisHash = "" }, "genesis hash is required"},
		{"bad genesis hash", func(c *Config) { c.Networks[0].ExpectedGenesisHash = "not-base58!!" }, "genesis hash"},
		{"short genesis hash", func(c *Config) { c.Networks[0].ExpectedGenesisHash = "1111111111111111111111111" }, "genesis hash"},
		{"no endpoints", func(c *Config) { c.Networks[0].Endpoints = nil }, "at least one endpoint"},
		{"bad scheme", func(c *Config) { c.Networks[0].Endpoints = []string{"ftp://rpc.example.com"} }, "scheme"},
		{"negative timeout", func(c *Config) { c.Networks[0].RequestTimeout = -time.Second }, "timeout"},
		{"bad commitment", func(c *Config) { c.Networks[0].Commitment = "final" }, "commitment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Networks: []NetworkConfig{goodNetwork()}}
			tc.mut(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("want rejection for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestConfigNetworkIDCarriesGenesisHash(t *testing.T) {
	n := goodNetwork()
	id := n.networkID()
	if err := id.Validate(); err != nil {
		t.Fatalf("network id must validate: %v", err)
	}
	if id.ChainID != n.ExpectedGenesisHash {
		t.Fatalf("chain id must carry the cluster identity, got %q", id.ChainID)
	}
	if id.Family.String() != "solana" {
		t.Fatalf("family must be solana, got %q", id.Family)
	}
}
