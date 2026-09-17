// Package opencode provides the OpenCode adapter — a concrete implementation of
// port.Provider. It spawns an ephemeral opencode CLI process per invocation via
// os/exec with an argv slice (never sh -c), using "run" for non-interactive output.
// It enforces ctx deadlines and parses output into framework types before returning.
// Raw process output never crosses the port boundary.
//
// Import policy: imported only by cmd/company (the composition root). core/ must never
// import this package.
package opencode

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

// TruncationMarker is prepended to output when it is truncated by the size cap.
const TruncationMarker = "[output truncated]"

// defaultOutputLimit is the maximum bytes read from an opencode process before truncation.
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
//
// Deliberately identical in shape to (but a distinct type from) claudecode.Drainer:
// each adapter package defines its own copy to stay decoupled from transport/mcp and
// from each other. See claudecode.TokenMinter's doc comment for why these interfaces
// are exported rather than unexported.
type Drainer interface {
	Drain() []port.ActionIntent
}

// TokenMinter mints a per-invocation MCP bearer token bound to {tenant, agent, taskID}.
type TokenMinter interface {
	Mint(tenant, agent, taskID string, exp time.Time) (string, Drainer)
}

// mcpConfigFile is the MCP config JSON placed in OPENCODE_CONFIG_CONTENT.
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
	// entirely: OPENCODE_CONFIG_CONTENT is never set, and ActionIntents is always empty.
	MCPRegistry TokenMinter
	// MCPServerAddr is the loopback address of the running MCP server (e.g. "127.0.0.1:54321").
	// Required when MCPRegistry is non-nil.
	MCPServerAddr string
	// Tenant is passed to MCPRegistry.Mint and must match the MCP server's configured tenant.
	Tenant string
	// AgentName identifies this adapter's agent to MCPRegistry.Mint. When empty, falls back
	// to the agentName constructor parameter (which also drives --agent).
	AgentName string
	// PolicyActionKinds is returned by Capabilities().ActionKinds.
	PolicyActionKinds []string
	// ContextBudget is returned by Capabilities().ContextBudget.
	ContextBudget int
	TokenLifetime time.Duration // overrides defaultMintTimeout when non-zero (test-only knob)
}

// Adapter implements port.Provider by running an ephemeral opencode CLI process per task.
// It is stateless: each RunTask call creates a fresh exec.Cmd with no shared state.
type Adapter struct {
	opencodeBin         string
	limit               int64
	model               string
	agentName           string
	systemPromptContent string // file content read once at New(); empty → no prepend
	mcpRegistry         TokenMinter
	mcpServerAddr       string
	tenant              string
	mcpAgentName        string
	policyActionKinds   []string
	contextBudget       int
	tokenLifetime       time.Duration
}

// New returns an Adapter that invokes opencodeBin as the opencode CLI.
// opencodeBin must be a path to the opencode executable (or a test double).
// model is optional; when non-empty it is passed as --model <model>.
// agentName is optional; when non-empty it is passed as --agent <agentName>.
// systemPromptPath is optional; when non-empty the file is read once at construction time
// and its content is prepended to every task input as "[SYSTEM]\n{content}\n\n".
// --pure is always included unconditionally for agent isolation.
func New(opencodeBin string, opts Options, model string, agentName string, systemPromptPath string) *Adapter {
	limit := opts.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	var content string
	if systemPromptPath != "" {
		raw, err := os.ReadFile(systemPromptPath)
		if err == nil {
			content = string(raw)
		} else {
			// TOCTOU: Load() validated the file but it became unreadable before New().
			// The agent starts without a system prompt rather than crashing.
			fmt.Fprintf(os.Stderr, "warn: system_prompt file validated at config load but unreadable at adapter construction: %v; agent will start without system prompt\n", err)
		}
	}
	mcpAgentName := opts.AgentName
	if mcpAgentName == "" {
		mcpAgentName = agentName
	}
	lifetime := opts.TokenLifetime
	if lifetime <= 0 {
		lifetime = defaultMintTimeout
	}
	return &Adapter{
		opencodeBin:         opencodeBin,
		limit:               limit,
		model:               model,
		agentName:           agentName,
		systemPromptContent: content,
		mcpRegistry:         opts.MCPRegistry,
		mcpServerAddr:       opts.MCPServerAddr,
		tenant:              opts.Tenant,
		mcpAgentName:        mcpAgentName,
		policyActionKinds:   opts.PolicyActionKinds,
		contextBudget:       opts.ContextBudget,
		tokenLifetime:       lifetime,
	}
}

// RunTask implements port.Provider.RunTask.
// It spawns a fresh opencode process with "run" (non-interactive mode), passes input as
// a positional argv argument, reads stdout up to the size cap, and returns a parsed
// ProviderResult. Non-zero exit → error. ctx deadline kills the child.
//
// When mcpRegistry is configured, RunTask mints a per-invocation MCP bearer token and
// sets OPENCODE_CONFIG_CONTENT on the subprocess env only (never os.Setenv, never a
// persisted file), then drains the token's recorded ActionIntents after the subprocess
// exits. The bearer token is delivered only inside that env var's JSON, never on argv.
func (a *Adapter) RunTask(ctx context.Context, taskID string, input string) (port.ProviderResult, error) {
	// Build argv: opencode run --pure [--model <model>] [--agent <agentName>] [--format json]
	//   <effective-input>
	// argv-as-slice: input is passed as a literal argument, never interpolated into a shell string.
	// This is the primary guard against argument injection.
	// --pure is always included unconditionally for agent isolation.
	// When systemPromptContent is set, prepend "[SYSTEM]\n{content}\n\n" to the task input.
	args := []string{"run", "--pure"}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	if a.agentName != "" {
		args = append(args, "--agent", a.agentName)
	}
	// --format json is always requested: opencode's NDJSON stream is parsed solely to
	// extract text (never any tool_use-shaped event — the spec forbids that; see
	// spec: "ActionIntents Are Collected From the Sink, Never From Stream Parsing").
	args = append(args, "--format", "json")

	var handle Drainer
	var mcpEnv string
	if a.mcpRegistry != nil {
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(a.tokenLifetime)
		}
		token, h := a.mcpRegistry.Mint(a.tenant, a.mcpAgentName, taskID, deadline.Add(defaultTokenGrace))
		handle = h
		defer h.Drain() // release on every return path; idempotent

		cfgJSON, err := buildMCPConfigJSON(a.mcpServerAddr, token)
		if err != nil {
			return port.ProviderResult{}, fmt.Errorf("opencode: mcp config: %w", err)
		}
		mcpEnv = string(cfgJSON)
	}

	effectiveInput := input
	if a.systemPromptContent != "" {
		effectiveInput = "[SYSTEM]\n" + a.systemPromptContent + "\n\n" + input
	}
	args = append(args, effectiveInput)

	cmd := exec.CommandContext(ctx, a.opencodeBin, args...) //nolint:gosec // argv slice, no shell
	if mcpEnv != "" {
		// Subprocess-scoped only — never os.Setenv, never touches persisted config
		// (design Threat matrix: "OPENCODE_CONFIG_CONTENT scoped to that process env").
		cmd.Env = append(os.Environ(), "OPENCODE_CONFIG_CONTENT="+mcpEnv)
	}

	// Capture stderr independently of StdoutPipe. cmd.Stderr and StdoutPipe are
	// orthogonal: setting Stderr does not interfere with the LimitReader drain pattern.
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	// Use StdoutPipe so we control reading. This lets us read only up to the size cap
	// and then drain the remainder via io.Discard in a goroutine, preventing EPIPE.
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return port.ProviderResult{}, fmt.Errorf("opencode: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return port.ProviderResult{}, fmt.Errorf("opencode: start: %w", err)
	}

	// Read up to limit bytes via io.LimitReader. After limit bytes, switch to
	// draining via io.Discard so the child can write without blocking on a full pipe.
	// This reads until EOF (process exit) — never waits on any dedicated terminal
	// event line inside the stream (spec: "OpenCode adapter terminates its reader on
	// process exit, not a sentinel event").
	lr := io.LimitReader(stdout, a.limit)
	var buf bytes.Buffer
	n, readErr := io.Copy(&buf, lr)

	// Drain any remaining output so the child is not blocked writing to a full pipe.
	io.Copy(io.Discard, stdout) //nolint:errcheck // drain only; error irrelevant

	// Wait for exit. exec.CommandContext sends SIGKILL when ctx is done.
	waitErr := cmd.Wait()

	// Prioritise context cancellation: if ctx is done the kill error is expected.
	if ctx.Err() != nil {
		return port.ProviderResult{}, fmt.Errorf("opencode: deadline exceeded: %w", ctx.Err())
	}
	if waitErr != nil {
		// Non-zero exit → failure outcome. Include captured stderr and stdout
		// so callers receive actionable diagnostics. Some CLI errors land on stdout.
		return port.ProviderResult{}, fmt.Errorf("opencode: %w\nstderr: %s\nstdout: %s", waitErr, stderrBuf.String(), buf.String())
	}
	if readErr != nil {
		return port.ProviderResult{}, fmt.Errorf("opencode: read output: %w", readErr)
	}

	var output string
	if n >= a.limit {
		// Output was capped mid-stream — the raw bytes may not be valid NDJSON.
		// Fall back to the legacy raw-truncation behaviour rather than failing to parse.
		output = parseOutput(buf.Bytes(), n, a.limit)
	} else {
		output = parseStreamText(buf.Bytes())
		if output == "" {
			// Not every opencode invocation emits NDJSON text lines; fall back to the
			// raw trimmed text rather than losing it.
			if raw := strings.TrimRight(buf.String(), "\n"); raw != "" {
				output = raw
			}
		}
	}

	result := port.ProviderResult{Output: output}
	if handle != nil {
		// Sink is authoritative: ActionIntents come only from the MCP server's sink,
		// never from parsing the stream above.
		result.ActionIntents = handle.Drain()
	}
	return result, nil
}

// parseOutput converts raw bytes to a string, prepending TruncationMarker when the
// byte count equals the limit (meaning io.LimitReader may have stopped early).
func parseOutput(raw []byte, n, limit int64) string {
	text := strings.TrimRight(string(raw), "\n")
	if n >= limit {
		return TruncationMarker + " " + text
	}
	return text
}

// streamEvent is a permissive envelope for the opencode --format json lines this adapter
// cares about: a bare {"type":"text","text":"..."} line. Only text content is ever
// extracted (spec: "ActionIntents Are Collected From the Sink, Never From Stream Parsing").
type streamEvent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
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
		if ev.Type == "text" {
			sb.WriteString(ev.Text)
		}
	}
	return sb.String()
}

// buildMCPConfigJSON marshals the MCP server descriptor placed in OPENCODE_CONFIG_CONTENT:
//
//	{"mcpServers": {"framework": {"type": "http", "url": "http://<addr>",
//	  "headers": {"Authorization": "Bearer <token>"}}}}
func buildMCPConfigJSON(addr, token string) ([]byte, error) {
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
		return nil, fmt.Errorf("marshal: %w", err)
	}
	return data, nil
}

// NOTE: ProbeModel is intentionally NOT implemented for the opencode adapter.
// Unlike Claude CLI, opencode's "run" command does not distinguish between
// an invalid model and a missing prompt — both produce the same generic error.
// Until opencode exposes a model-validation path, this adapter does not satisfy
// the modelProber interface, and materializeAgents skips the probe for it.

// ---- port.Provider stub methods (A2A network client side) -------------------
// The A2A client methods are implemented by transport/a2a, not by this adapter.
// These stubs satisfy the interface so the package compiles and contract tests
// can exercise RunTask in isolation.

var errNotImplemented = fmt.Errorf("opencode: A2A client methods are provided by transport/a2a, not this adapter")

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
