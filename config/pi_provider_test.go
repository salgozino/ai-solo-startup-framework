package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
)

// writeCompany writes a minimal company.yaml whose single agent body is agentYAML
// (already indented under "  - ") into a fresh temp dir and returns its path.
func writeCompany(t *testing.T, agentYAML string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "company.yaml")
	body := "tenant: acme\nauth_token_env: A2A_AUTH_TOKEN\nagents:\n  - name: ceo\n    role: ceo\n" + agentYAML
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write company.yaml: %v", err)
	}
	return path
}

// TestLoad_PiProvider covers the pi default provider (S2) and the per-agent
// extensions allowlist (S4) from odd/tasks/pi-adapter.md.
func TestLoad_PiProvider(t *testing.T) {
	t.Run("omitted provider defaults to pi", func(t *testing.T) {
		c, err := config.Load(writeCompany(t, ""))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if c.Agents[0].Provider != "pi" {
			t.Errorf("Provider = %q; want %q", c.Agents[0].Provider, "pi")
		}
		if c.Agents[0].Extensions != nil {
			t.Errorf("Extensions = %v; want nil when the field is absent", c.Agents[0].Extensions)
		}
	})

	t.Run("explicit claude-code provider is kept", func(t *testing.T) {
		c, err := config.Load(writeCompany(t, "    provider: claude-code\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if c.Agents[0].Provider != "claude-code" {
			t.Errorf("Provider = %q; want %q", c.Agents[0].Provider, "claude-code")
		}
	})

	t.Run("relative extension path resolves against the config dir and builtin entries pass through", func(t *testing.T) {
		path := writeCompany(t, "    provider: pi\n    extensions:\n      - ext/bridge\n      - builtin:llama.cpp\n")
		extDir := filepath.Join(filepath.Dir(path), "ext", "bridge")
		if err := os.MkdirAll(extDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		c, err := config.Load(path)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := []string{extDir, "builtin:llama.cpp"}
		if !reflect.DeepEqual(c.Agents[0].Extensions, want) {
			t.Errorf("Extensions = %v; want %v", c.Agents[0].Extensions, want)
		}
	})

	t.Run("extensions on an agent with omitted provider are accepted", func(t *testing.T) {
		c, err := config.Load(writeCompany(t, "    extensions:\n      - builtin:mcp\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !reflect.DeepEqual(c.Agents[0].Extensions, []string{"builtin:mcp"}) {
			t.Errorf("Extensions = %v; want [builtin:mcp]", c.Agents[0].Extensions)
		}
	})

	errCases := []struct {
		name        string
		agentYAML   string
		errContains []string
	}{
		{
			name:        "missing extension path is rejected naming the path",
			agentYAML:   "    provider: pi\n    extensions:\n      - does/not/exist\n",
			errContains: []string{`agent[0] ("ceo")`, "extensions[0]", "not found", filepath.Join("does", "not", "exist")},
		},
		{
			name:        "npm package source is rejected because pi ignores it on -e",
			agentYAML:   "    provider: pi\n    extensions:\n      - npm:pi-claude-bridge\n",
			errContains: []string{"extensions[0]", `"npm:pi-claude-bridge"`, "package sources are not supported", "path"},
		},
		{
			name:        "git package source is rejected",
			agentYAML:   "    provider: pi\n    extensions:\n      - git:github.com/x/y\n",
			errContains: []string{"extensions[0]", "package sources are not supported"},
		},
		{
			name:        "empty extension entry is rejected",
			agentYAML:   "    provider: pi\n    extensions:\n      - \"\"\n",
			errContains: []string{"extensions[0]", "must not be empty"},
		},
		{
			name:        "extensions on a non-pi provider is rejected",
			agentYAML:   "    provider: claude-code\n    extensions:\n      - builtin:mcp\n",
			errContains: []string{"extensions", `only supported for provider "pi"`, `"claude-code"`},
		},
		{
			name:        "extensions that is not a list is rejected",
			agentYAML:   "    provider: pi\n    extensions: builtin:mcp\n",
			errContains: []string{"extensions", "must be a list of strings"},
		},
		{
			name:        "non-string extension entry is rejected",
			agentYAML:   "    provider: pi\n    extensions:\n      - 42\n",
			errContains: []string{"extensions[0]", "must be a list of strings"},
		},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := config.Load(writeCompany(t, tc.agentYAML))
			if err == nil {
				t.Fatalf("Load succeeded with %+v; want error", c)
			}
			for _, want := range tc.errContains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

// TestLoad_MCPServers covers the pi-only per-agent mcp_servers field (S4) from
// odd/tasks/software-factory.md.
func TestLoad_MCPServers(t *testing.T) {
	t.Run("valid servers load with command and args", func(t *testing.T) {
		c, err := config.Load(writeCompany(t, "    mcp_servers:\n      chrome-devtools:\n        command: npx\n        args: [\"chrome-devtools-mcp\", \"--headless\"]\n      plain_1:\n        command: /usr/bin/tool\n"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := map[string]config.MCPServer{
			"chrome-devtools": {Command: "npx", Args: []string{"chrome-devtools-mcp", "--headless"}},
			"plain_1":         {Command: "/usr/bin/tool"},
		}
		if !reflect.DeepEqual(c.Agents[0].MCPServers, want) {
			t.Errorf("MCPServers = %#v; want %#v", c.Agents[0].MCPServers, want)
		}
	})

	t.Run("absent field leaves MCPServers nil", func(t *testing.T) {
		c, err := config.Load(writeCompany(t, ""))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if c.Agents[0].MCPServers != nil {
			t.Errorf("MCPServers = %v; want nil", c.Agents[0].MCPServers)
		}
	})

	rejects := []struct {
		name, yaml, want string
	}{
		{"non-pi provider", "    provider: claude-code\n    mcp_servers:\n      a:\n        command: x\n", `mcp_servers: only supported for provider "pi", got "claude-code"`},
		{"uppercase name", "    mcp_servers:\n      Bad:\n        command: x\n", `mcp_servers: invalid server name "Bad": must match ^[a-z0-9][a-z0-9_-]*$`},
		{"leading dash name", "    mcp_servers:\n      -bad:\n        command: x\n", `mcp_servers: invalid server name "-bad": must match ^[a-z0-9][a-z0-9_-]*$`},
		{"reserved framework name", "    mcp_servers:\n      framework:\n        command: x\n", `mcp_servers: server name "framework" is reserved for the framework's own MCP server`},
		{"empty command", "    mcp_servers:\n      a:\n        args: [x]\n", `mcp_servers["a"]: command is required`},
		{"unknown key", "    mcp_servers:\n      a:\n        command: x\n        env: {A: b}\n", `mcp_servers["a"]: unknown field "env"`},
		{"not a map", "    mcp_servers: [a]\n", `mcp_servers: must be a map of server name to {command, args}`},
		{"args not strings", "    mcp_servers:\n      a:\n        command: x\n        args: [1]\n", `mcp_servers["a"]: args must be a list of strings`},
		{"names differing only in dash and underscore", "    mcp_servers:\n      a_b:\n        command: x\n      a-b:\n        command: y\n", `mcp_servers: server names "a-b" and "a_b" collide: pi treats "-" and "_" in server names as the same character`},
	}
	for _, tc := range rejects {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			_, err := config.Load(writeCompany(t, tc.yaml))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load error = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}

// S6: the "__" server-name rule is gone (exact tool names make it unnecessary).
func TestLoad_MCPServerNameWithDoubleUnderscoreAccepted(t *testing.T) {
	cfg, err := config.Load(writeCompany(t, "    mcp_servers:\n      github__prod:\n        command: x\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := cfg.Agents[0].MCPServers["github__prod"]; !ok {
		t.Errorf("servers = %v", cfg.Agents[0].MCPServers)
	}
}
