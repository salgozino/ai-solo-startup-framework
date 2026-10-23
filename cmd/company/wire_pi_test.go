package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// TestMaterializeAgents_PiProviderWiresAdapterWithModelAndExtensions verifies that
// provider "pi" selects the pi adapter (odd/tasks/pi-adapter.md S1) and hands it the
// agent's model and extensions allowlist (S4). A fake `pi` on PATH records its argv and
// lists a different model, so the startup ProbeModel fails deterministically: the error
// proves the pi case was taken (not "unknown provider") and the recorded argv proves the
// extensions reached the adapter.
func TestMaterializeAgents_PiProviderWiresAdapterWithModelAndExtensions(t *testing.T) {
	binDir := t.TempDir()
	argvFile := filepath.Join(binDir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\n" +
		"echo 'provider      model                     context  max-out  thinking  images'\n" +
		"echo 'openai        gpt-4o                    128K     16.4K    no        yes'\n"
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(script), 0o700); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_AUTH_TOKEN_PI_WIRE", "secret")

	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "TEST_AUTH_TOKEN_PI_WIRE",
		RiskPolicy:   map[string]config.Policy{},
		Agents: []config.AgentConfig{{
			Name: "ceo", Role: "ceo", Provider: "pi",
			Model:      "openai-codex/gpt-6-luna",
			Extensions: []string{"builtin:llama.cpp"},
		}},
	}

	var stderr bytes.Buffer
	_, err := materializeAgents(cfg, wireOptions{stderr: &stderr, storeDir: t.TempDir()})
	if err == nil {
		t.Fatal("materializeAgents succeeded; want the pi model probe to fail for an unlisted model")
	}
	for _, want := range []string{`agent "ceo" model probe failed`, "openai-codex/gpt-6-luna"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}

	raw, readErr := os.ReadFile(argvFile)
	if readErr != nil {
		t.Fatalf("fake pi was never invoked: %v", readErr)
	}
	got := strings.Fields(string(raw))
	want := []string{"-ne", "-e", "builtin:llama.cpp", "--offline", "--list-models"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("probe argv = %q; want %q", got, want)
	}
}

// TestPiMCPServers pins the config -> adapter conversion of an agent's mcp_servers.
func TestPiMCPServers(t *testing.T) {
	if got := piMCPServers(nil); got != nil {
		t.Errorf("piMCPServers(nil) = %v; want nil", got)
	}
	got := piMCPServers(map[string]config.MCPServer{"a": {Command: "npx", Args: []string{"x"}}})
	if s, ok := got["a"]; !ok || s.Command != "npx" || len(s.Args) != 1 || s.Args[0] != "x" {
		t.Errorf("piMCPServers = %#v", got)
	}
}

// S6: a declared mcp_servers entry is probed at materialize; a failing probe aborts
// before any server binds, naming the agent and the server.
func TestMaterializeAgents_PiMCPServerProbeFailureAbortsMaterialize(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("write fake pi: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_AUTH_TOKEN_PI_PROBE", "secret")

	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "TEST_AUTH_TOKEN_PI_PROBE",
		RiskPolicy:   map[string]config.Policy{},
		Agents: []config.AgentConfig{{
			Name: "qa", Role: "qa", Provider: "pi",
			MCPServers: map[string]config.MCPServer{"browser": {Command: filepath.Join(binDir, "missing-mcp")}},
		}},
	}
	_, err := materializeAgents(cfg, wireOptions{stderr: &bytes.Buffer{}, storeDir: t.TempDir()})
	if err == nil {
		t.Fatal("materializeAgents succeeded; want the mcp server probe to fail")
	}
	for _, want := range []string{`agent "qa" mcp probe failed`, `pi: mcp server "browser" probe failed`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

// TestMaterializeAgents_MaxRoundsReachesSupervisor pins config max_rounds -> supervisor cap.
func TestMaterializeAgents_MaxRoundsReachesSupervisor(t *testing.T) {
	t.Setenv("TEST_AUTH_TOKEN_MAXR", "secret")
	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "TEST_AUTH_TOKEN_MAXR",
		RiskPolicy:   map[string]config.Policy{},
		Agents: []config.AgentConfig{
			{Name: "ceo", Role: "ceo", Provider: "pi", MaxRounds: 12},
			{Name: "eng", Role: "engineer", Provider: "pi"},
		},
	}
	runtimes, err := materializeAgents(cfg, wireOptions{
		stderr: &bytes.Buffer{}, storeDir: t.TempDir(), providerOverride: &fake.Provider{},
		authTokenOverride: "test-bearer-token",
	})
	if err != nil {
		t.Fatalf("materializeAgents: %v", err)
	}
	t.Cleanup(func() {
		for _, rt := range runtimes {
			_ = rt.srv.Shutdown(context.Background())
		}
	})
	got := map[string]int{}
	for i, rt := range runtimes {
		got[cfg.Agents[i].Name] = rt.sup.MaxRounds()
	}
	if got["ceo"] != 12 || got["eng"] != 8 {
		t.Errorf("MaxRounds = %v; want ceo 12, eng 8 (unset -> default)", got)
	}
}
