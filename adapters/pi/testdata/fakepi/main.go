// fakepi simulates the pi coding agent CLI (pi 1.2.0) for unit testing the pi adapter.
//
// Run mode (`-p --mode json ...`): the prompt is read from stdin (never argv) and the
// fake emits the real pi JSONL envelope: a session header followed by agent_start,
// turn_start and message_start/message_end events. The prompt, trimmed, selects a sentinel:
//
//	"fail"        — exits 1, prints nothing
//	"fail-stderr" — writes to stderr, exits 1
//	"hang"        — sleeps until SIGKILL
//	"large"       — prints 1 MiB of 'x' then exits 0
//	"raw-text"    — prints plain non-JSONL text and exits 0
//	"model-error" — assistant message_end with stopReason "error" and errorMessage, EXIT 0
//	"aborted"     — assistant message_end with stopReason "aborted", exit 0
//	anything else — echoes the prompt as the assistant text (after a thinking block)
//
// Test hooks (all optional, set via env):
//
//	FAKEPI_ARGV_FILE=<path>      writes os.Args (newline-separated) before anything else.
//	FAKEPI_STDIN_FILE=<path>     writes the raw stdin bytes received.
//	FAKEPI_ENV_FILE=<path>       writes "ASF_MCP_URL\nASF_MCP_TOKEN" as this process received them.
//	FAKEPI_EXT_FILE=<path>       writes "<path>\n<octal mode>\n<content>" of the `-e <file>.ts` entry.
//	FAKEPI_CALL_MCP=1            reads ASF_MCP_URL/ASF_MCP_TOKEN (what the real extension reads),
//	                             requires the `-e <file>` extension to exist, connects and calls
//	                             telegram_send.
//	FAKEPI_CONNECT_MCP_ONLY=1    same but stops after the handshake.
//	FAKEPI_PROBE_HANG=1          sleeps forever on --version / --list-models.
//
// `--list-models` prints a fixed table; the claude-bridge provider appears only when an
// `-e` entry ending in pi-claude-bridge is passed.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// staticOAuthHandler feeds a fixed bearer token to the go-sdk MCP client.
type staticOAuthHandler struct{ token string }

func (h *staticOAuthHandler) TokenSource(_ context.Context) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token}), nil
}

func (h *staticOAuthHandler) Authorize(_ context.Context, _ *http.Request, _ *http.Response) error {
	return nil
}

func main() {
	if f := os.Getenv("FAKEPI_ARGV_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(strings.Join(os.Args, "\n")), 0o600)
	}

	var extensions []string
	listModels, version := false, false
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-e":
			if i+1 < len(os.Args) {
				extensions = append(extensions, os.Args[i+1])
				i++
			}
		case "--list-models":
			listModels = true
		case "--version":
			version = true
		}
	}

	if version || listModels {
		if os.Getenv("FAKEPI_PROBE_HANG") == "1" {
			time.Sleep(24 * time.Hour)
		}
		if version {
			fmt.Println("1.2.0")
			return
		}
		printModelTable(extensions)
		return
	}

	stdin, _ := io.ReadAll(os.Stdin)
	if f := os.Getenv("FAKEPI_STDIN_FILE"); f != "" {
		_ = os.WriteFile(f, stdin, 0o600)
	}
	if f := os.Getenv("FAKEPI_ENV_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(os.Getenv("ASF_MCP_URL")+"\n"+os.Getenv("ASF_MCP_TOKEN")), 0o600)
	}
	extPath := extensionFile(extensions)
	if f := os.Getenv("FAKEPI_EXT_FILE"); f != "" && extPath != "" {
		if st, err := os.Stat(extPath); err == nil {
			content, _ := os.ReadFile(extPath)
			info := fmt.Sprintf("%s\n%o\n%s", extPath, st.Mode().Perm(), content)
			_ = os.WriteFile(f, []byte(info), 0o600)
		}
	}

	input := strings.TrimSpace(string(stdin))
	switch input {
	case "fail":
		os.Exit(1)
	case "fail-stderr":
		fmt.Fprintln(os.Stderr, "simulated stderr output from failed subprocess")
		os.Exit(1)
	case "hang":
		time.Sleep(24 * time.Hour)
	case "large":
		fmt.Print(strings.Repeat("x", 1<<20))
	case "raw-text":
		fmt.Println("plain text, not an event stream")
	case "model-error":
		emitHeader(input)
		emit(map[string]any{"type": "message_end", "message": map[string]any{
			"role": "assistant", "content": []any{}, "stopReason": "error",
			"errorMessage": "Codex error: The 'gpt-5.4-mini' model is not supported when using Codex with a ChatGPT account.",
		}})
		emitTail()
	case "aborted":
		emitHeader(input)
		emit(map[string]any{"type": "message_end", "message": map[string]any{
			"role": "assistant", "content": []any{}, "stopReason": "aborted",
			"errorMessage": "Request was aborted",
		}})
		emitTail()
	default:
		callTool := os.Getenv("FAKEPI_CALL_MCP") == "1"
		if callTool || os.Getenv("FAKEPI_CONNECT_MCP_ONLY") == "1" {
			contactMCP(extPath, callTool)
		}
		emitHeader(string(stdin))
		if callTool {
			emit(map[string]any{"type": "message_end", "message": map[string]any{
				"role": "assistant", "stopReason": "toolUse", "content": []any{
					map[string]any{"type": "toolCall", "id": "call_1", "name": "mcp__framework__telegram_send",
						"arguments": map[string]any{"body": "hello owner"}},
				},
			}})
			emit(map[string]any{"type": "message_end", "message": map[string]any{
				"role": "toolResult", "toolCallId": "call_1", "toolName": "mcp__framework__telegram_send",
				"content": []any{map[string]any{"type": "text", "text": "TOOLRESULT-NOISE"}},
			}})
		}
		emit(map[string]any{"type": "message_end", "message": map[string]any{
			"role": "assistant", "stopReason": "stop", "provider": "openai-codex", "model": "gpt-6-luna",
			"content": []any{
				map[string]any{"type": "thinking", "thinking": "THINKING-NOISE"},
				map[string]any{"type": "text", "text": input, "textSignature": "sig"},
			},
		}})
		emitTail()
	}
}

// extensionFile returns the `-e` entry that is a file path (not builtin: and not a package).
func extensionFile(exts []string) string {
	for _, e := range exts {
		if strings.HasSuffix(e, ".ts") {
			return e
		}
	}
	return ""
}

func emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	fmt.Println(string(b))
}

// emitHeader prints the real pi preamble: session header, agent/turn start and the
// user message echo (which the adapter must never include in Output).
func emitHeader(prompt string) {
	emit(map[string]any{"type": "session", "version": 3, "id": "ses_fakepi", "timestamp": "2026-10-22T00:00:00.000Z", "cwd": "/tmp"})
	emit(map[string]any{"type": "agent_start"})
	emit(map[string]any{"type": "turn_start"})
	userMsg := map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "USER:" + strings.TrimSpace(prompt)}}}
	emit(map[string]any{"type": "message_start", "message": userMsg})
	emit(map[string]any{"type": "message_end", "message": userMsg})
}

func emitTail() {
	emit(map[string]any{"type": "turn_end"})
	emit(map[string]any{"type": "agent_end"})
	emit(map[string]any{"type": "agent_settled"})
}

func printModelTable(exts []string) {
	fmt.Println("provider      model           context  max-out  thinking  images")
	fmt.Println("openai-codex  gpt-6-luna      400K     128K     yes       yes")
	fmt.Println("openai-codex  gpt-5.4-mini    400K     128K     yes       yes")
	fmt.Println("anthropic     claude-opus-5-5 1M       64K      yes       yes")
	for _, e := range exts {
		if strings.HasSuffix(e, "pi-claude-bridge") {
			fmt.Println("claude-bridge claude-haiku-4-5 200K     64K      yes       yes")
		}
	}
}

// contactMCP does what the real extension + builtin:mcp would: read ASF_MCP_URL and
// ASF_MCP_TOKEN from env and connect. It refuses to connect when the `-e <file>`
// extension does not exist, mirroring the fact that without it pi has no server entry.
func contactMCP(extPath string, callTool bool) {
	if extPath == "" {
		return
	}
	if _, err := os.Stat(extPath); err != nil {
		return
	}
	url, token := os.Getenv("ASF_MCP_URL"), os.Getenv("ASF_MCP_TOKEN")
	if url == "" {
		return
	}
	transport := &gomcp.StreamableClientTransport{Endpoint: url, OAuthHandler: &staticOAuthHandler{token: token}}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "fakepi"}, nil)
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
	_, _ = session.CallTool(ctx, &gomcp.CallToolParams{Name: "telegram_send", Arguments: map[string]any{"body": "test-intent"}})
}
