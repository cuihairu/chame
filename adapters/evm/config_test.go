package evm

import (
	"testing"
	"time"
)

func validConfig() Config {
	return Config{Networks: []NetworkConfig{
		{Name: "mainnet", ChainID: 1, Endpoints: []string{"https://a.example", "https://b.example"}},
		{Name: "base", ChainID: 8453, Endpoints: []string{"https://base.example"}},
	}}
}

func TestConfigValidateAccepts(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestConfigValidateRejects(t *testing.T) {
	cases := map[string]func(*Config){
		"no networks": func(c *Config) { c.Networks = nil },
		"duplicate name": func(c *Config) {
			c.Networks = append(c.Networks, NetworkConfig{Name: "mainnet", ChainID: 5, Endpoints: []string{"https://x"}})
		},
		"chain id zero":    func(c *Config) { c.Networks[0].ChainID = 0 },
		"no endpoints":     func(c *Config) { c.Networks[0].Endpoints = nil },
		"bad scheme":       func(c *Config) { c.Networks[0].Endpoints = []string{"ftp://x"} },
		"negative timeout": func(c *Config) { c.Networks[0].RequestTimeout = -time.Second },
		"bad network name": func(c *Config) { c.Networks[0].Name = "Mainnet!" },
	}
	for name, mutate := range cases {
		c := validConfig()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := NetworkConfig{Name: "n", ChainID: 1, Endpoints: []string{"https://x"}}
	if got := cfg.timeout(); got != DefaultRequestTimeout {
		t.Errorf("timeout = %v, want default %v", got, DefaultRequestTimeout)
	}
	if got := cfg.Finality.withDefaults().Confirmations; got != 12 {
		t.Errorf("confirmations = %d, want 12", got)
	}
	cfg.RequestTimeout = time.Second
	if got := cfg.timeout(); got != time.Second {
		t.Errorf("timeout = %v, want 1s", got)
	}
	cfg.Finality.Confirmations = 32
	if got := cfg.Finality.withDefaults().Confirmations; got != 32 {
		t.Errorf("confirmations = %d, want 32", got)
	}
}

func TestNetworkIDDerivation(t *testing.T) {
	cfg := NetworkConfig{Name: "base", ChainID: 8453, Endpoints: []string{"https://x"}}
	id := cfg.networkID()
	if id.Family != "evm" || id.Name != "base" || id.ChainID != "8453" {
		t.Errorf("network id = %+v", id)
	}
	if err := id.Validate(); err != nil {
		t.Errorf("derived id must validate: %v", err)
	}
}
