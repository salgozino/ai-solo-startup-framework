// Package config loads and validates company.yaml.
// Enforces: env-var references only (never inline secrets), gateways at the
// company level only (no per-agent gateway fields), unknown agent fields rejected.
package config

import (
	"bytes"
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// envVarName matches valid environment variable names.
// A value that does not match cannot be an env-var reference — it is an inline secret.
var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CompanyConfig is the top-level structure decoded from company.yaml.
type CompanyConfig struct {
	Tenant     string            `yaml:"tenant"`
	Agents     []AgentConfig     `yaml:"agents"`
	Gateways   GatewayConfig     `yaml:"gateways"`
	RiskPolicy map[string]Policy `yaml:"risk_policy"`
}

// AgentConfig declares a single agent: name, role, and provider.
// There is deliberately no Gateways field here; gateway authorization is
// governed solely by RiskPolicy.allowed_roles at the company level.
type AgentConfig struct {
	Name     string `yaml:"name"`
	Role     string `yaml:"role"`
	Provider string `yaml:"provider"`
}

// GatewayConfig holds the company-level gateway declarations.
type GatewayConfig struct {
	Telegram *TelegramGatewayConfig `yaml:"telegram,omitempty"`
}

// TelegramGatewayConfig holds the Telegram outbound gateway configuration.
// Both fields MUST be env-var references, never literal token values.
type TelegramGatewayConfig struct {
	TokenEnv     string `yaml:"token_env"`
	RecipientEnv string `yaml:"recipient_env,omitempty"`
}

// Policy is a single risk-policy entry for an action kind.
type Policy struct {
	Risk         string   `yaml:"risk"`
	AllowedRoles []string `yaml:"allowed_roles"`
}

// Load reads and validates company.yaml at path.
// Returns an error if:
//   - the file cannot be read or is malformed YAML
//   - tenant is empty
//   - any agent entry contains unknown fields (including a gateways field)
//   - gateways.telegram.token_env or recipient_env is not an env-var reference
func Load(path string) (*CompanyConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}

	// rawCompany mirrors CompanyConfig but agents use a generic map
	// so we can detect unknown/forbidden fields before accepting them.
	var raw struct {
		Tenant     string             `yaml:"tenant"`
		Agents     []map[string]any   `yaml:"agents"`
		Gateways   GatewayConfig      `yaml:"gateways"`
		RiskPolicy map[string]Policy  `yaml:"risk_policy"`
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown top-level fields
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("config: decode %q: %w", path, err)
	}

	if raw.Tenant == "" {
		return nil, fmt.Errorf("config: %q: tenant must not be empty", path)
	}

	// Decode agents with strict field enforcement.
	agents := make([]AgentConfig, 0, len(raw.Agents))
	for i, a := range raw.Agents {
		name, _ := a["name"].(string)

		// Reject the forbidden agent-level gateways field explicitly for a clear error.
		if _, ok := a["gateways"]; ok {
			return nil, fmt.Errorf("config: agent[%d] (%q): gateways field is not allowed on agent entries; declare gateways at the company level under risk_policy", i, name)
		}

		// Reject any other unknown field.
		for k := range a {
			switch k {
			case "name", "role", "provider":
				// valid fields
			default:
				return nil, fmt.Errorf("config: agent[%d] (%q): unknown field %q", i, name, k)
			}
		}

		role, _ := a["role"].(string)
		provider, _ := a["provider"].(string)
		agents = append(agents, AgentConfig{Name: name, Role: role, Provider: provider})
	}

	// Inline-secret guard for Telegram gateway.
	if tg := raw.Gateways.Telegram; tg != nil {
		if err := requireEnvRef("gateways.telegram.token_env", tg.TokenEnv); err != nil {
			return nil, err
		}
		if tg.RecipientEnv != "" {
			if err := requireEnvRef("gateways.telegram.recipient_env", tg.RecipientEnv); err != nil {
				return nil, err
			}
		}
	}

	return &CompanyConfig{
		Tenant:     raw.Tenant,
		Agents:     agents,
		Gateways:   raw.Gateways,
		RiskPolicy: raw.RiskPolicy,
	}, nil
}

// requireEnvRef returns an error if value is empty or does not look like an
// environment variable name. A Telegram bot token (e.g. "1234567890:AABBccdd...")
// never matches this pattern, so inline secrets are rejected at load time.
func requireEnvRef(field, value string) error {
	if value == "" {
		return fmt.Errorf("config: %s must not be empty", field)
	}
	if !envVarName.MatchString(value) {
		return fmt.Errorf("config: %s appears to be an inline secret value rather than an environment variable name; use the env var name (e.g. TELEGRAM_BOT_TOKEN), not the secret value itself", field)
	}
	return nil
}
