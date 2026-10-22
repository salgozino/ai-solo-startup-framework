// Package config loads and validates company.yaml.
// Enforces: env-var references only (never inline secrets), gateways at the
// company level only (no per-agent gateway fields), unknown agent fields rejected.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// envVarName matches valid environment variable names.
// A value that does not match cannot be an env-var reference — it is an inline secret.
var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CompanyConfig is the top-level structure decoded from company.yaml.
type CompanyConfig struct {
	Tenant       string            `yaml:"tenant"`
	AuthTokenEnv string            `yaml:"auth_token_env"`
	Agents       []AgentConfig     `yaml:"agents"`
	Gateways     GatewayConfig     `yaml:"gateways"`
	RiskPolicy   map[string]Policy `yaml:"risk_policy"`
}

// AgentConfig declares a single agent: name, role, provider, optional model,
// and optional system_prompt. There is deliberately no Gateways field here;
// gateway authorization is governed solely by RiskPolicy.allowed_roles at the
// company level.
//
// SystemPrompt holds the resolved absolute path to the agent's persona file
// after Load() validates and resolves it. Empty string means no system prompt.
//
// Provider defaults to DefaultProvider when omitted. Extensions is the pi-only
// allowlist of extensions loaded on top of the adapter's isolated invocation:
// each entry is an absolute path after Load() or a "builtin:<name>" reference.
type AgentConfig struct {
	Name         string   `yaml:"name"`
	Role         string   `yaml:"role"`
	Provider     string   `yaml:"provider"`
	Model        string   `yaml:"model,omitempty"`
	SystemPrompt string   `yaml:"system_prompt,omitempty"` // abs path after Load()
	Extensions   []string `yaml:"extensions,omitempty"`    // abs paths or builtin:<name> after Load()
}

// DefaultProvider is the provider an agent gets when its entry omits provider.
const DefaultProvider = "pi"

// builtinExtensionPrefix marks a pi built-in extension reference (e.g. builtin:mcp),
// passed to pi verbatim rather than resolved as a filesystem path.
const builtinExtensionPrefix = "builtin:"

// packageSourcePrefixes are pi package sources. pi's -e flag silently ignores them
// (verified on pi 1.2.0: `-e npm:pi-claude-bridge` loads nothing and reports nothing),
// so they are rejected at load time instead of producing an agent that quietly lacks
// the extension it declared.
var packageSourcePrefixes = []string{"npm:", "git:", "git+", "http://", "https://", "ssh://"}

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
//   - an agent's extensions is set for a non-pi provider, names a package source
//     (npm:, git:, ...), or names a path that does not exist
//
// An agent that omits provider gets DefaultProvider (pi).
func Load(path string) (*CompanyConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %q: %w", path, err)
	}

	// rawCompany mirrors CompanyConfig but agents use a generic map
	// so we can detect unknown/forbidden fields before accepting them.
	var raw struct {
		Tenant       string            `yaml:"tenant"`
		AuthTokenEnv string            `yaml:"auth_token_env"`
		Agents       []map[string]any  `yaml:"agents"`
		Gateways     GatewayConfig     `yaml:"gateways"`
		RiskPolicy   map[string]Policy `yaml:"risk_policy"`
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // reject unknown top-level fields
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("config: decode %q: %w", path, err)
	}

	if raw.Tenant == "" {
		return nil, fmt.Errorf("config: %q: tenant must not be empty", path)
	}

	if err := requireEnvRef("auth_token_env", raw.AuthTokenEnv); err != nil {
		return nil, fmt.Errorf("config: %q: %w", path, err)
	}

	// Derive basedir for relative system_prompt paths from the config file location.
	basedir := filepath.Dir(path)

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
			case "name", "role", "provider", "model", "system_prompt", "extensions":
				// valid fields
			default:
				return nil, fmt.Errorf("config: agent[%d] (%q): unknown field %q", i, name, k)
			}
		}

		role, _ := a["role"].(string)
		provider, _ := a["provider"].(string)
		if provider == "" {
			provider = DefaultProvider
		}
		model, _ := a["model"].(string)
		rawPrompt, _ := a["system_prompt"].(string)

		// Resolve system_prompt to an absolute path and validate file existence.
		var systemPrompt string
		if rawPrompt != "" {
			if !filepath.IsAbs(rawPrompt) {
				rawPrompt = filepath.Join(basedir, rawPrompt)
			}
			abs, err := filepath.Abs(rawPrompt)
			if err != nil {
				return nil, fmt.Errorf("config: agent[%d] (%q): system_prompt: resolve path: %w", i, name, err)
			}
			if _, err := os.Stat(abs); err != nil {
				return nil, fmt.Errorf("config: agent[%d] (%q): system_prompt: file not found: %s", i, name, abs)
			}
			systemPrompt = abs
		}

		extensions, err := resolveExtensions(a["extensions"], provider, basedir)
		if err != nil {
			return nil, fmt.Errorf("config: agent[%d] (%q): %w", i, name, err)
		}

		agents = append(agents, AgentConfig{
			Name:         name,
			Role:         role,
			Provider:     provider,
			Model:        model,
			SystemPrompt: systemPrompt,
			Extensions:   extensions,
		})
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
		Tenant:       raw.Tenant,
		AuthTokenEnv: raw.AuthTokenEnv,
		Agents:       agents,
		Gateways:     raw.Gateways,
		RiskPolicy:   raw.RiskPolicy,
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

// resolveExtensions validates an agent's raw extensions value and returns the entries
// pi receives as -e arguments: builtin references verbatim, filesystem paths resolved
// against basedir to absolute paths that must exist. A nil raw value yields nil.
func resolveExtensions(raw any, provider, basedir string) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if provider != DefaultProvider {
		return nil, fmt.Errorf("extensions: only supported for provider %q, got %q", DefaultProvider, provider)
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("extensions: must be a list of strings")
	}
	resolved := make([]string, 0, len(items))
	for j, item := range items {
		entry, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("extensions[%d]: must be a list of strings", j)
		}
		if entry == "" {
			return nil, fmt.Errorf("extensions[%d]: must not be empty", j)
		}
		if strings.HasPrefix(entry, builtinExtensionPrefix) {
			resolved = append(resolved, entry)
			continue
		}
		for _, prefix := range packageSourcePrefixes {
			if strings.HasPrefix(entry, prefix) {
				return nil, fmt.Errorf("extensions[%d]: %q: package sources are not supported; pi only loads a file or directory path (or builtin:<name>) through -e, so point to the installed extension's path instead", j, entry)
			}
		}
		path := entry
		if !filepath.IsAbs(path) {
			path = filepath.Join(basedir, path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("extensions[%d]: resolve path: %w", j, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, fmt.Errorf("extensions[%d]: file not found: %s", j, abs)
		}
		resolved = append(resolved, abs)
	}
	return resolved, nil
}
