// Package pi_test covers the pi adapter against a helper binary (testdata/fakepi) that
// emits the real pi 1.2.0 `--mode json` JSONL envelope, so no pi installation is needed.
//
// Spec coverage: S1 (port.Provider), S3 (isolation flags, --tools allowlist), S5 (ephemeral
// token/extension delivery), S6 (stdin input, system prompt flag), S7 (failure detection),
// S8 (output extraction, raw fallback, truncation), S9 (ProbeModel).
package pi_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/pi"
	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

// registryMinter adapts *transportmcp.Registry to pi.TokenMinter (see the claudecode and
// opencode tests for why the exported Drainer/TokenMinter pair is needed).
type registryMinter struct {
	reg *transportmcp.Registry
}

func (m *registryMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, pi.Drainer) {
	return m.reg.Mint(tenant, agent, taskID, exp)
}

// spyMinter records every minted token/expiry for direct assertion.
type spyMinter struct {
	reg    *transportmcp.Registry
	tokens []string
	exp    time.Time
}

func (m *spyMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, pi.Drainer) {
	token, handle := m.reg.Mint(tenant, agent, taskID, exp)
	m.tokens = append(m.tokens, token)
	m.exp = exp
	return token, handle
}

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

// helperBinary builds the fakepi binary and returns its path.
func helperBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakepi")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, filepath.Join("testdata", "fakepi", "main.go"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fakepi: %v", err)
	}
	return bin
}

// dumpArgv wires the argv dump hook and returns a reader of the recorded argv.
func dumpArgv(t *testing.T) func() []string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("FAKEPI_ARGV_FILE", f)
	return func() []string {
		t.Helper()
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read argv dump: %v", err)
		}
		return strings.Split(string(raw), "\n")
	}
}

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// valueAfter returns the argument following flag, or "" and false.
func valueAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func newMCPAdapter(bin string, srv *transportmcp.Server, minter pi.TokenMinter) *pi.Adapter {
	return pi.New(bin, pi.Options{
		OutputLimit:   1 << 20,
		MCPRegistry:   minter,
		MCPServerAddr: srv.Addr(),
		Tenant:        "acme",
		AgentName:     "ceo",
	}, "", "", nil)
}

// S3: the isolation flags are present on every invocation, with or without MCP.
func TestRunTask_IsolationFlagsAlwaysPresent(t *testing.T) {
	bin := helperBinary(t)
	argv := dumpArgv(t)
	adapter := pi.New(bin, pi.Options{}, "", "", nil)
	if _, err := adapter.RunTask(context.Background(), "t", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	args := argv()
	for _, flag := range []string{"-p", "-ne", "--no-session", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve", "--offline"} {
		if !contains(args, flag) {
			t.Errorf("expected %s in argv, got %v", flag, args)
		}
	}
	if mode, ok := valueAfter(args, "--mode"); !ok || mode != "json" {
		t.Errorf("expected --mode json, got %v", args)
	}
	// No MCP wired → no tool allowlist, no builtin:mcp.
	if contains(args, "--tools") || contains(args, "builtin:mcp") {
		t.Errorf("did not expect --tools / builtin:mcp without MCP, got %v", args)
	}
}

// S3/S6: model, system prompt and extension flags appear only when configured.
func TestRunTask_OptionalFlagsPresentWhenSet(t *testing.T) {
	bin := helperBinary(t)
	argv := dumpArgv(t)
	adapter := pi.New(bin, pi.Options{}, "openai-codex/gpt-6-luna:high", "/abs/system.md", []string{"/abs/ext.ts", "builtin:foo"})
	if _, err := adapter.RunTask(context.Background(), "t", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	args := argv()
	if v, _ := valueAfter(args, "--model"); v != "openai-codex/gpt-6-luna:high" {
		t.Errorf("--model = %q", v)
	}
	if v, _ := valueAfter(args, "--append-system-prompt"); v != "/abs/system.md" {
		t.Errorf("--append-system-prompt = %q", v)
	}
	var exts []string
	for i, a := range args {
		if a == "-e" && i+1 < len(args) {
			exts = append(exts, args[i+1])
		}
	}
	if strings.Join(exts, ",") != "/abs/ext.ts,builtin:foo" {
		t.Errorf("extensions in order = %v", exts)
	}
}

func TestRunTask_OptionalFlagsAbsentWhenUnset(t *testing.T) {
	bin := helperBinary(t)
	argv := dumpArgv(t)
	adapter := pi.New(bin, pi.Options{}, "", "", nil)
	if _, err := adapter.RunTask(context.Background(), "t", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	args := argv()
	for _, flag := range []string{"--model", "--append-system-prompt", "-e"} {
		if contains(args, flag) {
			t.Errorf("did not expect %s in argv, got %v", flag, args)
		}
	}
}

// S6: input goes on stdin — including inputs above the Linux per-argument ceiling and
// shell metacharacters — and never appears on argv.
func TestRunTask_InputOnStdinNotArgv(t *testing.T) {
	bin := helperBinary(t)
	stdinFile := filepath.Join(t.TempDir(), "stdin.txt")
	t.Setenv("FAKEPI_STDIN_FILE", stdinFile)
	argv := dumpArgv(t)
	adapter := pi.New(bin, pi.Options{OutputLimit: 1 << 20}, "", "", nil)

	cases := map[string]string{
		"metacharacters": "hello; touch /tmp/pwned && $(id) `id` | cat > /dev/null",
		"over argv cap":  strings.Repeat("a", 200_000),
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			result, err := adapter.RunTask(context.Background(), "t", input)
			if err != nil {
				t.Fatalf("RunTask: %v", err)
			}
			got, err := os.ReadFile(stdinFile)
			if err != nil {
				t.Fatalf("read stdin dump: %v", err)
			}
			if string(got) != input {
				t.Errorf("stdin = %d bytes, want %d bytes identical to input", len(got), len(input))
			}
			if result.Output != input {
				t.Errorf("output not the assistant echo of the input (len %d)", len(result.Output))
			}
			for _, a := range argv() {
				if len(a) >= 40 && strings.Contains(input, a) {
					t.Errorf("input leaked onto argv: %.60q", a)
				}
			}
		})
	}
}

// S8: Output is the text blocks of assistant message_end events only.
func TestRunTask_ExtractsOnlyAssistantTextBlocks(t *testing.T) {
	bin := helperBinary(t)
	t.Setenv("FAKEPI_CALL_MCP", "1")
	srv, registry := startTestMCPServer(t)
	result, err := newMCPAdapter(bin, srv, &registryMinter{reg: registry}).RunTask(context.Background(), "t", "DONE")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if result.Output != "DONE" {
		t.Errorf("Output = %q, want exactly the assistant text %q (no thinking, toolCall, toolResult, user)", result.Output, "DONE")
	}
}

// S8: non-event output falls back to the raw trimmed text.
func TestRunTask_RawTextFallback(t *testing.T) {
	bin := helperBinary(t)
	result, err := pi.New(bin, pi.Options{}, "", "", nil).RunTask(context.Background(), "t", "raw-text")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if result.Output != "plain text, not an event stream" {
		t.Errorf("Output = %q", result.Output)
	}
}

// S8: hitting the output cap prepends the truncation marker.
func TestRunTask_OversizedOutputTruncated(t *testing.T) {
	bin := helperBinary(t)
	result, err := pi.New(bin, pi.Options{OutputLimit: 1024}, "", "", nil).RunTask(context.Background(), "t", "large")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if !strings.HasPrefix(result.Output, pi.TruncationMarker) {
		t.Errorf("expected truncation marker prefix, got %.40q", result.Output)
	}
}

// S7/S8: a failure beyond the output cap is still detected (pi's agent_end repeats every
// message, so long sessions exceed the cap).
func TestRunTask_StopReasonErrorBeyondCapIsFailure(t *testing.T) {
	bin := helperBinary(t)
	_, err := pi.New(bin, pi.Options{OutputLimit: 4096}, "", "", nil).RunTask(context.Background(), "t", "big-error")
	if err == nil || !strings.Contains(err.Error(), "late failure beyond the cap") {
		t.Fatalf("expected errorMessage from beyond the cap, got %v", err)
	}
}

// S8: the cap applies to the extracted text, not to the JSONL envelope.
func TestRunTask_LargeAssistantTextTruncated(t *testing.T) {
	bin := helperBinary(t)
	result, err := pi.New(bin, pi.Options{OutputLimit: 4096}, "", "", nil).RunTask(context.Background(), "t", "big-text")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	want := pi.TruncationMarker + " " + strings.Repeat("y", 4096)
	if result.Output != want {
		t.Errorf("Output = %.60q (len %d), want marker + first 4096 text bytes", result.Output, len(result.Output))
	}
}

// S8: an over-long single line is skipped; later lines are still parsed.
func TestRunTask_OverlongLineSkipped(t *testing.T) {
	bin := helperBinary(t)
	result, err := pi.New(bin, pi.Options{}, "", "", nil).RunTask(context.Background(), "t", "long-line")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if result.Output != "AFTER" {
		t.Errorf("Output = %.60q (len %d), want %q", result.Output, len(result.Output), "AFTER")
	}
}

// S7: the critical case — pi exits 0 but the final assistant message is an error.
func TestRunTask_StopReasonErrorIsFailure(t *testing.T) {
	bin := helperBinary(t)
	adapter := pi.New(bin, pi.Options{}, "", "", nil)
	for _, sentinel := range []string{"model-error", "aborted"} {
		t.Run(sentinel, func(t *testing.T) {
			_, err := adapter.RunTask(context.Background(), "t", sentinel)
			if err == nil {
				t.Fatal("expected error for stopReason error/aborted despite exit 0")
			}
			want := "gpt-5.4-mini"
			if sentinel == "aborted" {
				want = "Request was aborted"
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should carry errorMessage %q, got: %v", want, err)
			}
		})
	}
}

// S7: non-zero exit and deadline fail like the sibling adapters.
func TestRunTask_NonZeroExitCarriesStderr(t *testing.T) {
	bin := helperBinary(t)
	adapter := pi.New(bin, pi.Options{}, "", "", nil)
	if _, err := adapter.RunTask(context.Background(), "t", "fail"); err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	_, err := adapter.RunTask(context.Background(), "t", "fail-stderr")
	if err == nil || !strings.Contains(err.Error(), "simulated stderr output") {
		t.Errorf("expected stderr in error, got %v", err)
	}
}

func TestRunTask_HungChildKilledOnDeadline(t *testing.T) {
	bin := helperBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := pi.New(bin, pi.Options{}, "", "", nil).RunTask(ctx, "t", "hang")
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("child was not killed promptly: %v", time.Since(start))
	}
}

// S1/S5: a real MCP tools/call through the extension env contract populates intents.
func TestRunTask_MCPToolCallPopulatesActionIntents(t *testing.T) {
	bin := helperBinary(t)
	t.Setenv("FAKEPI_CALL_MCP", "1")
	srv, registry := startTestMCPServer(t)
	result, err := newMCPAdapter(bin, srv, &registryMinter{reg: registry}).RunTask(context.Background(), "task-mcp", "hello")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(result.ActionIntents) != 1 || result.ActionIntents[0].Kind != "telegram_send" {
		t.Fatalf("expected 1 telegram_send intent, got %+v", result.ActionIntents)
	}
}

func TestRunTask_NoToolCallEmptyIntents(t *testing.T) {
	bin := helperBinary(t)
	t.Setenv("FAKEPI_CONNECT_MCP_ONLY", "1")
	srv, registry := startTestMCPServer(t)
	result, err := newMCPAdapter(bin, srv, &registryMinter{reg: registry}).RunTask(context.Background(), "task-none", "hello")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(result.ActionIntents) != 0 {
		t.Errorf("expected empty intents, got %+v", result.ActionIntents)
	}
}

// S7: alive-but-never-contacted MCP endpoint must fail loudly.
func TestRunTask_NeverContactedMCPFails(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)
	_, err := newMCPAdapter(bin, srv, &registryMinter{reg: registry}).RunTask(context.Background(), "task-never", "hello")
	if err == nil {
		t.Fatal("expected never-contacted error")
	}
	if !strings.Contains(err.Error(), srv.Addr()) || !strings.Contains(err.Error(), "task-never") {
		t.Errorf("error should name address and task, got: %v", err)
	}
}

// S7: a dead MCP server refuses the run before spawning pi.
func TestRunTask_DeadMCPServerRefusesToRun(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)
	argv := dumpArgv(t)
	_ = argv
	argvFile := os.Getenv("FAKEPI_ARGV_FILE")
	dead := errors.New("simulated mcp server death")
	adapter := pi.New(bin, pi.Options{
		MCPRegistry: &registryMinter{reg: registry}, MCPServerAddr: srv.Addr(), Tenant: "acme", AgentName: "ceo",
		MCPHealthCheck: func() error { return dead },
	}, "", "", nil)
	_, err := adapter.RunTask(context.Background(), "t", "hello")
	if !errors.Is(err, dead) {
		t.Fatalf("expected wrapped health error, got %v", err)
	}
	if _, statErr := os.Stat(argvFile); !os.IsNotExist(statErr) {
		t.Errorf("pi must not be spawned when MCP is dead (stat err=%v)", statErr)
	}
}

// S5: the token is subprocess-env only; never on argv nor in the extension file; the
// extension file is 0600 while pi runs and removed afterwards; os env is not mutated.
func TestRunTask_EphemeralTokenAndExtensionFile(t *testing.T) {
	bin := helperBinary(t)
	t.Setenv("FAKEPI_CONNECT_MCP_ONLY", "1")
	argv := dumpArgv(t)
	envFile := filepath.Join(t.TempDir(), "env.txt")
	extFile := filepath.Join(t.TempDir(), "ext.txt")
	t.Setenv("FAKEPI_ENV_FILE", envFile)
	t.Setenv("FAKEPI_EXT_FILE", extFile)

	srv, registry := startTestMCPServer(t)
	spy := &spyMinter{reg: registry}
	if _, err := newMCPAdapter(bin, srv, spy).RunTask(context.Background(), "t", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if len(spy.tokens) != 1 {
		t.Fatalf("expected 1 minted token, got %d", len(spy.tokens))
	}
	token := spy.tokens[0]

	env, _ := os.ReadFile(envFile)
	if string(env) != "http://"+srv.Addr()+"\n"+token {
		t.Errorf("subprocess env = %q, want URL and token", env)
	}
	if os.Getenv("ASF_MCP_TOKEN") != "" || os.Getenv("ASF_MCP_URL") != "" {
		t.Error("adapter leaked ASF_MCP_* into the parent process env")
	}

	args := argv()
	for _, a := range args {
		if strings.Contains(a, token) {
			t.Errorf("token leaked onto argv: %q", a)
		}
	}
	if tools, _ := valueAfter(args, "--tools"); tools != "read,bash,edit,write,mcp__framework__finish_task" {
		t.Errorf("--tools = %q", tools)
	}
	if !contains(args, "builtin:mcp") {
		t.Errorf("expected -e builtin:mcp, got %v", args)
	}

	info, err := os.ReadFile(extFile)
	if err != nil {
		t.Fatalf("extension file was not visible to pi: %v", err)
	}
	parts := strings.SplitN(string(info), "\n", 3)
	if len(parts) != 3 {
		t.Fatalf("bad ext dump: %q", info)
	}
	path, mode, content := parts[0], parts[1], parts[2]
	if mode != "600" {
		t.Errorf("extension file mode = %s, want 600", mode)
	}
	if !strings.HasSuffix(path, ".ts") {
		t.Errorf("extension file %q must end in .ts", path)
	}
	if strings.Contains(content, token) {
		t.Error("token present in extension file content")
	}
	for _, want := range []string{"registerMcpServer", `"framework"`, "ASF_MCP_URL", "ASF_MCP_TOKEN", `exposure: "direct"`} {
		if !strings.Contains(content, want) {
			t.Errorf("extension source missing %q:\n%s", want, content)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("temp extension file should be removed after RunTask (stat err=%v)", err)
	}
}

// Mint-then-fail releases the registry entry; lifetime covers long invocations.
func TestRunTask_MintThenFailReleasesEntry(t *testing.T) {
	bin := helperBinary(t)
	srv, registry := startTestMCPServer(t)
	spy := &spyMinter{reg: registry}
	before := time.Now()
	if _, err := newMCPAdapter(bin, srv, spy).RunTask(context.Background(), "t", "fail"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := registry.Resolve(spy.tokens[0], "acme"); err == nil {
		t.Error("registry entry leaked after failed RunTask")
	}
	if spy.exp.Sub(before) < time.Hour {
		t.Errorf("token lifetime too short: %v", spy.exp.Sub(before))
	}
}

func TestCapabilities(t *testing.T) {
	a := pi.New("pi", pi.Options{PolicyActionKinds: []string{"telegram_send"}, ContextBudget: 7}, "", "", nil)
	caps := a.Capabilities()
	if caps.ContextBudget != 7 || len(caps.ActionKinds) != 1 {
		t.Errorf("caps = %+v", caps)
	}
	var _ port.Provider = a
}

// ---- S9 ProbeModel ---------------------------------------------------------

func TestProbeModel(t *testing.T) {
	bin := helperBinary(t)
	cases := []struct {
		name       string
		model      string
		exts       []string
		wantErrSub string // "" → success
	}{
		{"bare id", "gpt-6-luna", nil, ""},
		{"provider qualified", "openai-codex/gpt-6-luna", nil, ""},
		{"thinking suffix", "openai-codex/gpt-6-luna:high", nil, ""},
		{"bare id thinking suffix", "gpt-6-luna:xhigh", nil, ""},
		{"unknown model", "no-such-model", nil, "no-such-model"},
		{"wrong provider", "anthropic/gpt-6-luna", nil, "anthropic/gpt-6-luna"},
		{"unknown suffix is not stripped", "gpt-6-luna:bogus", nil, "gpt-6-luna:bogus"},
		{"extension-provided model missing without ext", "claude-bridge/claude-haiku-4-5", nil, "claude-haiku-4-5"},
		{"extension-provided model found with ext", "claude-bridge/claude-haiku-4-5", []string{"/opt/pi-claude-bridge"}, ""},
		{"empty model only checks the binary", "", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pi.New(bin, pi.Options{}, tc.model, "", tc.exts).ProbeModel(context.Background())
			if tc.wantErrSub == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) || !strings.Contains(err.Error(), "--list-models") {
				t.Errorf("expected error naming %q and --list-models, got %v", tc.wantErrSub, err)
			}
		})
	}
}

func TestProbeModel_PassesExtensionsAndOffline(t *testing.T) {
	bin := helperBinary(t)
	argv := dumpArgv(t)
	if err := pi.New(bin, pi.Options{}, "gpt-6-luna", "", []string{"builtin:foo"}).ProbeModel(context.Background()); err != nil {
		t.Fatalf("ProbeModel: %v", err)
	}
	args := argv()
	for _, want := range []string{"-ne", "--offline", "--list-models", "builtin:foo"} {
		if !contains(args, want) {
			t.Errorf("expected %s in probe argv, got %v", want, args)
		}
	}
}

func TestProbeModel_EmptyModelRunsVersion(t *testing.T) {
	bin := helperBinary(t)
	argv := dumpArgv(t)
	if err := pi.New(bin, pi.Options{}, "", "", nil).ProbeModel(context.Background()); err != nil {
		t.Fatalf("ProbeModel: %v", err)
	}
	if !contains(argv(), "--version") {
		t.Errorf("expected --version probe, got %v", argv())
	}
}

func TestProbeModel_UnrunnableBinaryFails(t *testing.T) {
	if err := pi.New("/nonexistent/pi", pi.Options{}, "", "", nil).ProbeModel(context.Background()); err == nil {
		t.Error("expected error for unrunnable binary")
	}
	if err := pi.New("/nonexistent/pi", pi.Options{}, "gpt-6-luna", "", nil).ProbeModel(context.Background()); err == nil {
		t.Error("expected error for unrunnable binary with model")
	}
}

func TestProbeModel_DeadlineExceeded(t *testing.T) {
	bin := helperBinary(t)
	t.Setenv("FAKEPI_PROBE_HANG", "1")
	for _, model := range []string{"", "gpt-6-luna"} {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := pi.New(bin, pi.Options{}, model, "", nil).ProbeModel(ctx)
		cancel()
		if err == nil || !strings.Contains(err.Error(), "deadline") {
			t.Errorf("model %q: expected deadline error, got %v", model, err)
		}
	}
}

// S4 (software-factory): agent-declared stdio MCP servers are registered in the same
// temp extension and their tools are allowlisted; everything else stays hidden.
func runWithServers(t *testing.T, servers map[string]pi.MCPServer, withMCP bool) (args []string, ext string) {
	t.Helper()
	bin := helperBinary(t)
	argv := dumpArgv(t)
	extFile := filepath.Join(t.TempDir(), "ext.txt")
	t.Setenv("FAKEPI_EXT_FILE", extFile)
	opts := pi.Options{MCPServers: servers}
	if withMCP {
		t.Setenv("FAKEPI_CONNECT_MCP_ONLY", "1")
		srv, registry := startTestMCPServer(t)
		opts.MCPRegistry = &registryMinter{reg: registry}
		opts.MCPServerAddr = srv.Addr()
		opts.Tenant = "acme"
		opts.AgentName = "ceo"
	}
	a := pi.New(bin, opts, "", "", nil)
	if err := a.ProbeMCP(context.Background()); err != nil {
		t.Fatalf("ProbeMCP: %v", err)
	}
	if _, err := a.RunTask(context.Background(), "t", "hello"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	args = argv()
	if raw, err := os.ReadFile(extFile); err == nil {
		ext = strings.SplitN(string(raw), "\n", 3)[2]
	}
	return args, ext
}

func TestRunTask_MCPServersRegisteredAndAllowlisted(t *testing.T) {
	servers := map[string]pi.MCPServer{
		"chrome-devtools": {Command: fakeMCP(t), Args: []string{"paged"}},
		"alpha":           {Command: fakeMCP(t)},
	}
	args, ext := runWithServers(t, servers, true)

	// Exact names only, never a prefix pattern: framework first, then each server in name
	// order with its tools sorted; "-" and every non [A-Za-z0-9_] char became "_", and a
	// tool whose own name contains "__" stays callable.
	const wantTools = "read,bash,edit,write,mcp__framework__finish_task," +
		"mcp__alpha__do__thing,mcp__alpha__list_files,mcp__alpha__plain," +
		"mcp__chrome_devtools__t1,mcp__chrome_devtools__t2,mcp__chrome_devtools__t3,mcp__chrome_devtools__t4,mcp__chrome_devtools__t5"
	if tools, _ := valueAfter(args, "--tools"); tools != wantTools {
		t.Errorf("--tools = %q\nwant      %q", tools, wantTools)
	}
	if contains(args, "--exclude-tools") {
		t.Errorf("--exclude-tools must be gone: %v", args)
	}
	for _, want := range []string{
		`pi.registerMcpServer("framework"`,
		`pi.registerMcpServer("alpha", {"command":"` + servers["alpha"].Command + `","args":[],"exposure":"direct"});`,
		`pi.registerMcpServer("chrome-devtools", {"command":"` + servers["chrome-devtools"].Command + `","args":["paged"],"exposure":"direct"});`,
	} {
		if !strings.Contains(ext, want) {
			t.Errorf("extension missing %q:\n%s", want, ext)
		}
	}
	if strings.Index(ext, `"alpha"`) > strings.Index(ext, `"chrome-devtools"`) {
		t.Errorf("servers must be registered in sorted name order:\n%s", ext)
	}
}

func TestRunTask_MCPServersWithoutFrameworkMCP(t *testing.T) {
	args, ext := runWithServers(t, map[string]pi.MCPServer{"a": {Command: fakeMCP(t), Args: []string{"collide-free"}}}, false)
	if tools, _ := valueAfter(args, "--tools"); tools != "read,bash,edit,write,mcp__a__only" {
		t.Errorf("--tools = %q", tools)
	}
	if contains(args, "--exclude-tools") {
		t.Errorf("--exclude-tools must be gone: %v", args)
	}
	if contains(args, "builtin:mcp") == false {
		t.Errorf("expected -e builtin:mcp, got %v", args)
	}
	if strings.Contains(ext, "framework") || !strings.Contains(ext, `pi.registerMcpServer("a"`) {
		t.Errorf("unexpected extension:\n%s", ext)
	}
}

// Without mcp_servers the argv and extension content are exactly what they were before.
func TestRunTask_NoMCPServersUnchanged(t *testing.T) {
	for _, servers := range []map[string]pi.MCPServer{nil, {}} {
		args, ext := runWithServers(t, servers, true)
		if tools, _ := valueAfter(args, "--tools"); tools != "read,bash,edit,write,mcp__framework__finish_task" {
			t.Errorf("--tools = %q", tools)
		}
		if contains(args, "--exclude-tools") {
			t.Errorf("--exclude-tools must be gone: %v", args)
		}
		const want = `export default function (pi: any) {
  pi.registerMcpServer("framework", {
    url: process.env.ASF_MCP_URL,
    headers: { Authorization: "Bearer " + process.env.ASF_MCP_TOKEN },
    exposure: "direct",
  });
}
`
		if ext != want {
			t.Errorf("extension changed without mcp_servers:\n%s", ext)
		}
	}
}

func TestRunTask_NoMCPAtAllStaysBare(t *testing.T) {
	args, ext := runWithServers(t, nil, false)
	if contains(args, "--tools") || contains(args, "builtin:mcp") || ext != "" {
		t.Errorf("no MCP and no servers must add nothing: %v ext=%q", args, ext)
	}
}
