// Package main — wire_test.go tests materializeAgents's peer-directory wiring:
// the duplicate-role guard and per-agent role-to-base-URL binding.
package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// TestMaterializeAgents_DuplicateRoleFailsBeforeAnyReady verifies that two
// agents declaring the same role fail materializeAgents with an explicit
// error naming the duplicate role, before any agent's A2A server starts
// listening (a supervisor can only be marked ready via transa2a.New, which
// runs inside the per-agent loop — this asserts materialize never reaches
// that loop for a duplicate-role config, evidenced by the absence of any
// "wire: agent ... started" log line).
// Satisfies: company-as-code "Two agents declaring the same role fail
// materialize with a named error".
func TestMaterializeAgents_DuplicateRoleFailsBeforeAnyReady(t *testing.T) {
	provider := &fake.Provider{
		ReturnRunResult: port.ProviderResult{Output: "done"},
	}
	gw := &fake.Gateway{}

	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "A2A_AUTH_TOKEN",
		Agents: []config.AgentConfig{
			{Name: "ceo", Role: "engineer", Provider: "claude-code"},
			{Name: "worker", Role: "engineer", Provider: "claude-code"},
		},
		RiskPolicy: map[string]config.Policy{},
	}

	var stderr bytes.Buffer
	runtimes, err := materializeAgents(cfg, wireOptions{
		providerOverride:  provider,
		gatewayOverride:   gw,
		storeDir:          t.TempDir(),
		stderr:            &stderr,
		authTokenOverride: "test-bearer-token",
	})
	if err == nil {
		t.Fatal("expected an error for a duplicate role, got nil")
	}
	if !strings.Contains(err.Error(), "engineer") {
		t.Errorf("expected the error to name the duplicate role %q, got %q", "engineer", err.Error())
	}
	if len(runtimes) != 0 {
		t.Errorf("expected no runtimes on a duplicate-role failure, got %d", len(runtimes))
	}
	if strings.Contains(stderr.String(), "wire: agent") {
		t.Errorf("expected no agent to have started before the duplicate-role check, but stderr contains a start log: %q", stderr.String())
	}
}

// TestMaterializeAgents_DistinctRolesBindEachAgent verifies that materializing
// a company with distinct agent roles binds every role to its own agent's
// actual live base URL in the shared peer directory — never another agent's
// URL, never left unbound.
// Satisfies: company-as-code "Distinct roles across agents materialize
// normally"; peer-directory "A bound agent's role is registered with its
// live base URL".
func TestMaterializeAgents_DistinctRolesBindEachAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("cmd: skipping in -short mode")
	}

	provider := &fake.Provider{
		ReturnRunResult: port.ProviderResult{Output: "done"},
	}
	gw := &fake.Gateway{}

	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "A2A_AUTH_TOKEN",
		Agents: []config.AgentConfig{
			{Name: "ceo", Role: "ceo", Provider: "claude-code"},
			{Name: "worker", Role: "engineer", Provider: "claude-code"},
		},
		RiskPolicy: map[string]config.Policy{},
	}

	runtimes, err := materializeAgents(cfg, wireOptions{
		providerOverride:  provider,
		gatewayOverride:   gw,
		storeDir:          t.TempDir(),
		stderr:            &bytes.Buffer{},
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

	if len(runtimes) != len(cfg.Agents) {
		t.Fatalf("expected %d runtimes, got %d", len(cfg.Agents), len(runtimes))
	}

	dir := runtimes[0].dir
	if dir == nil {
		t.Fatal("expected agentRuntime.dir to be populated")
	}

	for i, rt := range runtimes {
		role := cfg.Agents[i].Role
		got, err := dir.BaseURL(role)
		if err != nil {
			t.Fatalf("dir.BaseURL(%q): %v", role, err)
		}
		if got != rt.srv.BaseURL() {
			t.Errorf("dir.BaseURL(%q) = %q, want %q (agent %q's own base URL)",
				role, got, rt.srv.BaseURL(), cfg.Agents[i].Name)
		}
	}

	// Cross-check: every agentRuntime shares the SAME directory instance, so the
	// engineer role is never resolvable to the ceo's base URL or vice versa.
	if runtimes[0].dir != runtimes[1].dir {
		t.Error("expected all agentRuntimes to share the same *PeerDirectory instance")
	}
}
