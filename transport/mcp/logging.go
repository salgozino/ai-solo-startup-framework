package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"strings"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Option configures a Server at construction time.
// Options are variadic on New so every existing caller keeps compiling.
type Option func(*options)

// options holds the resolved construction-time configuration for a Server.
type options struct {
	logger *slog.Logger
}

// defaultOptions returns the configuration used when no Option is supplied.
// The logger matches core/supervisor's fallback (structured JSON on stderr), so MCP
// request records land in the same stream the operator already reads for runtask events.
func defaultOptions() options {
	return options{logger: slog.New(slog.NewJSONHandler(os.Stderr, nil))}
}

// WithLogger sets the structured logger used for MCP request records.
// A nil logger is ignored, keeping the default stderr JSON logger rather than
// silently discarding the records that exist to diagnose missing tool calls.
func WithLogger(logger *slog.Logger) Option {
	return func(o *options) {
		if logger != nil {
			o.logger = logger
		}
	}
}

// requestLogEvent is the slog message shared by every MCP request record, matching the
// lowercase dotted event naming used elsewhere (see core/supervisor's "runtask.done").
const requestLogEvent = "mcp.request"

// requestLoggingMiddleware returns a go-sdk receiving middleware that emits one
// requestLogEvent record per incoming MCP method.
//
// Receiving middleware wraps the receiving method handler
// (go-sdk@v1.8.0/mcp/server.go:1860 -> mcp/shared.go:133 addMiddleware), and that handler
// dispatches tools/call into (*Server).callTool (mcp/server.go:1005), which invokes the
// tool handler built by AddTool. Input-schema validation happens *inside* that handler
// (mcp/server.go:402, applySchema -> CallToolResult.SetError), before the typed handler
// runs. A schema rejection is therefore an ordinary *CallToolResult with IsError=true
// flowing back out through this middleware — which is exactly why a rejected call is
// observable here even though no intent was ever recorded.
//
// Privacy contract: this never logs the bearer token, anything derived from it, or any
// argument VALUE. Argument KEY NAMES are logged because they answer the diagnostic
// question ("did the model send body, or message, or nothing?") without putting user
// message content into the process log.
func requestLoggingMiddleware(logger *slog.Logger, tenant string) gomcp.Middleware {
	return func(next gomcp.MethodHandler) gomcp.MethodHandler {
		return func(ctx context.Context, method string, req gomcp.Request) (gomcp.Result, error) {
			res, err := next(ctx, method, req)

			attrs := []any{"tenant", tenant, "method", method}
			// CallToolRequest is ServerRequest[*CallToolParamsRaw], so tools/call params
			// arrive here with their arguments still raw JSON.
			if params, ok := req.GetParams().(*gomcp.CallToolParamsRaw); ok && params != nil {
				attrs = append(attrs, "tool", params.Name, "arg_keys", argumentKeys(params.Arguments))
			}

			switch {
			case err != nil:
				// Transport- or protocol-level failure: the method handler never produced
				// a result (e.g. unknown tool, auth rejection).
				attrs = append(attrs, "outcome", "error", "error", err.Error())
				logger.Error(requestLogEvent, attrs...)

			case isErrorResult(res):
				// Tool-level failure, including go-sdk input-schema validation. The text
				// content carries the SDK's own message, e.g. `validating "arguments": ...`.
				callRes := res.(*gomcp.CallToolResult)
				attrs = append(attrs, "outcome", "tool_error", "error", resultErrorText(callRes))
				logger.Warn(requestLogEvent, attrs...)

			default:
				attrs = append(attrs, "outcome", "ok")
				if callRes, ok := res.(*gomcp.CallToolResult); ok {
					attrs = append(attrs, "intent_recorded", intentRecorded(callRes))
				}
				logger.Info(requestLogEvent, attrs...)
			}

			return res, err
		}
	}
}

// argumentKeys returns the sorted top-level key names of a raw tools/call argument
// object. Values are never read. It returns an empty slice when no arguments were sent,
// and nil when the arguments are not a JSON object (rendered as null, so "the caller sent
// something that is not an argument object" stays distinguishable from "no arguments").
func argumentKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// isErrorResult reports whether res is a tool result flagged as an error.
func isErrorResult(res gomcp.Result) bool {
	callRes, ok := res.(*gomcp.CallToolResult)
	return ok && callRes != nil && callRes.IsError
}

// resultErrorText joins the text content of an error tool result. This is SDK- or
// handler-authored diagnostic text (validation messages, "unauthorized: ...",
// "intent sink full: ..."), never caller-supplied argument values.
func resultErrorText(res *gomcp.CallToolResult) string {
	parts := make([]string, 0, len(res.Content))
	for _, c := range res.Content {
		if text, ok := c.(*gomcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	return strings.Join(parts, "; ")
}

// intentRecorded reports whether a successful tools/call actually added an intent to the
// sink. It reads the acknowledgement's StructuredContent (see buildAckResult): a duplicate
// returns the original receipt without recording anything new.
func intentRecorded(res *gomcp.CallToolResult) bool {
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		return false
	}
	if status, _ := sc["status"].(string); status != "recorded" {
		return false
	}
	duplicate, _ := sc["duplicate"].(bool)
	return !duplicate
}
