// Package main — wire_test.go tests materializeAgents's peer-directory wiring:
// the duplicate-role guard and per-agent role-to-base-URL binding.
package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// TestMaterializeAgents_DuplicateRoleFailsBeforeAnyReady verifies that two
// agents declaring the same role fail materializeAgents with an explicit
// error naming the duplicate role, before any agent's supervisor is
// constructed, its A2A listener opened, or its state machine marked ready.
//
// A supervisor can only reach IDLE through transa2a.New, which runs inside
// the per-agent loop. That loop body's first side effect on the world is
// supervisor.NewStore, which os.MkdirAll's <storeDir>/<agent name> — strictly
// before supervisor.New, before transa2a.New opens its TCP listener, and
// before sup.MarkReady(). An empty storeDir therefore proves the loop body
// never executed for any agent at all, which is what "before any ready"
// means. That is a recorded side effect of production code rather than the
// absence of a log line: the log is emitted at the very end of an iteration,
// so it cannot distinguish "never entered the loop" from "entered it, started
// a server, then failed". The error's identity is asserted for the same
// reason — the pre-loop directory declaration guard and the in-loop Bind
// rebind rejection both name the role, so only the wording separates them.
//
// TestMaterializeAgents_DistinctRolesBindEachAgent pins the other half of
// this invariant by asserting the successful path does create one store
// directory per agent, so the side effect cannot be dropped without a test
// noticing.
// Satisfies: company-as-code "Two agents declaring the same role fail
// materialize with a named error"; task 3.3 (no supervisor reaches a ready
// state on a duplicate-role config).
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

	storeDir := t.TempDir()
	var stderr bytes.Buffer
	runtimes, err := materializeAgents(cfg, wireOptions{
		providerOverride:  provider,
		gatewayOverride:   gw,
		storeDir:          storeDir,
		stderr:            &stderr,
		authTokenOverride: "test-bearer-token",
	})
	t.Cleanup(func() {
		for _, rt := range runtimes {
			_ = rt.srv.Shutdown(context.Background())
		}
	})

	if err == nil {
		t.Fatal("expected an error for a duplicate role, got nil")
	}
	if !strings.Contains(err.Error(), "engineer") {
		t.Errorf("expected the error to name the duplicate role %q, got %q", "engineer", err.Error())
	}
	if !strings.Contains(err.Error(), "duplicate role") {
		t.Errorf("expected the pre-loop declaration guard to reject the config (%q), got %q", "duplicate role", err.Error())
	}
	if strings.Contains(err.Error(), "already bound") {
		t.Errorf("expected the duplicate to be caught before the per-agent loop, but the error comes from Bind's in-loop rebind rejection: %q", err.Error())
	}
	if len(runtimes) != 0 {
		t.Errorf("expected no runtimes on a duplicate-role failure, got %d", len(runtimes))
	}

	entries, readErr := os.ReadDir(storeDir)
	if readErr != nil {
		t.Fatalf("read store dir: %v", readErr)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected materialize to fail before the per-agent loop ran for any agent, "+
			"but it created supervisor store(s) %v — a supervisor was constructed, so transa2a.New "+
			"may already have opened a listener and marked it ready", names)
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

	storeDir := t.TempDir()
	runtimes, err := materializeAgents(cfg, wireOptions{
		providerOverride:  provider,
		gatewayOverride:   gw,
		storeDir:          storeDir,
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

	// Pin the side effect TestMaterializeAgents_DuplicateRoleFailsBeforeAnyReady
	// reads as evidence that the per-agent loop body ran: entering that body
	// creates one supervisor store directory per agent, before the agent's A2A
	// listener is opened. If this ever stops holding, the duplicate-role test's
	// "no store directory" assertion stops meaning "no supervisor was started"
	// and must be reworked rather than silently weakened.
	entries, readErr := os.ReadDir(storeDir)
	if readErr != nil {
		t.Fatalf("read store dir: %v", readErr)
	}
	if len(entries) != len(cfg.Agents) {
		t.Errorf("expected one supervisor store directory per materialized agent (%d), got %d",
			len(cfg.Agents), len(entries))
	}
}
