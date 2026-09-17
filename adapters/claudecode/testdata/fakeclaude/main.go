// fakeclaude simulates the claude CLI for unit testing the Claude Code adapter.
// It reads argv and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// When --safe-mode is present, it prepends "safe:1|" to the output.
// When --model is present, it prepends "model:<model>|" to the output.
// When --system-prompt-file is present, it prepends "sysprompt:<path>|" to the output.
// --no-session-persistence, --strict-mcp-config, --verbose are consumed silently.
// --mcp-config and --output-format consume their values but are otherwise ignored;
// the adapter always parses stdout as NDJSON now, so the default-case output below
// is always wrapped as a single `{"type":"text","text":"..."}` line.
//
// MCP-related test hooks (env vars, not argv — mirrors how the real adapter never
// puts the bearer token on argv):
//
//	FAKECLAUDE_DUMP_ARGV=1 + FAKECLAUDE_ARGV_FILE=<path>
//	    writes os.Args (newline-separated) to <path> before any other behavior.
//	FAKECLAUDE_DUMP_MCP_CONFIG_PATH=<path>
//	    writes the value of the FAKECLAUDE_MCP_CONFIG env var to <path>, letting
//	    tests observe the ephemeral config path the adapter set for this invocation.
//	FAKECLAUDE_CALL_MCP=1
//	    reads the ephemeral MCP config JSON from FAKECLAUDE_MCP_CONFIG, extracts the
//	    server URL and bearer token, and performs a real MCP tools/call for
//	    "telegram_send" before producing normal output.
//	FAKECLAUDE_CONNECT_MCP_ONLY=1
//	    same as FAKECLAUDE_CALL_MCP but stops after the MCP handshake, calling no tool.
//	    This represents the healthy "the agent reached the server and chose not to call a
//	    tool" case, which must stay distinguishable from never reaching the server at all.
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

// mcpConfigFile mirrors the ephemeral MCP config JSON written by the claudecode adapter:
//
//	{"mcpServers": {"framework": {"type": "http", "url": "...", "headers": {"Authorization": "Bearer ..."}}}}
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
	if os.Getenv("FAKECLAUDE_DUMP_ARGV") == "1" {
		if f := os.Getenv("FAKECLAUDE_ARGV_FILE"); f != "" {
			_ = os.WriteFile(f, []byte(strings.Join(os.Args, "\n")), 0o600)
		}
	}
	if dumpPath := os.Getenv("FAKECLAUDE_DUMP_MCP_CONFIG_PATH"); dumpPath != "" {
		_ = os.WriteFile(dumpPath, []byte(os.Getenv("FAKECLAUDE_MCP_CONFIG")), 0o600)
	}

	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "fakeclaude: expected -p <argument>")
		os.Exit(2)
	}

	// Args: [-p [--safe-mode] [--no-session-persistence] [--model <model>]
	//        [--system-prompt-file <path>] [--mcp-config <path>] [--strict-mcp-config]
	//        [--output-format <format>] [--verbose]] <input>
	// Parse flags, then take the last positional argument as the prompt.
	var model string
	var systemPromptFile string
	safeMode := false
	input := ""
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-p":
			// skip
		case "--safe-mode":
			safeMode = true
		case "--no-session-persistence", "--strict-mcp-config", "--verbose":
			// consumed silently
		case "--model":
			if i+1 < len(os.Args) {
				model = os.Args[i+1]
				i++
			}
		case "--system-prompt-file":
			if i+1 < len(os.Args) {
				systemPromptFile = os.Args[i+1]
				i++
			}
		case "--mcp-config":
			if i+1 < len(os.Args) {
				i++ // value consumed via FAKECLAUDE_MCP_CONFIG env instead
			}
		case "--output-format":
			if i+1 < len(os.Args) {
				i++ // e.g. "stream-json"; the fake always emits NDJSON now
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
		fmt.Fprintln(os.Stderr, "Error: Input must be provided either through stdin or as a prompt argument when using --print")
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
		// Sleep until killed — simulates a hung process.
		time.Sleep(24 * time.Hour)

	case "large":
		// Emit 1 MiB of raw data so the adapter's io.LimitReader is triggered and takes
		// the truncation-marker path (which bypasses NDJSON parsing entirely).
		fmt.Print(strings.Repeat("x", 1<<20))

	default:
		if os.Getenv("FAKECLAUDE_CALL_MCP") == "1" {
			contactMCP(true)
		} else if os.Getenv("FAKECLAUDE_CONNECT_MCP_ONLY") == "1" {
			contactMCP(false)
		}
		output := input
		if systemPromptFile != "" {
			output = "sysprompt:" + systemPromptFile + "|" + output
		}
		if model != "" {
			output = "model:" + model + "|" + output
		}
		if safeMode {
			output = "safe:1|" + output
		}
		emitNDJSONText(output)
	}
}

// emitNDJSONText prints a single NDJSON line matching what the real claude CLI's
// --output-format stream-json --verbose produces for text content: {"type":"text","text":"..."}.
func emitNDJSONText(text string) {
	line, err := json.Marshal(map[string]string{"type": "text", "text": text})
	if err != nil {
		// Should never happen for a plain string; fall back to raw text.
		fmt.Print(text)
		return
	}
	fmt.Println(string(line))
}

// contactMCP reads the ephemeral MCP config the adapter wrote and connects to the MCP
// server it describes. When callTool is true it also calls the "telegram_send" tool once;
// when false it completes the handshake and stops, simulating an agent that reached the
// server but chose not to call any tool. Errors are swallowed — this simulates a real CLI
// attempting the call; the test asserts on the server-side state, not on this outcome.
func contactMCP(callTool bool) {
	cfgPath := os.Getenv("FAKECLAUDE_MCP_CONFIG")
	if cfgPath == "" {
		return
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return
	}
	var cfg mcpConfigFile
	if err := json.Unmarshal(raw, &cfg); err != nil {
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
	client := gomcp.NewClient(&gomcp.Implementation{Name: "fakeclaude"}, nil)

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
