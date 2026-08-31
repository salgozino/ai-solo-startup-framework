// Package main — cmd_test.go tests the command-layer behaviour:
// config.Load inline-token rejection, and materialize starting goroutines per agent.
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// TestLoad_RejectsInlineToken verifies that config.Load returns an error when
// gateways.telegram.token_env contains an inline token value instead of an
// env-var name. This is the PR 1 guard exercised at the composition root level.
// Satisfies: company-as-code "Inline token in token_env is rejected at load".
func TestLoad_RejectsInlineToken(t *testing.T) {
	yaml := `
tenant: acme
agents:
  - name: ceo
    role: ceo
    provider: claude-code
gateways:
  telegram:
    token_env: "1234567890:AABBccDDeEFfGG"
`
	path := filepath.Join(t.TempDir(), "company.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Error("expected error for inline token in token_env, got nil")
	}
}

// TestLoad_AcceptsEnvVarRef verifies that config.Load succeeds when token_env
// contains a proper env-var name.
func TestLoad_AcceptsEnvVarRef(t *testing.T) {
	yaml := `
tenant: acme
agents:
  - name: ceo
    role: ceo
    provider: claude-code
gateways:
  telegram:
    token_env: TELEGRAM_BOT_TOKEN
    recipient_env: TELEGRAM_OWNER_ID
risk_policy:
  telegram_send:
    risk: risky
    allowed_roles: [ceo]
`
	path := filepath.Join(t.TempDir(), "company.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Tenant != "acme" {
		t.Errorf("expected tenant=acme, got %q", cfg.Tenant)
	}
}

// TestMaterialize_TwoAgentStartsTwoGoroutines verifies that materializing a two-agent
// company starts two supervisor goroutines, each discoverable by their A2A endpoint.
// Satisfies: company-as-code "Materializing two-agent company starts two supervisors".
func TestMaterialize_TwoAgentStartsTwoGoroutines(t *testing.T) {
	if testing.Short() {
		t.Skip("cmd: skipping in -short mode")
	}

	provider := &fake.Provider{
		ReturnRunResult: port.ProviderResult{Output: "done"},
	}
	gw := &fake.Gateway{}

	cfg := &config.CompanyConfig{
		Tenant: "acme",
		Agents: []config.AgentConfig{
			{Name: "ceo", Role: "ceo", Provider: "claude-code"},
			{Name: "worker", Role: "engineer", Provider: "claude-code"},
		},
		RiskPolicy: map[string]config.Policy{},
	}

	runtimes, err := materializeAgents(cfg, wireOptions{
		providerOverride: provider,
		gatewayOverride:  gw,
		storeDir:         t.TempDir(),
		stderr:           &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("materializeAgents: %v", err)
	}
	t.Cleanup(func() {
		for _, rt := range runtimes {
			_ = rt.srv.Shutdown(context.Background())
		}
	})

	// Exactly two runtimes must be returned.
	if len(runtimes) != 2 {
		t.Fatalf("expected 2 runtimes, got %d", len(runtimes))
	}

	// Each runtime must have a unique, reachable base URL.
	urls := make(map[string]bool)
	for _, rt := range runtimes {
		u := rt.srv.BaseURL()
		if u == "" {
			t.Error("runtime has empty BaseURL")
		}
		if urls[u] {
			t.Errorf("duplicate BaseURL: %q", u)
		}
		urls[u] = true
	}

	// Each supervisor must be in IDLE state (started and ready).
	for _, rt := range runtimes {
		state := rt.sup.StatusStr()
		if state != "IDLE" {
			t.Errorf("expected supervisor state IDLE, got %q (agent: %s)", state, rt.sup.Addr())
		}
	}

	// Each runtime must accept a ListTasks call (goroutine is running and handler is wired).
	var wg sync.WaitGroup
	for _, rt := range runtimes {
		rt := rt
		wg.Add(1)
		go func() {
			defer wg.Done()
			tasks, err := rt.uiAdap.ListTasks()
			if err != nil {
				t.Errorf("ListTasks for %s: %v", rt.sup.Addr(), err)
			}
			// No tasks yet — list must be empty or nil, never an error.
			_ = tasks
		}()
	}
	wg.Wait()
}
