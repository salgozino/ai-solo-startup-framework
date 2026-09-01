package config_test

import (
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		file        string
		wantErr     bool
		errContains string
		check       func(*testing.T, *config.CompanyConfig)
	}{
		{
			name: "valid company file is accepted",
			file: "testdata/valid.yaml",
			check: func(t *testing.T, c *config.CompanyConfig) {
				if c.Tenant != "acme" {
					t.Errorf("Tenant = %q; want %q", c.Tenant, "acme")
				}
				if len(c.Agents) != 2 {
					t.Fatalf("len(Agents) = %d; want 2", len(c.Agents))
				}
				if c.Agents[0].Name != "ceo" {
					t.Errorf("Agents[0].Name = %q; want %q", c.Agents[0].Name, "ceo")
				}
				if c.Agents[0].Model != "anthropic/claude-sonnet-4-20250514" {
					t.Errorf("Agents[0].Model = %q; want %q", c.Agents[0].Model, "anthropic/claude-sonnet-4-20250514")
				}
				if c.Agents[1].Provider != "opencode" {
					t.Errorf("Agents[1].Provider = %q; want %q", c.Agents[1].Provider, "opencode")
				}
				if c.Agents[1].Model != "openai/gpt-4o" {
					t.Errorf("Agents[1].Model = %q; want %q", c.Agents[1].Model, "openai/gpt-4o")
				}
				if c.Gateways.Telegram == nil {
					t.Fatal("Gateways.Telegram = nil; want non-nil")
				}
				if c.Gateways.Telegram.TokenEnv != "TELEGRAM_BOT_TOKEN" {
					t.Errorf("TokenEnv = %q; want TELEGRAM_BOT_TOKEN", c.Gateways.Telegram.TokenEnv)
				}
				if p, ok := c.RiskPolicy["telegram_send"]; !ok {
					t.Error("RiskPolicy missing telegram_send entry")
				} else if p.Risk != "risky" {
					t.Errorf("RiskPolicy[telegram_send].Risk = %q; want %q", p.Risk, "risky")
				}
			},
		},
		{
			name:        "inline token is rejected at load time",
			file:        "testdata/inline_token.yaml",
			wantErr:     true,
			errContains: "token_env",
		},
		{
			name:        "missing token_env is rejected",
			file:        "testdata/missing_token_env.yaml",
			wantErr:     true,
			errContains: "token_env",
		},
		{
			name:        "inline recipient_env is rejected",
			file:        "testdata/inline_recipient.yaml",
			wantErr:     true,
			errContains: "recipient_env",
		},
		{
			name:        "agent-level gateway field is rejected",
			file:        "testdata/agent_gateway_field.yaml",
			wantErr:     true,
			errContains: "gateways",
		},
		{
			name:        "malformed YAML is rejected",
			file:        "testdata/malformed.yaml",
			wantErr:     true,
			errContains: "decode",
		},
		{
			name:        "unknown top-level field is rejected",
			file:        "testdata/unknown_top_field.yaml",
			wantErr:     true,
			errContains: "decode",
		},
		{
			name:        "missing file returns error",
			file:        "testdata/does_not_exist.yaml",
			wantErr:     true,
			errContains: "read",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := config.Load(tc.file)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Load(%q) = %+v, nil; want error", tc.file, got)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("Load(%q) error = %q; want it to contain %q", tc.file, err.Error(), tc.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load(%q) unexpected error: %v", tc.file, err)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}
