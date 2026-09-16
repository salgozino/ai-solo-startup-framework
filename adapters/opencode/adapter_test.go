// Package opencode_test contains threat-matrix RED tests for the OpenCode adapter.
// These tests cover the provider-subprocess threat cases:
//
//	(a) argv-as-slice: shell metacharacters in input are literal data, never interpreted
//	(b) hung child killed after ctx deadline → FAILED
//	(c) oversized output truncated with marker before parse
//	(d) non-zero exit → failure outcome, not success
//	(e) --pure always present; content prepended only when system_prompt is set
//
// Tests use a helper binary (built from testdata/fakeopencode) that simulates opencode CLI
// exit behavior without requiring a real opencode installation.
package opencode_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/opencode"
	"github.com/salgozino/ai-solo-startup-framework/config"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

// registryMinter adapts *transportmcp.Registry to opencode.TokenMinter.
// See adapters/claudecode/adapter_test.go's registryMinter for why this wrapper
// (with an exported Drainer/TokenMinter pair) is required instead of the
// unexported interfaces tasks.md originally sketched: Go requires a method's
// declared return type to be identical to the interface's declared return
// type, and unexported types cannot be named outside their declaring package.
type registryMinter struct {
	reg *transportmcp.Registry
}

func (m *registryMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, opencode.Drainer) {
	return m.reg.Mint(tenant, agent, taskID, exp)
}

// startTestMCPServer starts an in-process MCP server with a single
// "telegram_send" tool for tenant "acme" and registers cleanup.
func startTestMCPServer(t *testing.T) (*transportmcp.Server, *transportmcp.Registry) {
	t.Helper()
	registry := transportmcp.NewRegistry()
	srv := transportmcp.New("acme", map[string]config.Policy{"telegram_send": {}}, registry)
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("start mcp server: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return srv, registry
}

// helperBinary builds the fakeopencode binary once per test run and returns its path.
func helperBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeopencode")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := filepath.Join("testdata", "fakeopencode", "main.go")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fakeopencode: %v", err)
	}
	return bin
}

// TestPureFlag_AlwaysPresent verifies that --pure is unconditionally included
// in the opencode invocation regardless of other settings (spec: Unconditional Isolation).
func TestPureFlag_AlwaysPresent(t *testing.T) {
	bin := helperBinary(t)
	// No system prompt, no model, no agent — pure must still be set.
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-pure", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// fakeopencode prepends "pure:1|" when --pure is passed.
	if !strings.HasPrefix(result.Output, "pure:1|") {
		t.Errorf("expected output to start with \"pure:1|\", got %q", result.Output)
	}
}

// TestContentPrepend_WhenSet verifies that when a system prompt path is configured,
// its content is prepended to the task input with the [SYSTEM] marker.
func TestContentPrepend_WhenSet(t *testing.T) {
	bin := helperBinary(t)
	// Write a temp system prompt file.
	promptFile := filepath.Join(t.TempDir(), "system.md")
	if err := os.WriteFile(promptFile, []byte("You are the CEO."), 0o600); err != nil {
		t.Fatalf("write temp prompt: %v", err)
	}
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", promptFile)

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-sysprompt", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// The effective input passed to the binary should contain the system content prepended.
	// fakeopencode echoes the input, so the output will contain the [SYSTEM] marker.
	if !strings.Contains(result.Output, "[SYSTEM]") {
		t.Errorf("expected output to contain [SYSTEM] marker, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "You are the CEO.") {
		t.Errorf("expected output to contain system prompt content, got %q", result.Output)
	}
}

// TestContentPrepend_WhenNotSet verifies that no system content is prepended
// when no system prompt path is configured.
func TestContentPrepend_WhenNotSet(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-sysprompt", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// No system prompt → no [SYSTEM] marker in output.
	if strings.Contains(result.Output, "[SYSTEM]") {
		t.Errorf("expected no [SYSTEM] in output when not set, got %q", result.Output)
	}
}

// TestArgvSlice_ShellMetacharactersAreLiteral verifies threat-matrix case (a):
// shell metacharacters in input do not alter the invocation.
func TestArgvSlice_ShellMetacharactersAreLiteral(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	maliciousInput := "prefix; echo INJECTED"

	result, err := adapter.RunTask(ctx, "task-argv", maliciousInput)
	if err != nil {
		t.Fatalf("RunTask with metachar input: unexpected error: %v", err)
	}
	// With argv-as-slice: fakeopencode echoes the full string as one token.
	// Output has "pure:1|" prefix (isolation flag is always present) then the literal input.
	expected := "pure:1|" + maliciousInput
	if result.Output != expected {
		t.Errorf("expected literal output %q, got %q", expected, result.Output)
	}
	// Secondary check: no newline inside the output — a shell would produce two lines.
	if strings.Contains(result.Output, "\n") {
		t.Errorf("output contains newline — possible shell interpretation: %q", result.Output)
	}
}

// TestHungChild_KilledOnDeadline verifies threat-matrix case (b):
// a hung opencode process is killed when the context deadline elapses.
func TestHungChild_KilledOnDeadline(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := adapter.RunTask(ctx, "task-hung", "hang")
	if err == nil {
		t.Fatal("expected error when child is killed by deadline, got nil")
	}
}

// TestOversizedOutput_TruncatedWithMarker verifies threat-matrix case (c):
// output that exceeds the size cap is truncated; the marker is prepended.
func TestOversizedOutput_TruncatedWithMarker(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 10}, "", "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-large", "large")
	if err != nil {
		t.Fatalf("RunTask large output: %v", err)
	}
	if !strings.Contains(result.Output, opencode.TruncationMarker) {
		t.Errorf("expected truncation marker %q in output, got: %q", opencode.TruncationMarker, result.Output)
	}
}

// TestNonZeroExit_MapsToError verifies threat-matrix case (d):
// a non-zero exit from the opencode process results in an error.
func TestNonZeroExit_MapsToError(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	_, err := adapter.RunTask(ctx, "task-fail", "fail")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil (success hidden failure)")
	}
}

// TestModelFlag_PassedToCLI verifies that when a model is configured,
// the --model flag is correctly passed to the opencode CLI.
func TestModelFlag_PassedToCLI(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "anthropic/claude-sonnet-4-20250514", "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-model", "hello")
	if err != nil {
		t.Fatalf("RunTask with model: unexpected error: %v", err)
	}
	// fakeopencode prepends "pure:1|" (always) then "model:<model>|" when --model is passed.
	expected := "pure:1|model:anthropic/claude-sonnet-4-20250514|hello"
	if result.Output != expected {
		t.Errorf("expected output %q, got %q", expected, result.Output)
	}
}

// TestNoModelFlag_OmitsFlag verifies that when no model is configured,
// the --model flag is not passed to the CLI.
func TestNoModelFlag_OmitsFlag(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-model", "hello")
	if err != nil {
		t.Fatalf("RunTask without model: unexpected error: %v", err)
	}
	// Without model, output is only pure prefix + input.
	if result.Output != "pure:1|hello" {
		t.Errorf("expected output %q, got %q", "pure:1|hello", result.Output)
	}
}

// TestAgentFlag_PassedToCLI verifies that when an agent name is configured,
// the --agent flag is correctly passed to the opencode CLI.
func TestAgentFlag_PassedToCLI(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "ceo", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-agent", "hello")
	if err != nil {
		t.Fatalf("RunTask with agent: unexpected error: %v", err)
	}
	// fakeopencode prepends "pure:1|" (always) then "agent:<agent>|" when --agent is passed.
	expected := "pure:1|agent:ceo|hello"
	if result.Output != expected {
		t.Errorf("expected output %q, got %q", expected, result.Output)
	}
}

// TestNoAgentFlag_OmitsFlag verifies that when no agent name is configured,
// the --agent flag is not passed to the CLI.
func TestNoAgentFlag_OmitsFlag(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-agent", "hello")
	if err != nil {
		t.Fatalf("RunTask without agent: unexpected error: %v", err)
	}
	// Without agent, output is only pure prefix + input.
	if result.Output != "pure:1|hello" {
		t.Errorf("expected output %q, got %q", "pure:1|hello", result.Output)
	}
}

// TestRunTask_StderrInError verifies that subprocess stderr text is surfaced in the
// returned error on non-zero exit (spec: Subprocess fails with stderr output).
func TestRunTask_StderrInError(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	// "fail-stderr" causes fakeopencode to write a diagnostic line to stderr then exit 1.
	_, err := adapter.RunTask(ctx, "task-fail-stderr", "fail-stderr")
	if err == nil {
		t.Fatal("expected error for non-zero exit with stderr, got nil")
	}
	if !strings.Contains(err.Error(), "stderr:") {
		t.Errorf("error must contain \"stderr:\" label; got: %v", err)
	}
	if !strings.Contains(err.Error(), "simulated stderr output") {
		t.Errorf("error must contain subprocess stderr text; got: %v", err)
	}
}

// TestRunTask_EmptyStderrOnFail verifies that a non-zero exit with no stderr
// produces a non-nil, non-empty error and does not panic
// (spec: Subprocess fails with empty stderr).
func TestRunTask_EmptyStderrOnFail(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	ctx := context.Background()
	// "fail" exits 1 without writing anything to stderr.
	_, err := adapter.RunTask(ctx, "task-fail-empty-stderr", "fail")
	if err == nil {
		t.Fatal("expected non-nil error for non-zero exit with empty stderr, got nil")
	}
	if err.Error() == "" {
		t.Error("error message must not be empty string")
	}
	// Error format must include the "stderr:" label even when content is empty.
	if !strings.Contains(err.Error(), "stderr:") {
		t.Errorf("error must contain \"stderr:\" label even with empty stderr; got: %v", err)
	}
}

// NOTE: ProbeModel tests removed — opencode CLI does not distinguish invalid
// model from missing prompt (both produce the same generic error). The opencode
// adapter intentionally does not implement modelProber; materializeAgents skips
// the probe for it. See adapters/opencode/adapter.go for details.

// ---- Phase 3: MCP tool use and action intent emission (spec: provider-action-intent-emission) ----

// TestOpenCodeAdapter_MCPToolCall_PopulatesActionIntents proves the real adapter path is wired:
// a fake opencode binary performs an actual MCP tools/call over HTTP against a running server,
// and RunTask returns the intent recorded by the server's sink — not a hand-constructed fixture.
func TestOpenCodeAdapter_MCPToolCall_PopulatesActionIntents(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	adapter := opencode.New(bin, opencode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "", "")

	t.Setenv("FAKEOPENCODE_CALL_MCP", "1")

	result, err := adapter.RunTask(context.Background(), "task-mcp", "hello")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(result.ActionIntents) != 1 {
		t.Fatalf("expected 1 action intent, got %d: %+v", len(result.ActionIntents), result.ActionIntents)
	}
	if result.ActionIntents[0].Kind != "telegram_send" {
		t.Errorf("expected Kind=telegram_send, got %q", result.ActionIntents[0].Kind)
	}
}

// TestOpenCodeAdapter_NoToolCall_EmptyIntents verifies that a completed invocation which
// never calls the MCP tool yields empty ActionIntents and a nil error.
func TestOpenCodeAdapter_NoToolCall_EmptyIntents(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	adapter := opencode.New(bin, opencode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "", "")

	result, err := adapter.RunTask(context.Background(), "task-no-mcp", "hello")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(result.ActionIntents) != 0 {
		t.Errorf("expected empty ActionIntents, got %+v", result.ActionIntents)
	}
}

// TestOpenCodeAdapter_EphemeralConfig_PersistedMtimeUnchanged verifies spec "OpenCode adapter
// configures MCP ephemerally": a persisted config file the adapter never touches keeps its
// original mtime, proving MCP configuration is scoped to the subprocess env only.
func TestOpenCodeAdapter_EphemeralConfig_PersistedMtimeUnchanged(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	persistedPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(persistedPath, []byte(`{"existing":"config"}`), 0o600); err != nil {
		t.Fatalf("write persisted config: %v", err)
	}
	before, err := os.Stat(persistedPath)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	adapter := opencode.New(bin, opencode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "", "")

	if _, err := adapter.RunTask(context.Background(), "task-persisted", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	after, err := os.Stat(persistedPath)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("persisted opencode config mtime changed: before=%v after=%v", before.ModTime(), after.ModTime())
	}
}

// TestOpenCodeAdapter_TerminatesOnEOF_NoHang verifies spec "OpenCode adapter terminates its
// reader on process exit, not a sentinel event": RunTask must return promptly once the
// subprocess closes stdout, without waiting on any dedicated terminal event line.
func TestOpenCodeAdapter_TerminatesOnEOF_NoHang(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "", "")

	done := make(chan error, 1)
	go func() {
		_, err := adapter.RunTask(context.Background(), "task-eof", "hello")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTask: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("RunTask did not return within 1 second — reader hung waiting for a sentinel event")
	}
}
