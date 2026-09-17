// Package claudecode provides the Claude Code adapter — the first concrete implementation
// of port.Provider. It spawns an ephemeral claude CLI process per invocation via os/exec
// with an argv slice (never sh -c), using -p for non-interactive output. It enforces ctx
// deadlines and parses output into framework types before returning. Raw process output
// never crosses the port boundary.
//
// Import policy: imported only by cmd/company (the composition root). core/ must never
// import this package.
package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// TruncationMarker is prepended to output when it is truncated by the size cap (task 5.3).
const TruncationMarker = "[output truncated]"

// defaultOutputLimit is the maximum bytes read from a claude process before truncation.
const defaultOutputLimit int64 = 1 << 20 // 1 MiB

// defaultTokenGrace extends the invocation deadline before it is used as the MCP
// token expiry, so the token remains valid for the brief window between process
// exit and the adapter draining it.
const defaultTokenGrace = 30 * time.Second

// defaultMintTimeout: token lifetime with no ctx deadline. Drain releases the entry
// regardless, so this only avoids mid-task 401s from the prior, arbitrary 5min ceiling.
const defaultMintTimeout = 24 * time.Hour

// Drainer is satisfied by a live MCP invocation handle: after the subprocess exits,
// Drain releases the invocation and returns any action intents the MCP server's sink
// recorded for it. Idempotent — a second Drain call returns nil without panicking.
type Drainer interface {
	Drain() []port.ActionIntent
	// Contacted reports whether the agent CLI ever reached the MCP server with this
	// invocation's bearer token. An empty sink alone cannot distinguish "the agent chose
	// not to call a tool" from "the agent never reached the MCP server at all".
	Contacted() bool
}

// TokenMinter mints a per-invocation MCP bearer token bound to {tenant, agent, taskID}.
//
// TokenMinter and Drainer are exported — not literally unexported "tokenMinter"/"drainer"
// as originally sketched — because Go requires a method's declared return type to be
// IDENTICAL to the interface's declared return type for interface satisfaction; there is
// no covariant return typing. transport/mcp's *Registry.Mint returns (string, *mcp.Handle),
// not (string, Drainer), so a package outside this one (cmd/company/wire.go) must supply a
// small wrapper whose Mint method literally declares `(string, Drainer)` as its return
// type. An unexported type cannot be named outside its declaring package, so the
// interface must be exported for that wrapper to spell it — while this adapter package
// still never imports transport/mcp, preserving the design's decoupling intent.
type TokenMinter interface {
	Mint(tenant, agent, taskID string, exp time.Time) (string, Drainer)
}

// mcpConfigFile is the ephemeral MCP config JSON written for --mcp-config.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// Options configures the adapter. Zero value is valid (uses defaults, no MCP wiring).
type Options struct {
	// OutputLimit caps the number of bytes read from the child process stdout.
	// When zero, defaultOutputLimit is used.
	OutputLimit int64
	// MCPRegistry mints per-invocation MCP bearer tokens. Nil disables MCP wiring
	// entirely: no --mcp-config flag is added, and ActionIntents is always empty.
	MCPRegistry TokenMinter
	// MCPServerAddr is the loopback address of the running MCP server (e.g. "127.0.0.1:54321").
	// Required when MCPRegistry is non-nil.
	MCPServerAddr string
	// Tenant is passed to MCPRegistry.Mint and must match the MCP server's configured tenant.
	Tenant string
	// AgentName identifies this adapter's agent to MCPRegistry.Mint (audit/logging only).
	AgentName string
	// PolicyActionKinds is returned by Capabilities().ActionKinds.
	PolicyActionKinds []string
	// ContextBudget is returned by Capabilities().ContextBudget.
	ContextBudget int
	TokenLifetime time.Duration // overrides defaultMintTimeout when non-zero (test-only knob)
	// MCPHealthCheck reports the MCP server's health when non-nil (production: the running
	// transport/mcp Server's Err() method value). RunTask consults it before spawning the
	// subprocess whenever MCPRegistry is configured: a non-nil result aborts the invocation
	// with an explicit error instead of running an agent whose tool calls could never
	// reach a live server, which would otherwise return a false "success" with empty
	// ActionIntents indistinguishable from "the agent made no tool calls".
	MCPHealthCheck func() error
}

// Adapter implements port.Provider by running an ephemeral claude CLI process per task.
// It is stateless: each RunTask call creates a fresh exec.Cmd with no shared state.
type Adapter struct {
	claudeBin         string
	limit             int64
	model             string
	systemPromptPath  string // absolute path; empty → flag omitted
	mcpRegistry       TokenMinter
	mcpServerAddr     string
	tenant            string
	agentName         string
	policyActionKinds []string
	contextBudget     int
	tokenLifetime     time.Duration
	mcpHealthCheck    func() error
}

// New returns an Adapter that invokes claudeBin as the claude CLI.
// claudeBin must be a path to the claude executable (or a test double).
// model is optional; when non-empty it is passed as --model <model>.
// systemPromptPath is optional; when non-empty it is passed as --system-prompt-file <path>.
// --no-session-persistence, --setting-sources "", and --disable-slash-commands are always
// included unconditionally (see RunTask's doc comment for why --safe-mode is deliberately
// NOT one of them).
func New(claudeBin string, opts Options, model string, systemPromptPath string) *Adapter {
	limit := opts.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	lifetime := opts.TokenLifetime
	if lifetime <= 0 {
		lifetime = defaultMintTimeout
	}
	return &Adapter{
		claudeBin:         claudeBin,
		limit:             limit,
		model:             model,
		systemPromptPath:  systemPromptPath,
		mcpRegistry:       opts.MCPRegistry,
		mcpServerAddr:     opts.MCPServerAddr,
		tenant:            opts.Tenant,
		agentName:         opts.AgentName,
		policyActionKinds: opts.PolicyActionKinds,
		contextBudget:     opts.ContextBudget,
		tokenLifetime:     lifetime,
		mcpHealthCheck:    opts.MCPHealthCheck,
	}
}

// RunTask implements port.Provider.RunTask.
// It spawns a fresh claude process with -p (non-interactive mode), passes input as
// a positional argv argument, reads stdout up to the size cap, and returns a parsed
// ProviderResult. Non-zero exit → error. ctx deadline kills the child.
//
// When mcpRegistry is configured, RunTask mints a per-invocation MCP bearer token,
// writes an ephemeral --mcp-config file (0600, removed via defer before returning),
// and drains the token's recorded ActionIntents after the subprocess exits. The
// bearer token is delivered only inside that config file's JSON, never on argv.
func (a *Adapter) RunTask(ctx context.Context, taskID string, input string) (port.ProviderResult, error) {
	// argv-as-slice: input is passed as a literal argument, never interpolated into a shell string.
	// This is the primary guard against argument injection (threat-matrix case a).
	// -p requests non-interactive mode: claude processes the prompt and prints output to stdout,
	// then exits. Without -p, claude starts an interactive REPL which blocks forever.
	//
	// --safe-mode is deliberately NOT passed. Per `claude --help` (verified against the
	// installed 2.1.268 CLI), --safe-mode disables "CLAUDE.md, skills, plugins, hooks, MCP
	// servers, custom commands and agents, output styles, workflows, custom themes,
	// keybindings" as one bundle — MCP servers are explicitly in that disabled set, so
	// combining --safe-mode with --mcp-config/--strict-mcp-config below made the MCP
	// endpoint unreachable, which the never-contacted guard at the end of this function
	// then turned into a hard failure on every MCP-wired invocation.
	//
	// No flag combination in the installed CLI replicates --safe-mode's full isolation
	// while leaving MCP enabled: --bare disables a similar bundle but also restricts
	// Anthropic auth to ANTHROPIC_API_KEY/apiKeyHelper only (OAuth and keychain are never
	// read), which would break the adapter's existing auth story; --restricted strips
	// Bash/code-execution tools the agents need. --setting-sources "" and
	// --disable-slash-commands are the closest available substitute: they skip
	// user/project/local settings.json (where hooks and permission overrides normally
	// live) and the user's own installed skills, respectively. --strict-mcp-config
	// (appended below when MCP is wired) already restricts MCP to only this adapter's own
	// --mcp-config, so ambient user MCP servers stay excluded either way.
	//
	// KNOWN ISOLATION REGRESSION (decision item — see AGENTS.md "Provider CLI
	// compatibility"): CLAUDE.md auto-discovery, plugins, custom commands/agents, and
	// output styles/workflows/themes/keybindings are NOT covered by any known flag and now
	// load normally for every invocation. This is an explicit, accepted tradeoff to make
	// MCP reachable at all, not an oversight.
	//
	// --no-session-persistence prevents writing session transcripts to disk.
	// A fresh exec.Cmd per call → stateless across invocations (task 5.5).
	args := []string{"-p", "--no-session-persistence", "--setting-sources", "", "--disable-slash-commands"}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	if a.systemPromptPath != "" {
		args = append(args, "--system-prompt-file", a.systemPromptPath)
	}

	var handle Drainer
	var mcpConfigPath string
	if a.mcpRegistry != nil {
		// Consult the MCP server's health before ever spawning the subprocess. Checking
		// after the fact (post-Drain) would still return a nil error with empty
		// ActionIntents whenever the agent's own turn happened not to call a tool —
		// exactly the false "success" this check exists to eliminate.
		if a.mcpHealthCheck != nil {
			if healthErr := a.mcpHealthCheck(); healthErr != nil {
				return port.ProviderResult{}, fmt.Errorf("claudecode: mcp server unavailable, refusing to run task without a working MCP endpoint: %w", healthErr)
			}
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(a.tokenLifetime)
		}
		token, h := a.mcpRegistry.Mint(a.tenant, a.agentName, taskID, deadline.Add(defaultTokenGrace))
		handle = h
		defer h.Drain() // release on every return path; idempotent

		path, err := writeEphemeralMCPConfig(a.mcpServerAddr, token)
		if err != nil {
			return port.ProviderResult{}, fmt.Errorf("claudecode: mcp config: %w", err)
		}
		mcpConfigPath = path
		defer os.Remove(mcpConfigPath) //nolint:errcheck // best-effort cleanup; temp file, no persisted state

		args = append(args, "--mcp-config", mcpConfigPath, "--strict-mcp-config")
	}
	// --output-format stream-json --verbose is always requested: claude's structured stream
	// is parsed solely to extract text (never tool_use events — the spec forbids that; see
	// spec: "ActionIntents Are Collected From the Sink, Never From Stream Parsing"). Note
	// --json-schema is deliberately never used: it ends the turn and cannot carry free-form
	// Output alongside intents (spec: "Claude adapter does not request --json-schema").
	args = append(args, "--output-format", "stream-json", "--verbose")

	args = append(args, input)

	cmd := exec.CommandContext(ctx, a.claudeBin, args...) //nolint:gosec // argv slice, no shell
	if mcpConfigPath != "" {
		// FAKECLAUDE_MCP_CONFIG is consumed only by the test double; the real claude CLI
		// reads the config via --mcp-config. Subprocess-scoped only — never os.Setenv.
		cmd.Env = append(os.Environ(), "FAKECLAUDE_MCP_CONFIG="+mcpConfigPath)
	}

	// Capture stderr independently of StdoutPipe. cmd.Stderr and StdoutPipe are
	// orthogonal: setting Stderr does not interfere with the LimitReader drain pattern.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	// Use StdoutPipe so we control reading. This lets us read only up to the size cap
	// and then drain the remainder via io.Discard in a goroutine, preventing EPIPE.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return port.ProviderResult{}, fmt.Errorf("claudecode: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return port.ProviderResult{}, fmt.Errorf("claudecode: start: %w", err)
	}

	// Task 5.3: read up to limit bytes via io.LimitReader. After limit bytes, switch to
	// draining via io.Discard so the child can write without blocking on a full pipe.
	lr := io.LimitReader(stdout, a.limit)
	var buf bytes.Buffer
	n, readErr := io.Copy(&buf, lr)

	// Drain any remaining output so the child is not blocked writing to a full pipe.
	// We do not care about this content — only the first `limit` bytes matter.
	io.Copy(io.Discard, stdout) //nolint:errcheck // drain only; error irrelevant

	// Wait for exit. exec.CommandContext sends SIGKILL when ctx is done (threat-matrix case b).
	waitErr := cmd.Wait()

	// Prioritise context cancellation: if ctx is done the kill error is expected.
	if ctx.Err() != nil {
		return port.ProviderResult{}, fmt.Errorf("claudecode: deadline exceeded: %w", ctx.Err())
	}
	if waitErr != nil {
		// Non-zero exit → failure outcome (threat-matrix case d). Include captured
		// stderr and stdout so callers receive actionable diagnostics rather than
		// opaque exit codes. Some CLI errors land on stdout, not stderr.
		return port.ProviderResult{}, fmt.Errorf("claudecode: %w\nstderr: %s\nstdout: %s", waitErr, stderrBuf.String(), buf.String())
	}
	if readErr != nil {
		return port.ProviderResult{}, fmt.Errorf("claudecode: read output: %w", readErr)
	}

	var output string
	if n >= a.limit {
		// Output was capped mid-stream — the raw bytes may not be valid NDJSON.
		// Fall back to the legacy raw-truncation behaviour rather than failing to parse.
		output = parseOutput(buf.Bytes(), n, a.limit)
	} else {
		output = parseStreamText(buf.Bytes())
		if output == "" {
			// Not every claude invocation emits NDJSON — e.g. a bare --version flag
			// anywhere in argv short-circuits to a plain version string regardless of
			// --output-format. Fall back to the raw trimmed text rather than losing it.
			if raw := strings.TrimRight(buf.String(), "\n"); raw != "" {
				output = raw
			}
		}
	}

	result := port.ProviderResult{Output: output}
	if handle != nil {
		// Sink is authoritative: ActionIntents come only from the MCP server's sink,
		// never from parsing tool_use events out of the stream above.
		result.ActionIntents = handle.Drain()

		// An empty sink is ambiguous on its own. MCPHealthCheck above only catches a
		// server that died; a server that is alive but was never reached (config shape
		// ignored by the CLI, handshake failure, bearer rejected, subprocess killed
		// before the call) leaves no signal there. The registry does know whether the
		// minted token was ever presented, so consult it rather than reporting a
		// success that is byte-identical to "the agent made no tool calls".
		if len(result.ActionIntents) == 0 && !handle.Contacted() {
			return port.ProviderResult{}, fmt.Errorf("claudecode: mcp endpoint %s was never contacted by the CLI for task %q; refusing to report success for an invocation whose tool calls could not have been recorded", a.mcpServerAddr, taskID)
		}
	}
	return result, nil
}

// parseOutput converts raw bytes to a string, prepending TruncationMarker when the
// byte count equals the limit (meaning io.LimitReader may have stopped early).
func parseOutput(raw []byte, n, limit int64) string {
	text := strings.TrimRight(string(raw), "\n")
	if n >= limit {
		// At least limit bytes were available — output was capped.
		return TruncationMarker + " " + text
	}
	return text
}

// streamEvent is a permissive envelope for the claude --output-format stream-json lines
// this adapter cares about: either a bare {"type":"text","text":"..."} line, or an
// {"type":"assistant","message":{"content":[{"type":"text","text":"..."}]}} line. Only
// text content is ever extracted — tool_use blocks are intentionally never inspected
// (spec: "ActionIntents Are Collected From the Sink, Never From Stream Parsing").
type streamEvent struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	Message *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message,omitempty"`
}

// parseStreamText extracts and concatenates all text content from an NDJSON stream of
// streamEvent lines. Lines that fail to parse are skipped rather than aborting the whole
// result — a single malformed line should not erase everything else the CLI produced.
func parseStreamText(raw []byte) string {
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "text":
			sb.WriteString(ev.Text)
		case "assistant":
			if ev.Message != nil {
				for _, c := range ev.Message.Content {
					if c.Type == "text" {
						sb.WriteString(c.Text)
					}
				}
			}
		}
	}
	return sb.String()
}

// writeEphemeralMCPConfig writes a 0600 temp file describing the running MCP server and
// per-invocation bearer token, in the format claude's --mcp-config expects:
//
//	{"mcpServers": {"framework": {"type": "http", "url": "http://<addr>",
//	  "headers": {"Authorization": "Bearer <token>"}}}}
//
// The caller is responsible for removing the returned path once the subprocess exits.
func writeEphemeralMCPConfig(addr, token string) (string, error) {
	cfg := mcpConfigFile{
		MCPServers: map[string]mcpServerEntry{
			"framework": {
				Type: "http",
				URL:  "http://" + addr,
				Headers: map[string]string{
					"Authorization": "Bearer " + token,
				},
			},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}

	f, err := os.CreateTemp("", "mcp-config-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	path := f.Name()

	if _, err := f.Write(data); err != nil {
		f.Close() //nolint:errcheck // already failing; original error takes priority
		os.Remove(path)
		return "", fmt.Errorf("write: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("close: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		os.Remove(path)
		return "", fmt.Errorf("chmod: %w", err)
	}
	return path, nil
}

// ProbeModel verifies the configured model is recognised by the CLI without
// making an API call. It passes an empty prompt ("") which forces the CLI to
// validate the model and exit immediately (~1s). An invalid model produces
// "issue with the selected model" on stderr; a valid model produces "Input
// must be provided". Both exit non-zero — we distinguish them by stderr content.
// ProbeModel satisfies the unexported modelProber interface in cmd/company/wire.go.
func (a *Adapter) ProbeModel(ctx context.Context) error {
	// Empty prompt: CLI validates model then exits 1 without an API call.
	// Invalid model → "issue with the selected model" on stderr.
	// Valid model   → "Input must be provided" on stderr.
	args := []string{"-p"}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	args = append(args, "")

	cmd := exec.CommandContext(ctx, a.claudeBin, args...) //nolint:gosec // argv slice, no shell

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	cmd.Stdout = io.Discard

	_ = cmd.Run() // always exits non-zero with empty prompt

	if ctx.Err() != nil {
		return fmt.Errorf("claudecode: probe deadline exceeded: %w", ctx.Err())
	}

	stderr := stderrBuf.String()
	if strings.Contains(stderr, "issue with the selected model") ||
		strings.Contains(stderr, "isn't described by this version") {
		return fmt.Errorf("claudecode: invalid model %q\nstderr: %s", a.model, stderr)
	}
	// "Input must be provided" or similar → model is valid, CLI just rejected the empty prompt.
	return nil
}

// ---- port.Provider stub methods (A2A network client side) -------------------
// The A2A client methods (Complete, CompleteError, SendMessage, etc.) are implemented by
// transport/a2a, not by this adapter. This adapter only implements RunTask (local execution).
// The composition root (cmd/company) wires the full port.Provider by composing this adapter
// with transport/a2a. These stubs satisfy the interface so the package compiles and contract
// tests can exercise RunTask in isolation.
//
// ponytail: stubs return errNotImplemented — clear failure rather than silent wrong behavior.

var errNotImplemented = fmt.Errorf("claudecode: A2A client methods are provided by transport/a2a, not this adapter")

// Capabilities returns the ContextBudget and ActionKinds configured via Options
// at construction time (see New).
func (a *Adapter) Capabilities() port.ProviderCapabilities {
	return port.ProviderCapabilities{ContextBudget: a.contextBudget, ActionKinds: a.policyActionKinds}
}

func (a *Adapter) Complete(_ string, _ port.TaskResult) error { return errNotImplemented }
func (a *Adapter) CompleteError(_ string, _ error) error      { return errNotImplemented }
func (a *Adapter) SendMessage(_ context.Context, _ address.A2AAddress, _ string, _ bool) (string, error) {
	return "", errNotImplemented
}
func (a *Adapter) SendMessageStream(_ context.Context, _ address.A2AAddress, _ string) (<-chan port.StreamEvent, error) {
	return nil, errNotImplemented
}
func (a *Adapter) ResolveAgent(_ context.Context, _ string) (address.A2AAddress, error) {
	return "", errNotImplemented
}
func (a *Adapter) SendTask(_ context.Context, _ address.A2AAddress, _ string, _ map[string]any, _ *port.TaskOptions) (string, error) {
	return "", errNotImplemented
}

// compile-time check: Adapter must satisfy port.Provider.
var _ port.Provider = (*Adapter)(nil)
