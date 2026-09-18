// Package mcp_test: observability tests for the MCP receiving middleware.
//
// These tests exist because a real end-to-end run produced "runtask.done ...
// action_intents_count: 0" while the agent's own output text claimed it had recorded an
// intent, and the process log could not tell "the model never attempted a tools/call"
// apart from "the model attempted one and the SDK rejected it during input-schema
// validation". Handle.Contacted cannot discriminate either: it is set by TokenVerifier,
// which runs for every authenticated HTTP request including the initialize handshake.
package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/salgozino/ai-solo-startup-framework/config"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

// capturedRecord is one slog record flattened into an assertable shape.
type capturedRecord struct {
	Level slog.Level
	Msg   string
	Attrs map[string]any
}

// captureHandler is an in-memory slog.Handler that appends every record to a slice.
// It is safe for concurrent use: the server writes records from its HTTP handler
// goroutine while the test reads them.
type captureHandler struct {
	mu    *sync.Mutex
	recs  *[]capturedRecord
	attrs []slog.Attr
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{mu: &sync.Mutex{}, recs: &[]capturedRecord{}}
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := capturedRecord{Level: r.Level, Msg: r.Message, Attrs: map[string]any{}}
	for _, a := range h.attrs {
		rec.Attrs[a.Key] = a.Value.Any()
	}
	r.Attrs(func(a slog.Attr) bool {
		rec.Attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	*h.recs = append(*h.recs, rec)
	h.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &captureHandler{mu: h.mu, recs: h.recs}
	next.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return next
}

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

// records returns a snapshot of everything captured so far.
func (h *captureHandler) records() []capturedRecord {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]capturedRecord{}, *h.recs...)
}

// requestRecords returns the captured "mcp.request" records for the given MCP method.
func (h *captureHandler) requestRecords(method string) []capturedRecord {
	var out []capturedRecord
	for _, r := range h.records() {
		if r.Msg == "mcp.request" && r.Attrs["method"] == method {
			out = append(out, r)
		}
	}
	return out
}

// startLoggingServer builds a server whose MCP request log is captured in memory.
func startLoggingServer(t *testing.T, tenant string) (*captureHandler, *transportmcp.Registry, string) {
	t.Helper()
	cap := newCaptureHandler()
	reg := transportmcp.NewRegistry()
	srv := transportmcp.New(
		tenant,
		map[string]config.Policy{"telegram_send": {}},
		reg,
		transportmcp.WithLogger(slog.New(cap)),
	)
	return cap, reg, startServer(t, srv)
}

// attrStrings reads an attribute expected to hold a list of strings.
func attrStrings(t *testing.T, rec capturedRecord, key string) []string {
	t.Helper()
	v, ok := rec.Attrs[key]
	if !ok {
		t.Fatalf("record has no %q attribute; attrs: %v", key, rec.Attrs)
	}
	got, ok := v.([]string)
	if !ok {
		t.Fatalf("attribute %q: expected []string, got %T (%v)", key, v, v)
	}
	return got
}

// TestServer_Logging_SchemaRejectedToolCall_Discriminates: RED — a tools/call rejected by
// go-sdk input-schema validation (go-sdk@v1.8.0/mcp/server.go:402, inside the tool handler
// and therefore inside the receiving middleware's call) must still produce a log record
// naming the method, the tool, and the argument keys the caller actually sent. This is the
// record that tells "the model called the tool with the wrong key" apart from "the model
// never called the tool at all".
func TestServer_Logging_SchemaRejectedToolCall_Discriminates(t *testing.T) {
	cap, reg, addr := startLoggingServer(t, "acme")

	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	// Wrong argument key: the schema requires "body".
	_, _ = session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"message": "hi"},
	})

	recs := cap.requestRecords("tools/call")
	if len(recs) != 1 {
		t.Fatalf("expected 1 %q log record, got %d; all records: %v", "tools/call", len(recs), cap.records())
	}
	rec := recs[0]

	if rec.Attrs["tool"] != "telegram_send" {
		t.Errorf("tool attribute: expected %q, got %v", "telegram_send", rec.Attrs["tool"])
	}

	keys := attrStrings(t, rec, "arg_keys")
	if len(keys) != 1 || keys[0] != "message" {
		t.Errorf("arg_keys: expected [message], got %v", keys)
	}

	if rec.Attrs["outcome"] != "tool_error" {
		t.Errorf("outcome: expected %q, got %v", "tool_error", rec.Attrs["outcome"])
	}

	errText, _ := rec.Attrs["error"].(string)
	if !strings.Contains(errText, `validating "arguments"`) {
		t.Errorf("error attribute does not carry the SDK validation message: %q", errText)
	}

	if intents := handle.Drain(); len(intents) != 0 {
		t.Errorf("Drain: expected 0 intents for a schema-rejected call, got %d", len(intents))
	}
}

// TestServer_Logging_SuccessfulToolCall_MarksIntentRecorded: RED — a successful tools/call
// must be logged as a success that actually recorded an intent, so the operator can read
// "the tool ran and the sink took it" straight from the process log.
func TestServer_Logging_SuccessfulToolCall_MarksIntentRecorded(t *testing.T) {
	cap, reg, addr := startLoggingServer(t, "acme")

	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool: expected success, got isError=true; content: %v", result.Content)
	}

	// The handshake is logged too — that is what proves "the CLI connected but never
	// called a tool" is visible as an initialize record with no tools/call record.
	if len(cap.requestRecords("initialize")) == 0 {
		t.Errorf("no %q log record; the middleware does not observe the handshake", "initialize")
	}

	recs := cap.requestRecords("tools/call")
	if len(recs) != 1 {
		t.Fatalf("expected 1 %q log record, got %d; all records: %v", "tools/call", len(recs), cap.records())
	}
	rec := recs[0]

	if rec.Attrs["tool"] != "telegram_send" {
		t.Errorf("tool attribute: expected %q, got %v", "telegram_send", rec.Attrs["tool"])
	}
	if rec.Attrs["outcome"] != "ok" {
		t.Errorf("outcome: expected %q, got %v", "ok", rec.Attrs["outcome"])
	}
	if recorded, _ := rec.Attrs["intent_recorded"].(bool); !recorded {
		t.Errorf("intent_recorded: expected true, got %v", rec.Attrs["intent_recorded"])
	}

	keys := attrStrings(t, rec, "arg_keys")
	if len(keys) != 1 || keys[0] != "body" {
		t.Errorf("arg_keys: expected [body], got %v", keys)
	}

	if intents := handle.Drain(); len(intents) != 1 {
		t.Errorf("Drain: expected 1 intent, got %d", len(intents))
	}
}

// TestServer_Logging_NeverLeaksTokenOrArgumentValues: RED — the log is a diagnostic
// channel, not a data channel. The bearer token must never appear, and argument VALUES
// must never appear: "body" carries user message content, and the key names alone already
// answer the diagnostic question.
func TestServer_Logging_NeverLeaksTokenOrArgumentValues(t *testing.T) {
	const sentinel = "SENTINEL-ARGUMENT-VALUE-e3f1a7c9-must-never-be-logged"

	cap, reg, addr := startLoggingServer(t, "acme")

	token, _ := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	// One accepted call and one rejected call, so both log branches are exercised.
	_, _ = session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": sentinel},
	})
	_, _ = session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"message": sentinel},
	})

	recs := cap.records()
	if len(recs) == 0 {
		t.Fatal("no log records captured; the leak assertions below would pass vacuously")
	}
	// Guard against a vacuous pass: the argument keys must really be reaching the log.
	if len(cap.requestRecords("tools/call")) != 2 {
		t.Fatalf("expected 2 %q records, got %d", "tools/call", len(cap.requestRecords("tools/call")))
	}

	blob, err := json.Marshal(recs)
	if err != nil {
		t.Fatalf("marshal captured records: %v", err)
	}
	dump := string(blob)

	if strings.Contains(dump, token) {
		t.Errorf("log contains the bearer token; records: %s", dump)
	}
	if strings.Contains(dump, sentinel) {
		t.Errorf("log contains an argument value; records: %s", dump)
	}
}
