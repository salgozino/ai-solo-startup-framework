// Package claudecode_test contains threat-matrix RED tests for the Claude Code adapter.
// These tests cover the provider-subprocess threat cases from design.md:
//
//	(a) argv-as-slice: shell metacharacters in input are literal data, never interpreted
//	(b) hung child killed after ctx deadline → FAILED
//	(c) oversized output truncated with marker before parse
//	(d) non-zero exit → failure outcome, not success
//
// Tests use a helper binary (built from testdata/fakeclaude) that simulates claude CLI
// exit behavior without requiring a real claude installation.
package claudecode_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
	"github.com/salgozino/ai-solo-startup-framework/config"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

// registryMinter adapts *transportmcp.Registry to claudecode.TokenMinter.
// It exists in the test package (not production code) because Go requires a
// method's declared return type to be IDENTICAL to the interface's declared
// return type for interface satisfaction (no covariant return types):
// *transportmcp.Registry.Mint returns (string, *transportmcp.Handle), which
// does not literally match claudecode.TokenMinter.Mint's declared
// (string, claudecode.Drainer) signature even though *transportmcp.Handle
// structurally satisfies claudecode.Drainer. This wrapper's Mint method
// spells the exact return type so it satisfies the interface.
type registryMinter struct {
	reg *transportmcp.Registry
}

func (m *registryMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, claudecode.Drainer) {
	return m.reg.Mint(tenant, agent, taskID, exp)
}

// spyMinter wraps registryMinter and records every minted token so tests can
// assert the token never leaks into subprocess argv.
type spyMinter struct {
	reg    *transportmcp.Registry
	mu     sync.Mutex
	tokens []string
	exp    time.Time
}

func (m *spyMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, claudecode.Drainer) {
	token, handle := m.reg.Mint(tenant, agent, taskID, exp)
	m.mu.Lock()
	m.tokens = append(m.tokens, token)
	m.exp = exp
	m.mu.Unlock()
	return token, handle
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

// helperBinary builds the fakeclaude binary once per test run and returns its path.
// The binary is placed in t.TempDir() so it is cleaned up automatically.
func helperBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeclaude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := filepath.Join("testdata", "fakeclaude", "main.go")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fakeclaude: %v", err)
	}
	return bin
}

// TestArgvSlice_ShellMetacharactersAreLiteral verifies threat-matrix case (a):
// shell metacharacters in input do not alter the invocation — they are passed as literal data.
//
// If the adapter used "sh -c", the shell would execute `echo INJECTED` and `rm -rf /` as
// separate commands, producing a multi-line output where INJECTED appears on its own line.
// With argv-as-slice, the entire string is passed verbatim as one argument; fakeclaude echoes
// it as-is on a single line. No newline within the output means no command was interpreted.
func TestArgvSlice_ShellMetacharactersAreLiteral(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// This input would produce a second output line ("INJECTED") if run through sh -c.
	maliciousInput := "prefix; echo INJECTED"

	result, err := adapter.RunTask(ctx, "task-argv", maliciousInput)
	if err != nil {
		t.Fatalf("RunTask with metachar input: unexpected error: %v", err)
	}
	// With argv-as-slice: fakeclaude echoes the full string as one token, no newline inside.
	// The output has an "iso:1|" prefix (isolation flags are always present) then the literal input.
	expected := "iso:1|" + maliciousInput
	if result.Output != expected {
		t.Errorf("expected literal output %q, got %q", expected, result.Output)
	}
	// Secondary check: no newline inside the output — a shell would produce two lines.
	if strings.Contains(result.Output, "\n") {
		t.Errorf("output contains newline — possible shell interpretation: %q", result.Output)
	}
}

// TestHungChild_KilledOnDeadline verifies threat-matrix case (b):
// a hung claude process is killed when the context deadline elapses; outcome is FAILED.
func TestHungChild_KilledOnDeadline(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	// Very short deadline — fakeclaude in "hang" mode sleeps indefinitely.
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
	// Tiny limit so the fakeclaude "large" output exceeds it.
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 10}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-large", "large")
	if err != nil {
		t.Fatalf("RunTask large output: %v", err)
	}
	// When truncated, the marker must appear in the output.
	if !strings.Contains(result.Output, claudecode.TruncationMarker) {
		t.Errorf("expected truncation marker %q in output, got: %q", claudecode.TruncationMarker, result.Output)
	}
}

// TestNonZeroExit_MapsToError verifies threat-matrix case (d):
// a non-zero exit from the claude process results in an error, not a success result.
func TestNonZeroExit_MapsToError(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// fakeclaude exits with code 1 when input is "fail".
	_, err := adapter.RunTask(ctx, "task-fail", "fail")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil (success hidden failure)")
	}
}

// TestIsolationFlags_AlwaysPresent verifies that the --safe-mode substitute flags
// (--setting-sources "" and --disable-slash-commands) are unconditionally included in the
// claude invocation regardless of other settings (spec: Unconditional Isolation) — this
// adapter no longer passes --safe-mode itself; see TestClaudeAdapter_MCPFlags_
// NeverCombinedWithDisablingFlag below for the JD-2 regression guard on that removal.
func TestIsolationFlags_AlwaysPresent(t *testing.T) {
	bin := helperBinary(t)
	// No system prompt, no model — isolation flags must still be set.
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-iso", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// fakeclaude prepends "iso:1|" when both --setting-sources and --disable-slash-commands
	// are passed.
	if !strings.HasPrefix(result.Output, "iso:1|") {
		t.Errorf("expected output to start with \"iso:1|\", got %q", result.Output)
	}
}

// TestSystemPromptFile_WhenSet verifies that --system-prompt-file is included
// when a non-empty system prompt path is configured.
func TestSystemPromptFile_WhenSet(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "/abs/path/to/ceo.md")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-sysprompt", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// fakeclaude prepends "sysprompt:<path>|" when --system-prompt-file is passed.
	if !strings.Contains(result.Output, "sysprompt:/abs/path/to/ceo.md|") {
		t.Errorf("expected output to contain sysprompt path, got %q", result.Output)
	}
}

// TestSystemPromptFile_WhenNotSet verifies that --system-prompt-file is absent
// when no system prompt path is configured.
func TestSystemPromptFile_WhenNotSet(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-sysprompt", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// No --system-prompt-file → no sysprompt prefix in output.
	if strings.Contains(result.Output, "sysprompt:") {
		t.Errorf("expected no sysprompt in output when not set, got %q", result.Output)
	}
}

// TestModelFlag_PassedToCLI verifies that when a model is configured,
// the --model flag is correctly passed to the claude CLI.
func TestModelFlag_PassedToCLI(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "anthropic/claude-sonnet-4-20250514", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-model", "hello")
	if err != nil {
		t.Fatalf("RunTask with model: unexpected error: %v", err)
	}
	// fakeclaude prepends "iso:1|" (always) then "model:<model>|" when --model is passed.
	expected := "iso:1|model:anthropic/claude-sonnet-4-20250514|hello"
	if result.Output != expected {
		t.Errorf("expected output %q, got %q", expected, result.Output)
	}
}

// TestRunTask_StderrInError verifies that subprocess stderr text is surfaced in the
// returned error on non-zero exit (spec: Subprocess fails with stderr output).
func TestRunTask_StderrInError(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// "fail-stderr" causes fakeclaude to write a diagnostic line to stderr then exit 1.
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
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

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

// TestNoModelFlag_OmitsFlag verifies that when no model is configured,
// the --model flag is not passed to the CLI.
func TestNoModelFlag_OmitsFlag(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-model", "hello")
	if err != nil {
		t.Fatalf("RunTask without model: unexpected error: %v", err)
	}
	// Without model, fakeclaude echoes input with only the isolation-flags prefix.
	if result.Output != "iso:1|hello" {
		t.Errorf("expected output %q, got %q", "iso:1|hello", result.Output)
	}
}

// TestProbeModel_ValidModel verifies that ProbeModel returns nil when the model
// is valid (spec: Adapter exposes ProbeModel / valid model succeeds).
func TestProbeModel_ValidModel(t *testing.T) {
	bin := helperBinary(t)
	// model="good" → fakeclaude exits 0 (no special behaviour for "good").
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "good", "")

	ctx := context.Background()
	if err := adapter.ProbeModel(ctx); err != nil {
		t.Errorf("ProbeModel with valid model: expected nil error, got: %v", err)
	}
}

// TestProbeModel_BadModel verifies that ProbeModel returns a non-nil error
// containing stderr text when the model is invalid
// (threat (d); spec: Invalid model — probe fails with stderr).
func TestProbeModel_BadModel(t *testing.T) {
	bin := helperBinary(t)
	// model="badmodel" → fakeclaude writes "issue with the selected model" to stderr and exits 1.
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "badmodel", "")

	ctx := context.Background()
	err := adapter.ProbeModel(ctx)
	if err == nil {
		t.Fatal("expected non-nil error for bad model, got nil")
	}
	if !strings.Contains(err.Error(), "invalid model") {
		t.Errorf("error must contain \"invalid model\"; got: %v", err)
	}
	if !strings.Contains(err.Error(), "issue with the selected model") {
		t.Errorf("error must contain stderr text from CLI; got: %v", err)
	}
}

// TestProbeModel_Deadline verifies that ProbeModel returns a deadline-exceeded
// error when the context deadline elapses before the probe responds
// (threat (b); spec: Probe exceeds 15-second deadline).
func TestProbeModel_Deadline(t *testing.T) {
	bin := helperBinary(t)
	// model="hangmodel" → fakeclaude sleeps indefinitely, simulating an unresponsive model.
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "hangmodel", "")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	err := adapter.ProbeModel(ctx)
	if err == nil {
		t.Fatal("expected deadline error from probe, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded); got: %v", err)
	}
}

// ---- Phase 3: MCP tool use and action intent emission (spec: provider-action-intent-emission) ----

// TestClaudeAdapter_MCPToolCall_PopulatesActionIntents proves the real adapter path is wired:
// a fake claude binary performs an actual MCP tools/call over HTTP against a running server,
// and RunTask returns the intent recorded by the server's sink — not a hand-constructed fixture.
func TestClaudeAdapter_MCPToolCall_PopulatesActionIntents(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "")

	t.Setenv("FAKECLAUDE_CALL_MCP", "1")

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

// TestClaudeAdapter_NoToolCall_EmptyIntents verifies that a completed invocation which
// reached the MCP endpoint and simply called no tool yields empty ActionIntents and a nil
// error (not an erroneous result). This is the regression guard for the never-contacted
// check below: that check must only fire when the endpoint was never reached, never on
// this healthy outcome.
func TestClaudeAdapter_NoToolCall_EmptyIntents(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	// The CLI completes the MCP handshake but calls no tool.
	t.Setenv("FAKECLAUDE_CONNECT_MCP_ONLY", "1")

	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "")

	result, err := adapter.RunTask(context.Background(), "task-no-mcp", "hello")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(result.ActionIntents) != 0 {
		t.Errorf("expected empty ActionIntents, got %+v", result.ActionIntents)
	}
}

// TestClaudeAdapter_NeverContactedMCP_FailsLoudInsteadOfSilentSuccess is RED for the
// finding that a CLI which never reaches the MCP endpoint at all produces a result
// byte-identical to the healthy "the agent chose not to call a tool" case: Output set,
// nil error, empty ActionIntents.
//
// MCPHealthCheck cannot catch this: the server here is alive and healthy, it was simply
// never contacted (config shape ignored by the CLI, handshake failure, bearer rejected,
// subprocess killed before the call). The registry already knows the minted token was
// never presented, so the adapter must consult it rather than reporting success for an
// invocation whose tool calls could not have been recorded.
func TestClaudeAdapter_NeverContactedMCP_FailsLoudInsteadOfSilentSuccess(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "")

	// Neither FAKECLAUDE_CALL_MCP nor FAKECLAUDE_CONNECT_MCP_ONLY is set: the subprocess
	// exits successfully without ever touching the MCP endpoint.
	_, err := adapter.RunTask(context.Background(), "task-never-contacted", "hello")
	if err == nil {
		t.Fatal("expected RunTask to fail loudly when the CLI never contacted the MCP endpoint, got nil error (silent false success)")
	}
	if !strings.Contains(err.Error(), srv.Addr()) {
		t.Errorf("expected the error to name the MCP server address %q so an operator can act, got: %v", srv.Addr(), err)
	}
	if !strings.Contains(err.Error(), "task-never-contacted") {
		t.Errorf("expected the error to name the task ID so an operator can act, got: %v", err)
	}
}

// TestClaudeAdapter_TokenAbsentFromArgv verifies threat-matrix case "Subprocess argv":
// the bearer token is delivered in an HTTP header (via the ephemeral MCP config file),
// never as a literal subprocess argv element.
func TestClaudeAdapter_TokenAbsentFromArgv(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)
	spy := &spyMinter{reg: registry}

	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKECLAUDE_DUMP_ARGV", "1")
	t.Setenv("FAKECLAUDE_ARGV_FILE", argvFile)
	// Model a CLI that actually reaches the MCP endpoint, so this test stays focused on
	// argv contents rather than tripping the never-contacted check.
	t.Setenv("FAKECLAUDE_CONNECT_MCP_ONLY", "1")

	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   spy,
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "")

	if _, err := adapter.RunTask(context.Background(), "task-argv-check", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	if len(spy.tokens) != 1 {
		t.Fatalf("expected exactly 1 minted token, got %d", len(spy.tokens))
	}
	token := spy.tokens[0]

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv dump: %v", err)
	}
	if strings.Contains(string(raw), token) {
		t.Errorf("bearer token leaked into subprocess argv: dump=%q", string(raw))
	}

	// Positive assertion (spec: "Claude adapter configures MCP ephemerally"): the adapter
	// must actually pass --mcp-config and --strict-mcp-config when MCP is wired. Without
	// this, a regression that silently dropped --strict-mcp-config (weakening the CLI to
	// also read the user's real, persisted MCP config) would go undetected by this test.
	if !strings.Contains(string(raw), "--mcp-config") {
		t.Errorf("expected argv to contain --mcp-config; dump=%q", string(raw))
	}
	if !strings.Contains(string(raw), "--strict-mcp-config") {
		t.Errorf("expected argv to contain --strict-mcp-config; dump=%q", string(raw))
	}
}

// TestClaudeAdapter_NoJsonSchema_InArgv verifies spec "Claude adapter does not request
// --json-schema": that flag ends the turn and cannot carry free-form Output alongside intents.
func TestClaudeAdapter_NoJsonSchema_InArgv(t *testing.T) {
	bin := helperBinary(t)

	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKECLAUDE_DUMP_ARGV", "1")
	t.Setenv("FAKECLAUDE_ARGV_FILE", argvFile)

	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	if _, err := adapter.RunTask(context.Background(), "task-no-schema", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv dump: %v", err)
	}
	if strings.Contains(string(raw), "--json-schema") {
		t.Errorf("argv must never contain --json-schema: %q", string(raw))
	}
}

// TestClaudeAdapter_MintThenFail_ReleasesEntryAndLifetime: RED for (B) registry leak and (E) ceiling.
func TestClaudeAdapter_MintThenFail_ReleasesEntryAndLifetime(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)
	spy := &spyMinter{reg: registry}
	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit: 1 << 20, MCPRegistry: spy, MCPServerAddr: srv.Addr(), Tenant: "acme", AgentName: "ceo",
	}, "", "")

	before := time.Now()
	if _, err := adapter.RunTask(context.Background(), "task-mint-fail", "fail"); err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if len(spy.tokens) != 1 {
		t.Fatalf("expected exactly 1 minted token, got %d", len(spy.tokens))
	}
	if _, err := registry.Resolve(spy.tokens[0], "acme"); err == nil {
		t.Error("expected registry entry released after a failed RunTask, but Resolve still succeeded (leak)")
	}
	if spy.exp.Sub(before) < time.Hour {
		t.Errorf("token lifetime too short for a long-running invocation: only %v from mint", spy.exp.Sub(before))
	}
}

// TestClaudeAdapter_EphemeralConfig_TempFileRemoved verifies spec "Claude adapter configures
// MCP ephemerally": the temp MCP config file is removed after the subprocess exits.
func TestClaudeAdapter_EphemeralConfig_TempFileRemoved(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	dumpFile := filepath.Join(t.TempDir(), "mcp-config-path.txt")
	t.Setenv("FAKECLAUDE_DUMP_MCP_CONFIG_PATH", dumpFile)
	// Model a CLI that actually reaches the MCP endpoint, so this test stays focused on
	// temp-file cleanup rather than tripping the never-contacted check.
	t.Setenv("FAKECLAUDE_CONNECT_MCP_ONLY", "1")

	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "")

	if _, err := adapter.RunTask(context.Background(), "task-ephemeral", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	raw, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatalf("read mcp config path dump: %v", err)
	}
	cfgPath := strings.TrimSpace(string(raw))
	if cfgPath == "" {
		t.Fatal("fakeclaude did not report the MCP config path it saw")
	}
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Errorf("expected ephemeral MCP config temp file to be removed after RunTask, stat err=%v", statErr)
	}
}

// TestClaudeAdapter_DeadMCPServer_FailsLoudInsteadOfSilentSuccess is RED for the finding
// that a dead MCP server produced a silent false success: with no health check, RunTask
// would spawn the subprocess, find no tool calls were made (because the server that would
// have served them is dead), and return a nil error with empty ActionIntents — identical
// to the ordinary "the agent chose not to call a tool" outcome. An operator has no way to
// tell those two situations apart.
//
// The health check is a plain func() error (in production, wire.go passes the real
// transport/mcp Server.Err() method value — see transport/mcp/server_internal_test.go's
// TestServer_AbnormalDeath_IsObservable for proof that Err() itself reports the death). This
// test only needs to prove the adapter *consults and obeys* whatever MCPHealthCheck reports:
// a dead server must surface as an explicit RunTask error, and — because the check runs
// before the subprocess starts — the subprocess must never even be invoked (asserted via the
// FAKECLAUDE_DUMP_ARGV hook: no dump file means fakeclaude never ran).
func TestClaudeAdapter_DeadMCPServer_FailsLoudInsteadOfSilentSuccess(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKECLAUDE_DUMP_ARGV", "1")
	t.Setenv("FAKECLAUDE_ARGV_FILE", argvFile)

	simulatedDeath := errors.New("simulated mcp server death")
	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:    1 << 20,
		MCPRegistry:    &registryMinter{reg: registry},
		MCPServerAddr:  srv.Addr(),
		Tenant:         "acme",
		AgentName:      "ceo",
		MCPHealthCheck: func() error { return simulatedDeath },
	}, "", "")

	_, err := adapter.RunTask(context.Background(), "task-dead-mcp", "hello")
	if err == nil {
		t.Fatal("expected RunTask to fail loudly when the MCP server is dead, got nil error (silent false success)")
	}
	if !errors.Is(err, simulatedDeath) {
		t.Errorf("expected RunTask error to wrap the health check error, got: %v", err)
	}

	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Errorf("expected the claude subprocess to never be invoked when the MCP server is dead, but argv dump exists (stat err=%v)", statErr)
	}
}

// containsArg reports whether want appears as an exact element of argv.
func containsArg(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

// TestClaudeAdapter_MCPFlags_NeverCombinedWithDisablingFlag is the falsifiable JD-2
// regression test for the finding that --safe-mode disables MCP servers on the real CLI
// (per `claude --help`, verified against the installed 2.1.268 binary: --safe-mode disables
// "CLAUDE.md, skills, plugins, hooks, MCP servers, custom commands and agents, output
// styles, workflows, custom themes, keybindings" as one bundle), so combining it with
// --mcp-config/--strict-mcp-config made the MCP endpoint permanently unreachable and every
// MCP-wired RunTask call fail via the never-contacted guard.
//
// It reads the real subprocess argv (FAKECLAUDE_DUMP_ARGV), independent of fakeclaude's own
// flag-handling logic, and asserts directly against argv content — not against fakeclaude's
// self-reported behavior — so it stays falsifiable against a regression that reintroduces
// --safe-mode (or swaps in an equally MCP-disabling flag such as --bare) alongside the MCP
// flags. It also asserts the isolation substitute (--setting-sources ""/
// --disable-slash-commands) is present, since dropping --safe-mode entirely without any
// replacement would be a silent, undocumented isolation regression.
func TestClaudeAdapter_MCPFlags_NeverCombinedWithDisablingFlag(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)

	argvFile := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKECLAUDE_DUMP_ARGV", "1")
	t.Setenv("FAKECLAUDE_ARGV_FILE", argvFile)
	// Model a CLI that actually reaches the MCP endpoint, so this test stays focused on
	// argv contents rather than tripping the never-contacted check.
	t.Setenv("FAKECLAUDE_CONNECT_MCP_ONLY", "1")

	adapter := claudecode.New(bin, claudecode.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   &registryMinter{reg: registry},
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "")

	if _, err := adapter.RunTask(context.Background(), "task-mcp-flags", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("read argv dump: %v", err)
	}
	argv := strings.Split(string(raw), "\n")

	if !containsArg(argv, "--mcp-config") || !containsArg(argv, "--strict-mcp-config") {
		t.Fatalf("expected --mcp-config and --strict-mcp-config in argv when MCP is wired; argv=%v", argv)
	}

	for _, disabling := range []string{"--safe-mode", "--bare"} {
		if containsArg(argv, disabling) {
			t.Errorf("argv combines MCP flags (--mcp-config/--strict-mcp-config) with %q, which disables MCP servers on the real CLI — MCP would never be reachable; argv=%v", disabling, argv)
		}
	}

	if !containsArg(argv, "--disable-slash-commands") {
		t.Errorf("expected --disable-slash-commands (isolation substitute for --safe-mode) in argv; argv=%v", argv)
	}
	foundEmptySettingSources := false
	for i, a := range argv {
		if a == "--setting-sources" && i+1 < len(argv) && argv[i+1] == "" {
			foundEmptySettingSources = true
		}
	}
	if !foundEmptySettingSources {
		t.Errorf("expected --setting-sources \"\" (isolation substitute for --safe-mode) in argv; argv=%v", argv)
	}
}
