// Package main — cmd_test.go tests the command-layer behaviour:
// config.Load inline-token rejection, and materialize starting goroutines per agent.
package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	transmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
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
auth_token_env: A2A_AUTH_TOKEN
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

// extractMCPAddr parses the "mcp: server listening on <addr>" line materializeAgents
// writes to opts.stderr and returns <addr>, for tests that need to reach the
// owned MCP server it started without any other way to obtain the instance.
func extractMCPAddr(t *testing.T, stderr string) string {
	t.Helper()
	const prefix = "mcp: server listening on "
	idx := strings.Index(stderr, prefix)
	if idx == -1 {
		t.Fatalf("stderr does not contain the MCP listening line: %q", stderr)
	}
	rest := stderr[idx+len(prefix):]
	if end := strings.IndexByte(rest, '\n'); end != -1 {
		rest = rest[:end]
	}
	return strings.TrimSpace(rest)
}

// TestMaterializeAgents_ShutsDownOwnedMCPServerOnError verifies the fix for "MCP server
// leaks on partial materialize failure": when materializeAgents starts its own MCP
// server (opts.mcpServer is nil) and then fails partway through the agent loop, the
// owned server must be shut down before the error is returned — not left bound.
func TestMaterializeAgents_ShutsDownOwnedMCPServerOnError(t *testing.T) {
	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "TEST_AUTH_TOKEN_MCP_SHUTDOWN",
		RiskPolicy:   map[string]config.Policy{},
		Agents: []config.AgentConfig{
			// An unrecognized provider hits the "wire: unknown provider" error path
			// inside the loop, after the owned MCP server has already been started.
			{Name: "ceo", Role: "ceo", Provider: "unknown-provider"},
		},
	}
	t.Setenv("TEST_AUTH_TOKEN_MCP_SHUTDOWN", "secret")

	var stderr bytes.Buffer
	_, err := materializeAgents(cfg, wireOptions{
		stderr:   &stderr,
		storeDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}

	addr := extractMCPAddr(t, stderr.String())

	// The owned MCP server must have been shut down on this error path: connection
	// attempts to its address must eventually be refused. This polls rather than
	// dialing once because transport/mcp.Server.Start spawns http.Server.Serve in a
	// background goroutine; Shutdown can race that goroutine's own listener
	// registration (net/http only closes listeners it has tracked), so a listening
	// socket may briefly still accept a connection immediately after Shutdown
	// returns. That registration always completes very quickly on its own, so
	// polling for up to 2s reliably observes the server closed without depending
	// on that internal timing — and without touching transport/mcp itself.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr != nil {
			return // refused — the owned server was shut down.
		}
		conn.Close()
		if time.Now().After(deadline) {
			t.Fatalf("expected MCP server at %s to be shut down after materializeAgents error, but it kept accepting connections for 2s", addr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestMaterializeAgents_InjectedUnstartedMCPServer_ReturnsErrorNotPanic verifies the fix
// for the unguarded injected-server dereference: a *transmcp.Server injected via
// wireOptions.mcpServer that was never Start()ed has a nil listener, and calling its
// Addr() method directly would panic. materializeAgents must convert that into a plain
// error instead of crashing.
func TestMaterializeAgents_InjectedUnstartedMCPServer_ReturnsErrorNotPanic(t *testing.T) {
	cfg := &config.CompanyConfig{
		Tenant:       "acme",
		AuthTokenEnv: "TEST_AUTH_TOKEN_UNSTARTED_MCP",
		RiskPolicy:   map[string]config.Policy{},
		Agents: []config.AgentConfig{
			{Name: "ceo", Role: "ceo", Provider: "claude-code"},
		},
	}
	t.Setenv("TEST_AUTH_TOKEN_UNSTARTED_MCP", "secret")

	unstarted := transmcp.New(cfg.Tenant, cfg.RiskPolicy, transmcp.NewRegistry())
	// Deliberately never call Start: the injected server's listener stays nil.

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("materializeAgents panicked on an unstarted injected MCP server: %v", r)
		}
	}()

	_, err := materializeAgents(cfg, wireOptions{
		mcpServer: unstarted,
		storeDir:  t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected an error for an unstarted injected MCP server, got nil")
	}
}
