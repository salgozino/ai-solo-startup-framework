// fakeopencode simulates the opencode CLI for unit testing the OpenCode adapter.
// It reads argv and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// When --pure is present, it prepends "pure:1|" to the output so tests
// can verify the isolation flag was passed correctly.
// When --model is present, it prepends "model:<model>|" to the output.
// When --agent is present, it prepends "agent:<agent>|" to the output.
// --format consumes its value but is otherwise ignored; the adapter always parses
// stdout as NDJSON now, so the default-case output below is always emitted as the
// real opencode event stream: a step_start line, a text line whose content lives at
// part.text (NOT at the top level), and a step_finish line. See emitNDJSONText for
// the captured ground truth this mirrors.
//
// MCP-related test hooks (mirrors fakeclaude's hooks so both fakes stay consistent):
//
//	FAKEOPENCODE_DUMP_ARGV=1 + FAKEOPENCODE_ARGV_FILE=<path>
//	    writes os.Args (newline-separated) to <path> before any other behavior.
//	FAKEOPENCODE_DUMP_ENV_PATH=<path>
//	    writes the value of the OPENCODE_CONFIG_CONTENT env var this process actually
//	    received to <path>, letting tests observe that MCP configuration reached the
//	    subprocess env (as opposed to being unset, or leaked via a persisted file).
//	FAKEOPENCODE_CALL_MCP=1
//	    reads OPENCODE_CONFIG_CONTENT (the env var the adapter sets, mirroring what the
//	    real opencode CLI reads), extracts the server URL and bearer token, and performs
//	    a real MCP tools/call for "telegram_send" before producing normal output.
//	FAKEOPENCODE_CONNECT_MCP_ONLY=1
//	    same as FAKEOPENCODE_CALL_MCP but stops after the MCP handshake, calling no tool.
//	    This represents the healthy "the agent reached the server and chose not to call a
//	    tool" case, which must stay distinguishable from never reaching the server at all.
//
// ProbeModel test hooks (simulate CLI capability/version behavior, independent of model):
//
//	FAKEOPENCODE_REJECT_FORMAT_FLAG=1
//	    simulates a CLI build that does not support --format: if --format is present in
//	    argv, prints a flag-rejection error to stderr and exits 2, regardless of input.
//	FAKEOPENCODE_PROBE_HANG=1
//	    simulates a CLI that never returns for the probe invocation — sleeps until killed
//	    by the caller's context deadline.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// opencodeMCPConfig mirrors the MCP config JSON the opencode adapter puts in
// OPENCODE_CONFIG_CONTENT, in opencode's own config schema shape (not Claude's
// "mcpServers" shape): {"mcp": {"framework": {"type": "remote", "url": "...",
// "enabled": true, "headers": {"Authorization": "Bearer ..."}}}}.
type opencodeMCPConfig struct {
	MCP map[string]opencodeMCPServerEntry `json:"mcp"`
}

type opencodeMCPServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Enabled bool              `json:"enabled"`
	Headers map[string]string `json:"headers"`
}

// staticOAuthHandler feeds a fixed bearer token to the go-sdk MCP client.
type staticOAuthHandler struct{ token string }

func (h *staticOAuthHandler) TokenSource(_ context.Context) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token}), nil
}

func (h *staticOAuthHandler) Authorize(_ context.Context, _ *http.Request, _ *http.Response) error {
	return nil
}

func main() {
	if os.Getenv("FAKEOPENCODE_DUMP_ARGV") == "1" {
		if f := os.Getenv("FAKEOPENCODE_ARGV_FILE"); f != "" {
			_ = os.WriteFile(f, []byte(strings.Join(os.Args, "\n")), 0o600)
		}
	}
	if dumpPath := os.Getenv("FAKEOPENCODE_DUMP_ENV_PATH"); dumpPath != "" {
		_ = os.WriteFile(dumpPath, []byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), 0o600)
	}
	if os.Getenv("FAKEOPENCODE_PROBE_HANG") == "1" {
		time.Sleep(24 * time.Hour)
	}

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "fakeopencode: expected run <argument>")
		os.Exit(2)
	}

	// Args: [run [--pure] [--model <model>] [--agent <agent>] [--format <format>]] <input>
	// Parse flags, then take the last positional argument as the prompt.
	var model string
	var agent string
	pure := false
	input := ""
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "run":
			// skip subcommand
		case "--pure":
			pure = true
		case "--model":
			if i+1 < len(os.Args) {
				model = os.Args[i+1]
				i++
			}
		case "--agent":
			if i+1 < len(os.Args) {
				agent = os.Args[i+1]
				i++
			}
		case "--format":
			if os.Getenv("FAKEOPENCODE_REJECT_FORMAT_FLAG") == "1" {
				fmt.Fprintln(os.Stderr, "Error: unknown flag: --format")
				os.Exit(2)
			}
			if i+1 < len(os.Args) {
				i++ // e.g. "json"; the fake always emits NDJSON now
			}
		default:
			input = os.Args[i]
		}
	}

	// Model-level behaviour: checked before input-level sentinels so that
	// ProbeModel tests can trigger model-specific outcomes via --model flag.
	if model == "badmodel" {
		fmt.Fprintln(os.Stderr, "There's an issue with the selected model (badmodel). It may not exist or you may not have access to it.")
		os.Exit(1)
	}
	if model == "hangmodel" {
		// Simulates a model probe that never responds — killed by ctx deadline.
		time.Sleep(24 * time.Hour)
	}

	// Empty prompt: simulate CLI behaviour for ProbeModel.
	// Valid model + empty prompt → "Input must be provided" error (exit 1).
	if input == "" {
		fmt.Fprintln(os.Stderr, "Error: Input must be provided either through stdin or as a prompt argument")
		os.Exit(1)
	}

	switch input {
	case "fail":
		os.Exit(1)

	case "fail-stderr":
		// Exits non-zero AND writes to stderr — used to test stderr capture.
		fmt.Fprintln(os.Stderr, "simulated stderr output from failed subprocess")
		os.Exit(1)

	case "hang":
		time.Sleep(24 * time.Hour)

	case "large":
		// Emit 1 MiB of raw data so the adapter's io.LimitReader is triggered and takes
		// the truncation-marker path (which bypasses NDJSON parsing entirely).
		fmt.Print(strings.Repeat("x", 1<<20))

	case "raw-text":
		// Emit plain, non-NDJSON stdout and exit 0, so the adapter's raw-text fallback
		// is the only way to preserve the output. Exercises the "not an event stream
		// at all" branch of the parser's recognised flag.
		fmt.Println("plain text, not an event stream")

	case "error-event":
		// Emit a well-formed event stream that carries NO text part: a step_start, the
		// real captured error-event shape, and a step_finish. The error envelope below
		// is the actual shape the CLI writes to stdout.
		//
		// Exits 0 deliberately. In real runs an error event accompanies a non-zero exit
		// (which the adapter already maps to a failure before parsing), so this sentinel
		// is a parser-level probe of the recognised/empty-text branch, NOT a claim that
		// the real CLI exits 0 here. It proves the envelope is never concatenated into
		// Output when extraction legitimately yields nothing.
		emitNDJSONErrorStream()

	default:
		if os.Getenv("FAKEOPENCODE_CALL_MCP") == "1" {
			contactMCP(true)
		} else if os.Getenv("FAKEOPENCODE_CONNECT_MCP_ONLY") == "1" {
			contactMCP(false)
		}
		output := input
		if agent != "" {
			output = "agent:" + agent + "|" + output
		}
		if model != "" {
			output = "model:" + model + "|" + output
		}
		if pure {
			output = "pure:1|" + output
		}
		emitNDJSONText(output)
	}
}

// streamEnvelope mirrors the real opencode `--format json` NDJSON event envelope,
// captured from opencode 1.18.31 via `opencode run --format json "Reply with exactly: OK"`.
// Every event — step_start, text, step_finish — carries the SAME top-level shape
// (type, timestamp, sessionID, part) and the text content lives at part.text.
// There is no top-level "text" field on any event.
//
// This fake deliberately emits the full envelope rather than a convenient simplified
// shape: a test double that mirrors the adapter's own assumptions instead of the CLI's
// real output lets a broken parser stay green, which is exactly how the top-level-"text"
// parsing bug survived its own test suite.
type streamEnvelope struct {
	Type      string     `json:"type"`
	Timestamp int64      `json:"timestamp"`
	SessionID string     `json:"sessionID"`
	Part      streamPart `json:"part"`
}

// streamPart is the nested per-event payload. Fields absent from a given event type
// are omitted, matching the real stream (e.g. only text parts carry "text"/"time",
// only step_finish parts carry "cost"/"reason"/"tokens").
type streamPart struct {
	ID        string      `json:"id"`
	MessageID string      `json:"messageID"`
	SessionID string      `json:"sessionID"`
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	Time      *partTime   `json:"time,omitempty"`
	Cost      *float64    `json:"cost,omitempty"`
	Reason    string      `json:"reason,omitempty"`
	Tokens    *partTokens `json:"tokens,omitempty"`
}

type partTime struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type partTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// emitNDJSONText prints the real three-line opencode event stream for a single
// assistant turn: step_start, text (content at part.text), step_finish.
// The adapter must extract exactly `text` from this and nothing else — not the
// envelope, not the surrounding lifecycle events.
func emitNDJSONText(text string) {
	const (
		sessionID = "ses_fakeopencode"
		messageID = "msg_fakeopencode"
	)
	ts := time.Now().UnixMilli()
	cost := 0.0

	events := []streamEnvelope{
		{
			Type: "step_start", Timestamp: ts, SessionID: sessionID,
			Part: streamPart{ID: "prt_step_start", MessageID: messageID, SessionID: sessionID, Type: "step_start"},
		},
		{
			Type: "text", Timestamp: ts, SessionID: sessionID,
			Part: streamPart{
				ID: "prt_text", MessageID: messageID, SessionID: sessionID, Type: "text",
				Text: text, Time: &partTime{Start: ts, End: ts},
			},
		},
		{
			Type: "step_finish", Timestamp: ts, SessionID: sessionID,
			Part: streamPart{
				ID: "prt_step_finish", MessageID: messageID, SessionID: sessionID, Type: "step_finish",
				Cost: &cost, Reason: "stop", Tokens: &partTokens{Input: 1, Output: 1},
			},
		},
	}

	for _, ev := range events {
		line, err := json.Marshal(ev)
		if err != nil {
			// Last-resort: never silently emit nothing, or the adapter's raw-text
			// fallback assertion would pass for the wrong reason.
			fmt.Print(text)
			return
		}
		fmt.Println(string(line))
	}
}

// emitNDJSONErrorStream prints a well-formed event stream that contains no text part.
// The error line mirrors the real captured shape, which shares the same top-level
// envelope as every other event but carries an "error" object instead of a text part:
//
//	{"type":"error","timestamp":...,"sessionID":...,
//	 "error":{"name":"...","data":{"message":"...","ref":"..."}}}
func emitNDJSONErrorStream() {
	const sessionID = "ses_fakeopencode"
	ts := time.Now().UnixMilli()

	start, err := json.Marshal(streamEnvelope{
		Type: "step_start", Timestamp: ts, SessionID: sessionID,
		Part: streamPart{ID: "prt_step_start", MessageID: "msg_fakeopencode", SessionID: sessionID, Type: "step_start"},
	})
	if err != nil {
		return
	}
	fmt.Println(string(start))

	errLine, err := json.Marshal(map[string]any{
		"type":      "error",
		"timestamp": ts,
		"sessionID": sessionID,
		"error": map[string]any{
			"name": "ProviderAuthError",
			"data": map[string]any{"message": "simulated provider error", "ref": "err_fakeopencode"},
		},
	})
	if err != nil {
		return
	}
	fmt.Println(string(errLine))
}

// contactMCP reads the MCP config the adapter placed in OPENCODE_CONFIG_CONTENT and
// connects to the MCP server it describes. When callTool is true it also calls the
// "telegram_send" tool once; when false it completes the handshake and stops, simulating
// an agent that reached the server but chose not to call any tool.
// If the "framework" entry's "enabled" field is false, it does not connect at all — this
// mirrors the real opencode CLI, which never loads a disabled config entry, so the
// adapter's never-contacted guard is what must fire in that case.
// Errors are swallowed — the test asserts on the server-side state, not this call.
func contactMCP(callTool bool) {
	raw := os.Getenv("OPENCODE_CONFIG_CONTENT")
	if raw == "" {
		return
	}
	var cfg opencodeMCPConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return
	}
	entry, ok := cfg.MCP["framework"]
	if !ok {
		return
	}
	if !entry.Enabled {
		// Mirrors the real opencode CLI: a disabled entry in the config schema is never
		// loaded, so a CLI that received "enabled": false never contacts the server at
		// all — same as if the entry were missing.
		return
	}
	token := strings.TrimPrefix(entry.Headers["Authorization"], "Bearer ")

	transport := &gomcp.StreamableClientTransport{
		Endpoint:     entry.URL,
		OAuthHandler: &staticOAuthHandler{token: token},
	}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "fakeopencode"}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return
	}
	defer session.Close()

	if !callTool {
		return
	}

	_, _ = session.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "test-intent"},
	})
}
