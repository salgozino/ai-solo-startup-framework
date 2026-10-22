package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
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
