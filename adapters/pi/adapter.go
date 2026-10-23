// Package pi provides the pi coding agent adapter — a concrete implementation of
// port.Provider. It spawns an ephemeral pi CLI process per invocation via os/exec with an
// argv slice (never sh -c), using "-p --mode json" for non-interactive JSONL output. The
// task input is delivered on stdin. It enforces ctx deadlines and parses output into
// framework types before returning. Raw process output never crosses the port boundary.
//
// Import policy: imported only by cmd/company (the composition root). core/ must never
// import this package.
package pi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// TruncationMarker is prepended to output when it is truncated by the size cap.
const TruncationMarker = "[output truncated]"

// defaultOutputLimit is the maximum bytes read from a pi process before truncation.
const defaultOutputLimit int64 = 1 << 20 // 1 MiB

// maxLineSize bounds the memory held for a single JSONL line. pi's agent_end event repeats
// every message of the session, so lines can be large, but a line past this size is drained
// and skipped without parsing rather than buffered without bound.
const maxLineSize = 8 << 20 // 8 MiB

// killWaitDelay is the backstop after cancellation: even if a descendant escaped the
// process group and still holds the stdout/stderr descriptors, Wait returns and the pipes
// are force-closed this long after the kill, so RunTask cannot outlive its deadline unboundedly.
const killWaitDelay = 5 * time.Second

// defaultTokenGrace extends the invocation deadline before it is used as the MCP
// token expiry, so the token remains valid for the brief window between process
// exit and the adapter draining it.
const defaultTokenGrace = 30 * time.Second

// defaultMintTimeout: token lifetime with no ctx deadline. Drain releases the entry
// regardless, so this only avoids mid-task 401s from an arbitrary ceiling.
const defaultMintTimeout = 24 * time.Hour

// extensionHeader opens the ephemeral pi extension registering the framework MCP server. It
// is static: the URL and bearer token reach it only through the subprocess env vars
// ASF_MCP_URL / ASF_MCP_TOKEN, so the file never contains the token.
const extensionHeader = "export default function (pi: any) {\n"

const frameworkRegistration = `  pi.registerMcpServer("framework", {
    url: process.env.ASF_MCP_URL,
    headers: { Authorization: "Bearer " + process.env.ASF_MCP_TOKEN },
    exposure: "direct",
  });
`

// MCPServer is an agent-declared stdio MCP server (config.MCPServer), registered through
// pi.registerMcpServer for every invocation. Its tools are not action intents and bypass
// the risk policy: declaring it is an explicit grant by the company owner. Command and Args
// are written into the temp extension file, so they must not carry secrets.
type MCPServer struct {
	Command string
	Args    []string
}

// buildExtensionSource renders the static extension: the framework registration when
// withFramework, then each declared server in sorted name order. names is that sorted order.
// With no servers and withFramework the output is exactly the pre-mcp_servers source.
func buildExtensionSource(withFramework bool, servers map[string]MCPServer, names []string) string {
	var b strings.Builder
	b.WriteString(extensionHeader)
	if withFramework {
		b.WriteString(frameworkRegistration)
	}
	for _, name := range names {
		srv := servers[name]
		args := srv.Args
		if args == nil {
			args = []string{}
		}
		cfg, _ := json.Marshal(struct {
			Command  string   `json:"command"`
			Args     []string `json:"args"`
			Exposure string   `json:"exposure"`
		}{srv.Command, args, "direct"})
		fmt.Fprintf(&b, "  pi.registerMcpServer(%q, %s);\n", name, cfg)
	}
	b.WriteString("}\n")
	return b.String()
}

// Drainer is satisfied by a live MCP invocation handle: after the subprocess exits,
// Drain releases the invocation and returns any action intents the MCP server's sink
// recorded for it. Idempotent — a second Drain call returns nil without panicking.
//
// Deliberately identical in shape to (but a distinct type from) opencode.Drainer: each
// adapter package defines its own copy to stay decoupled from transport/mcp and from each
// other.
type Drainer interface {
	Drain() []port.ActionIntent
	// Contacted reports whether the agent CLI ever reached the MCP server with this
	// invocation's bearer token. An empty sink alone cannot distinguish "the agent chose
	// not to call a tool" from "the agent never reached the MCP server at all".
	Contacted() bool
}

// TokenMinter mints a per-invocation MCP bearer token bound to {tenant, agent, taskID}.
type TokenMinter interface {
	Mint(tenant, agent, taskID string, exp time.Time) (string, Drainer)
}

// Options configures the adapter. Zero value is valid (uses defaults, no MCP wiring).
type Options struct {
	// OutputLimit caps the number of bytes read from the child process stdout.
	// When zero, defaultOutputLimit is used.
	OutputLimit int64
	// MCPRegistry mints per-invocation MCP bearer tokens. Nil disables MCP wiring
	// entirely: no extension file, no ASF_MCP_* env, and ActionIntents is always empty.
	MCPRegistry TokenMinter
	// MCPServerAddr is the loopback address of the running MCP server (e.g. "127.0.0.1:54321").
	// Required when MCPRegistry is non-nil.
	MCPServerAddr string
	// Tenant is passed to MCPRegistry.Mint and must match the MCP server's configured tenant.
	Tenant string
	// AgentName identifies this adapter's agent to MCPRegistry.Mint.
	AgentName string
	// PolicyActionKinds is returned by Capabilities().ActionKinds.
	PolicyActionKinds []string
	// ContextBudget is returned by Capabilities().ContextBudget.
	ContextBudget int
	TokenLifetime time.Duration // overrides defaultMintTimeout when non-zero (test-only knob)
	// MCPHealthCheck reports the MCP server's health when non-nil (production: the running
	// transport/mcp Server's Err() method value). RunTask consults it before spawning the
	// subprocess whenever MCPRegistry is configured: a non-nil result aborts the invocation
	// instead of running an agent whose tool calls could never reach a live server, which
	// would otherwise look like "the agent made no tool calls".
	MCPHealthCheck func() error
	// MCPServers are the agent's own stdio MCP servers (pi-only mcp_servers), registered on
	// every invocation next to (or, without MCPRegistry, instead of) the framework server.
	MCPServers map[string]MCPServer
}

// Adapter implements port.Provider by running an ephemeral pi CLI process per task.
// It is stateless: each RunTask call creates a fresh exec.Cmd with no shared state.
type Adapter struct {
	piBin             string
	limit             int64
	model             string
	systemPromptPath  string
	extensions        []string
	mcpRegistry       TokenMinter
	mcpServerAddr     string
	tenant            string
	mcpAgentName      string
	policyActionKinds []string
	contextBudget     int
	tokenLifetime     time.Duration
	mcpHealthCheck    func() error
	mcpServers        map[string]MCPServer
	mcpServerNames    []string // sorted keys of mcpServers
}

// New returns an Adapter that invokes piBin as the pi CLI.
// model is optional; when non-empty it is passed as --model <model>.
// systemPromptPath is optional; when non-empty it is passed as --append-system-prompt
// <path> (pi reads the file contents itself when given a path).
// extensions are passed as -e <entry>, in order; each is an absolute path or
// builtin:<name>, already validated by config.
// The isolation flags are always included unconditionally.
func New(piBin string, opts Options, model string, systemPromptPath string, extensions []string) *Adapter {
	limit := opts.OutputLimit
	if limit <= 0 {
		limit = defaultOutputLimit
	}
	lifetime := opts.TokenLifetime
	if lifetime <= 0 {
		lifetime = defaultMintTimeout
	}
	names := make([]string, 0, len(opts.MCPServers))
	for name := range opts.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return &Adapter{
		mcpServers:        opts.MCPServers,
		mcpServerNames:    names,
		piBin:             piBin,
		limit:             limit,
		model:             model,
		systemPromptPath:  systemPromptPath,
		extensions:        extensions,
		mcpRegistry:       opts.MCPRegistry,
		mcpServerAddr:     opts.MCPServerAddr,
		tenant:            opts.Tenant,
		mcpAgentName:      opts.AgentName,
		policyActionKinds: opts.PolicyActionKinds,
		contextBudget:     opts.ContextBudget,
		tokenLifetime:     lifetime,
		mcpHealthCheck:    opts.MCPHealthCheck,
	}
}

// RunTask implements port.Provider.RunTask.
// It spawns a fresh pi process in print/JSON mode with every discovery and persistence
// feature disabled, writes input to the child's stdin, reads stdout up to the size cap,
// and returns a parsed ProviderResult. Non-zero exit → error. ctx deadline kills the child.
//
// pi exits 0 even when the model call fails; the failure only shows up as a final
// assistant message with stopReason "error" or "aborted", which RunTask maps to an error.
//
// When mcpRegistry is configured, RunTask mints a per-invocation MCP bearer token and
// delivers it to pi only through ASF_MCP_URL / ASF_MCP_TOKEN on the subprocess env (never
// os.Setenv), read by a static 0600 temp extension that is removed on every return path.
// The token is never on argv nor in the extension file.
func (a *Adapter) RunTask(ctx context.Context, taskID string, input string) (port.ProviderResult, error) {
	// argv-as-slice: nothing from the task is interpolated into a shell string.
	args := []string{
		"-p", "--mode", "json", "--no-session", "-ne",
		"--no-skills", "--no-prompt-templates", "--no-context-files", "--no-approve", "--offline",
	}
	if a.model != "" {
		args = append(args, "--model", a.model)
	}
	if a.systemPromptPath != "" {
		args = append(args, "--append-system-prompt", a.systemPromptPath)
	}
	for _, ext := range a.extensions {
		args = append(args, "-e", ext)
	}

	var handle Drainer
	var mcpEnv []string
	if a.mcpRegistry == nil && len(a.mcpServers) > 0 {
		extPath, err := writeExtensionFile(buildExtensionSource(false, a.mcpServers, a.mcpServerNames))
		if err != nil {
			return port.ProviderResult{}, fmt.Errorf("pi: mcp extension: %w", err)
		}
		defer os.Remove(extPath)
		args = append(args, "-e", "builtin:mcp", "-e", extPath, "--tools", a.toolsAllowlist(false))
	}
	if a.mcpRegistry != nil {
		// Consult the MCP server's health before ever spawning the subprocess; see
		// Options.MCPHealthCheck.
		if a.mcpHealthCheck != nil {
			if healthErr := a.mcpHealthCheck(); healthErr != nil {
				return port.ProviderResult{}, fmt.Errorf("pi: mcp server unavailable, refusing to run task without a working MCP endpoint: %w", healthErr)
			}
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			deadline = time.Now().Add(a.tokenLifetime)
		}
		token, h := a.mcpRegistry.Mint(a.tenant, a.mcpAgentName, taskID, deadline.Add(defaultTokenGrace))
		handle = h
		defer h.Drain() // release on every return path; idempotent

		extPath, err := writeExtensionFile(buildExtensionSource(true, a.mcpServers, a.mcpServerNames))
		if err != nil {
			return port.ProviderResult{}, fmt.Errorf("pi: mcp extension: %w", err)
		}
		defer os.Remove(extPath)

		args = append(args, "-e", "builtin:mcp", "-e", extPath, "--tools", a.toolsAllowlist(true))
		mcpEnv = []string{"ASF_MCP_URL=http://" + a.mcpServerAddr, "ASF_MCP_TOKEN=" + token}
	}

	cmd := exec.CommandContext(ctx, a.piBin, args...) //nolint:gosec // argv slice, no shell
	cmd.Stdin = strings.NewReader(input)
	configureProcessTree(cmd)
	cmd.WaitDelay = killWaitDelay
	if mcpEnv != nil {
		// Subprocess-scoped only — never os.Setenv.
		cmd.Env = append(os.Environ(), mcpEnv...)
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return port.ProviderResult{}, fmt.Errorf("pi: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return port.ProviderResult{}, fmt.Errorf("pi: start: %w", err)
	}

	// Consume the WHOLE stdout line by line (so a failure past the cap is still seen and the
	// child never blocks on a full pipe). Reads until EOF (process exit) — never waits on a
	// terminal event line.
	// The read runs before Wait, so Wait's WaitDelay cannot unblock it; close the pipe
	// ourselves killWaitDelay after cancellation in case a descendant escaped the group.
	parseDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-parseDone:
			case <-time.After(killWaitDelay):
				stdout.Close()
			}
		case <-parseDone:
		}
	}()
	parsed, readErr := parseStream(stdout, a.limit)
	close(parseDone)

	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return port.ProviderResult{}, fmt.Errorf("pi: deadline exceeded: %w", ctx.Err())
	}
	if waitErr != nil {
		return port.ProviderResult{}, fmt.Errorf("pi: %w\nstderr: %s\nstdout: %s", waitErr, stderrBuf.String(), parsed.raw)
	}
	if readErr != nil {
		return port.ProviderResult{}, fmt.Errorf("pi: read output: %w", readErr)
	}
	// Failure detection runs regardless of truncation.
	if parsed.failure != "" {
		return port.ProviderResult{}, fmt.Errorf("pi: model call failed: %s\nstderr: %s", parsed.failure, stderrBuf.String())
	}

	output, truncated := parsed.text, parsed.textTruncated
	if !parsed.recognised {
		// Gated on `recognised`, not on an empty extraction: a well-formed stream with no
		// assistant text must stay empty rather than dump the raw JSONL envelope.
		if raw := strings.TrimRight(string(parsed.raw), "\n"); raw != "" {
			output, truncated = raw, parsed.rawTruncated
		}
	}
	if truncated {
		output = TruncationMarker + " " + output
	}

	result := port.ProviderResult{Output: output}
	if handle != nil {
		// Sink is authoritative: ActionIntents come only from the MCP server's sink,
		// never from parsing tool calls out of the stream above.
		result.ActionIntents = handle.Drain()

		// An empty sink is ambiguous on its own; the registry knows whether the minted
		// token was ever presented.
		if len(result.ActionIntents) == 0 && !handle.Contacted() {
			return port.ProviderResult{}, fmt.Errorf("pi: mcp endpoint %s was never contacted by the CLI for task %q; refusing to report success for an invocation whose tool calls could not have been recorded", a.mcpServerAddr, taskID)
		}
	}
	return result, nil
}

// toolsAllowlist is the --tools value: the built-ins, the framework server when wired, and
// mcp__<name>__* for each declared server (sorted). Ambient servers stay hidden.
//
// pi names a server's tools mcp__<server>__<tool> with every character other than
// letters, digits and "_" replaced by "_" (docs/mcp.md), so server "chrome-devtools"
// exposes mcp__chrome_devtools__*. Config only admits [a-z0-9_-], so "-" is the one
// character to map; an unmapped pattern would match nothing and hide every tool.
func (a *Adapter) toolsAllowlist(withFramework bool) string {
	parts := []string{"read,bash,edit,write"}
	if withFramework {
		parts = append(parts, "mcp__framework__*")
	}
	for _, name := range a.mcpServerNames {
		parts = append(parts, "mcp__"+strings.ReplaceAll(name, "-", "_")+"__*")
	}
	return strings.Join(parts, ",")
}

// writeExtensionFile writes the static extension source to a 0600 temp file ending in
// .ts (pi loads extensions by suffix) and returns its path. The caller removes it.
func writeExtensionFile(source string) (string, error) {
	f, err := os.CreateTemp("", "asf-pi-mcp-*.ts")
	if err != nil {
		return "", err
	}
	path := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.WriteString(source); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

// streamEvent is the pi --mode json JSONL event envelope (verified on pi 1.2.0). The first
// line is a {"type":"session",...} header; the authoritative per-message events are
// "message_end". Only the fields this adapter reads are declared.
type streamEvent struct {
	Type    string         `json:"type"`
	Message *streamMessage `json:"message,omitempty"`
}

type streamMessage struct {
	Role         string         `json:"role"`
	Content      []contentBlock `json:"content"`
	StopReason   string         `json:"stopReason"`
	ErrorMessage string         `json:"errorMessage"`
}

// contentBlock is one element of a message's content. Only "text" blocks are extracted;
// thinking and toolCall blocks are deliberately ignored (ActionIntents come from the sink).
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// streamResult is what parseStream extracted from a pi stdout stream.
type streamResult struct {
	text          string // assistant text blocks in stream order, capped at limit bytes
	textTruncated bool   // extracted text exceeded limit
	failure       string // errorMessage when the LAST assistant message_end errored/aborted
	recognised    bool   // at least one line was a JSON object with a non-empty "type"
	raw           []byte // first limit bytes of stdout, for the raw-text fallback
	rawTruncated  bool   // stdout exceeded limit
}

// parseStream reads r to EOF line by line. It extracts, in stream order, the text blocks of
// every assistant message_end, and records a failure message when the LAST assistant
// message_end ended with stopReason "error" or "aborted" (pi exits 0 in that case).
// Lines that fail to parse, and lines longer than maxLineSize, are skipped; the stream is
// always drained so the child never blocks. Memory is bounded: text and raw are each capped
// at limit bytes, and at most maxLineSize bytes of one line are held.
//
// recognised reports whether at least one line was a JSON object with a non-empty "type".
// When true the extracted text is authoritative even when empty; when false the caller
// falls back to the raw output.
func parseStream(r io.Reader, limit int64) (streamResult, error) {
	var res streamResult
	var text strings.Builder
	br := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	overlong := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(chunk) > 0 || !isPrefix {
			res.addRaw(chunk, limit)
		}
		if !overlong {
			line = append(line, chunk...)
			if len(line) > maxLineSize {
				overlong, line = true, nil
			}
		}
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return finish(res, &text), err
		}
		if isPrefix {
			continue
		}
		if !overlong {
			res.handleLine(line, &text, limit)
		}
		line, overlong = line[:0], false
	}
}

func finish(res streamResult, text *strings.Builder) streamResult {
	res.text = text.String()
	return res
}

// addRaw keeps the first limit bytes of stdout (newline-delimited) for the raw fallback.
func (res *streamResult) addRaw(chunk []byte, limit int64) {
	room := limit - int64(len(res.raw))
	if room <= 0 {
		if len(chunk) > 0 {
			res.rawTruncated = true
		}
		return
	}
	if int64(len(chunk)) > room {
		res.raw = append(res.raw, chunk[:room]...)
		res.rawTruncated = true
		return
	}
	res.raw = append(res.raw, chunk...)
}

func (res *streamResult) handleLine(line []byte, text *strings.Builder, limit int64) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var ev streamEvent
	if err := json.Unmarshal(line, &ev); err != nil || ev.Type == "" {
		return
	}
	res.recognised = true
	if ev.Type != "message_end" || ev.Message == nil || ev.Message.Role != "assistant" {
		return
	}
	for _, block := range ev.Message.Content {
		if block.Type != "text" {
			continue
		}
		room := limit - int64(text.Len())
		switch {
		case room <= 0:
			if block.Text != "" {
				res.textTruncated = true
			}
		case int64(len(block.Text)) > room:
			text.WriteString(block.Text[:room])
			res.textTruncated = true
		default:
			text.WriteString(block.Text)
		}
	}
	res.failure = ""
	if sr := ev.Message.StopReason; sr == "error" || sr == "aborted" {
		res.failure = ev.Message.ErrorMessage
		if res.failure == "" {
			res.failure = "stopReason " + sr
		}
	}
}

// thinkingSuffixes are the trailing ":<level>" values pi accepts on a model spec.
var thinkingSuffixes = []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// ProbeModel verifies the configured pi setup at startup. With no model configured it only
// checks that the binary runs (`pi --version`). Otherwise it runs
// `pi -ne [-e <ext>...] --offline --list-models` and requires the configured model to be
// listed. The model may be "provider/id" or a bare "id", with an optional ":<thinking>"
// suffix. Extensions are passed so extension-provided providers are visible.
func (a *Adapter) ProbeModel(ctx context.Context) error {
	if a.model == "" {
		cmd := exec.CommandContext(ctx, a.piBin, "--version") //nolint:gosec // argv slice, no shell
		configureProcessTree(cmd)
		var stderrBuf bytes.Buffer
		cmd.Stderr = &stderrBuf
		cmd.Stdout = io.Discard
		err := cmd.Run()
		if ctx.Err() != nil {
			return fmt.Errorf("pi: probe deadline exceeded: %w", ctx.Err())
		}
		if err != nil {
			return fmt.Errorf("pi: cannot run %q --version: %w\nstderr: %s", a.piBin, err, stderrBuf.String())
		}
		return nil
	}

	args := []string{"-ne"}
	for _, ext := range a.extensions {
		args = append(args, "-e", ext)
	}
	args = append(args, "--offline", "--list-models")
	cmd := exec.CommandContext(ctx, a.piBin, args...) //nolint:gosec // argv slice, no shell
	configureProcessTree(cmd)
	cmd.WaitDelay = killWaitDelay
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	if ctx.Err() != nil {
		return fmt.Errorf("pi: probe deadline exceeded: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("pi: %q --list-models failed: %w\nstderr: %s", a.piBin, err, stderrBuf.String())
	}

	provider, id := splitModelSpec(a.model)
	// pi may print the table on stdout or stderr depending on version; scan both.
	for _, table := range []string{stdoutBuf.String(), stderrBuf.String()} {
		if listsModel(table, provider, id) {
			return nil
		}
	}
	return fmt.Errorf("pi: model %q was not listed by `pi --list-models` with the configured extensions %v", a.model, a.extensions)
}

// splitModelSpec strips a trailing ":<thinking>" suffix and splits an optional
// "provider/" prefix. Only the first "/" separates provider from id, since ids may
// themselves contain "/" (e.g. openrouter-style ids).
func splitModelSpec(spec string) (provider, id string) {
	if i := strings.LastIndex(spec, ":"); i >= 0 {
		for _, s := range thinkingSuffixes {
			if spec[i+1:] == s {
				spec = spec[:i]
				break
			}
		}
	}
	if i := strings.Index(spec, "/"); i >= 0 {
		return spec[:i], spec[i+1:]
	}
	return "", spec
}

// listsModel reports whether a `--list-models` table contains the model. The first two
// whitespace-separated columns of each row are provider and model; the header row is
// skipped. An empty provider matches any provider.
func listsModel(table, provider, id string) bool {
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || (fields[0] == "provider" && fields[1] == "model") {
			continue
		}
		if fields[1] == id && (provider == "" || fields[0] == provider) {
			return true
		}
	}
	return false
}

// ---- port.Provider stub methods (task-completion reporting side) ------------
// Complete/CompleteError are implemented by transport/a2a, not by this adapter.

var errNotImplemented = fmt.Errorf("pi: A2A client methods are provided by transport/a2a, not this adapter")

// Capabilities returns the ContextBudget and ActionKinds configured via Options
// at construction time (see New).
func (a *Adapter) Capabilities() port.ProviderCapabilities {
	return port.ProviderCapabilities{ContextBudget: a.contextBudget, ActionKinds: a.policyActionKinds}
}

func (a *Adapter) Complete(_ string, _ port.TaskResult) error { return errNotImplemented }
func (a *Adapter) CompleteError(_ string, _ error) error      { return errNotImplemented }
func (a *Adapter) SendTask(_ context.Context, _ address.A2AAddress, _ string, _ map[string]any, _ *port.TaskOptions) (string, error) {
	return "", errNotImplemented
}

// compile-time check: Adapter must satisfy port.Provider.
var _ port.Provider = (*Adapter)(nil)
