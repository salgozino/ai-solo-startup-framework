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
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// TruncationMarker is prepended to output when it is truncated by the size cap (task 5.3).
const TruncationMarker = "[output truncated]"

// defaultOutputLimit is the maximum bytes read from a claude process before truncation.
const defaultOutputLimit int64 = 1 << 20 // 1 MiB

// Options configures the adapter. Zero value is valid (uses defaults).
type Options struct {
	// OutputLimit caps the number of bytes read from the child process stdout.
	// When zero, defaultOutputLimit is used.
	OutputLimit int64
}

// Adapter implements port.Provider by running an ephemeral claude CLI process per task.
// It is stateless: each RunTask call creates a fresh exec.Cmd with no shared state.
type Adapter struct {
	claudeBin        string
	limit            int64
	model            string
	systemPromptPath string // absolute path; empty → flag omitted
}

// New returns an Adapter that invokes claudeBin as the claude CLI.
// claudeBin must be a path to the claude executable (or a test double).
// model is optional; when non-empty it is passed as --model <model>.
// systemPromptPath is optional; when non-empty it is passed as --system-prompt-file <path>.
// --safe-mode and --no-session-persistence are always included unconditionally.
func New(claudeBin string, opts Options, model string, systemPromptPath string) *Adapter {
	limit := opts.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	return &Adapter{
		claudeBin:        claudeBin,
		limit:            limit,
		model:            model,
		systemPromptPath: systemPromptPath,
	}
}

// RunTask implements port.Provider.RunTask.
// It spawns a fresh claude process with -p (non-interactive mode), passes input as
// a positional argv argument, reads stdout up to the size cap, and returns a parsed
// ProviderResult. Non-zero exit → error. ctx deadline kills the child.
func (a *Adapter) RunTask(ctx context.Context, _ string, input string) (port.ProviderResult, error) {
	// argv-as-slice: input is passed as a literal argument, never interpolated into a shell string.
	// This is the primary guard against argument injection (threat-matrix case a).
	// -p requests non-interactive mode: claude processes the prompt and prints output to stdout,
	// then exits. Without -p, claude starts an interactive REPL which blocks forever.
	// --safe-mode disables all customizations (skills, MCP, CLAUDE.md, hooks) while
	// preserving the user's auth/keychain — unlike --bare which requires ANTHROPIC_API_KEY.
	// --no-session-persistence prevents writing session transcripts to disk.
	// A fresh exec.Cmd per call → stateless across invocations (task 5.5).
	args := []string{"-p", "--safe-mode", "--no-session-persistence"}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	if a.systemPromptPath != "" {
		args = append(args, "--system-prompt-file", a.systemPromptPath)
	}
	args = append(args, input)

	cmd := exec.CommandContext(ctx, a.claudeBin, args...) //nolint:gosec // argv slice, no shell

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

	output := parseOutput(buf.Bytes(), n, a.limit)
	return port.ProviderResult{Output: output}, nil
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

// Capabilities returns a zero ProviderCapabilities for Phase 1.
// A real implementation that derives ActionKinds from the risk policy and
// returns the configured ContextBudget is deferred to Phase 3.
func (a *Adapter) Capabilities() port.ProviderCapabilities { return port.ProviderCapabilities{} }

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
