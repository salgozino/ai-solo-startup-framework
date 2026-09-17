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
// stdout as NDJSON now, so the default-case output below is always wrapped as a
// single `{"type":"text","text":"..."}` line.
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

// mcpConfigFile mirrors the MCP config JSON the opencode adapter puts in
// OPENCODE_CONFIG_CONTENT: {"mcpServers": {"framework": {"type": "http", "url": "...",
// "headers": {"Authorization": "Bearer ..."}}}}.
type mcpConfigFile struct {
	MCPServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
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

	default:
		if os.Getenv("FAKEOPENCODE_CALL_MCP") == "1" {
			callMCPTool()
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

// emitNDJSONText prints a single NDJSON line: {"type":"text","text":"..."},
// mimicking a simplified slice of the real opencode --format json event stream
// for text-only extraction by the adapter.
func emitNDJSONText(text string) {
	line, err := json.Marshal(map[string]string{"type": "text", "text": text})
	if err != nil {
		fmt.Print(text)
		return
	}
	fmt.Println(string(line))
}

// callMCPTool reads the MCP config the adapter placed in OPENCODE_CONFIG_CONTENT,
// connects to the MCP server it describes, and calls the "telegram_send" tool once.
// Errors are swallowed — the test asserts on the server-side sink, not this call.
func callMCPTool() {
	raw := os.Getenv("OPENCODE_CONFIG_CONTENT")
	if raw == "" {
		return
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return
	}
	entry, ok := cfg.MCPServers["framework"]
	if !ok {
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

	_, _ = session.CallTool(ctx, &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "test-intent"},
	})
}
