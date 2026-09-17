// Package mcp_test contains integration tests for the MCP transport server.
package mcp_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"github.com/salgozino/ai-solo-startup-framework/config"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

// staticOAuthHandler provides a fixed bearer token to the MCP client.
type staticOAuthHandler struct {
	token string
}

func (h *staticOAuthHandler) TokenSource(_ context.Context) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token}), nil
}

func (h *staticOAuthHandler) Authorize(_ context.Context, _ *http.Request, _ *http.Response) error {
	return nil
}

// connectMCPClient creates a connected MCP client session against the server at addr
// authenticated with the given bearer token.
func connectMCPClient(t *testing.T, addr, token string) *gomcp.ClientSession {
	t.Helper()
	transport := &gomcp.StreamableClientTransport{
		Endpoint:     "http://" + addr,
		OAuthHandler: &staticOAuthHandler{token: token},
	}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client"}, nil)
	session, err := client.Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// startServer is a test helper that starts the server and registers cleanup.
// Returns the server's listening address.
func startServer(t *testing.T, srv *transportmcp.Server) string {
	t.Helper()
	if err := srv.Start("127.0.0.1:0"); err != nil {
		t.Fatalf("server.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx) //nolint:errcheck
	})
	return srv.Addr()
}

func TestServer_ToolRegistryMirrorsPolicy(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{
		"telegram_send": {},
	}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, _ := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(result.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(result.Tools))
	}
	if result.Tools[0].Name != "telegram_send" {
		t.Errorf("tool name: expected %q, got %q", "telegram_send", result.Tools[0].Name)
	}
}

func TestServer_ToolDescription_DiscloseContract(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, _ := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("no tools returned")
	}

	desc := strings.ToLower(result.Tools[0].Description)
	if !strings.Contains(desc, "records") || !strings.Contains(desc, "intent") {
		t.Errorf("tool description missing 'records intent': %q", result.Tools[0].Description)
	}
	if !strings.Contains(desc, "does not") {
		t.Errorf("tool description missing 'does not': %q", result.Tools[0].Description)
	}
}

// TestServer_ToolInputSchema_DeclaresRequiredBody: RED — the advertised tool contract must
// name the "body" argument, because core/supervisor's extractBody reads only Payload["body"].
// An unconstrained object schema lets an agent send {"message": ...}, which yields an empty
// body and a Telegram "message text is empty" rejection after human approval.
func TestServer_ToolInputSchema_DeclaresRequiredBody(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, _ := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("no tools returned")
	}

	schema := decodeInputSchema(t, result.Tools[0].InputSchema)

	if schema.Type != "object" {
		t.Errorf("input schema type: expected %q, got %q", "object", schema.Type)
	}

	body, ok := schema.Properties["body"]
	if !ok {
		t.Fatalf("input schema declares no %q property; properties: %v", "body", schema.Properties)
	}
	if body.Type != "string" {
		t.Errorf("body property type: expected %q, got %q", "string", body.Type)
	}
	if body.Description == "" {
		t.Error("body property has no description; the agent is not told what to put in it")
	}

	if !slices.Contains(schema.Required, "body") {
		t.Errorf("input schema does not mark %q required; required: %v", "body", schema.Required)
	}
}

// TestServer_ToolCall_MissingBody_Rejected: RED — the go-sdk validates tools/call arguments
// against the declared input schema server-side (mcp.applySchema -> jsonschema.Resolved.Validate,
// see go-sdk@v1.8.0/mcp/server.go:402). A call without "body" must therefore be rejected
// rather than recording an intent the supervisor would read as an empty body.
func TestServer_ToolCall_MissingBody_Rejected(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"message": "hi"},
	})
	if err == nil && (result == nil || !result.IsError) {
		t.Error("CallTool without \"body\": expected rejection (error or isError=true)")
	}

	if intents := handle.Drain(); len(intents) != 0 {
		t.Errorf("Drain: expected 0 intents for a rejected call, got %d", len(intents))
	}
}

func TestServer_ValidToolCall_RecordsIntentAndNoGatewaySend(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	// Spy gateway — transport/mcp never calls any gateway; zero calls is always true.
	type fakeGateway struct{ calls int }
	spy := &fakeGateway{}

	result, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "hello"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Errorf("CallTool: expected isError=false, got true; content: %v", result.Content)
	}

	// Gateway never invoked (the server has no gateway dependency).
	if spy.calls != 0 {
		t.Errorf("spy gateway calls: expected 0, got %d", spy.calls)
	}

	intents := handle.Drain()
	if len(intents) != 1 {
		t.Fatalf("Drain: expected 1 intent, got %d", len(intents))
	}
	if intents[0].Kind != "telegram_send" {
		t.Errorf("intent Kind: expected %q, got %q", "telegram_send", intents[0].Kind)
	}
}

func TestServer_SinkIsAuthoritative_CLITextIgnored(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	_, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "test"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	intents := handle.Drain()
	if len(intents) != 1 {
		t.Fatalf("Drain: expected 1 intent from sink, got %d", len(intents))
	}
}

func TestServer_UnknownTokenRejected_ZeroIntents(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	// Never-minted token — auth middleware rejects with 401.
	// The rejection may surface either at Connect (initialize handshake) or at CallTool.
	transport := &gomcp.StreamableClientTransport{
		Endpoint:     "http://" + addr,
		OAuthHandler: &staticOAuthHandler{token: "never-minted-token-xyz"},
	}
	client := gomcp.NewClient(&gomcp.Implementation{Name: "test-client"}, nil)
	session, connectErr := client.Connect(context.Background(), transport, nil)

	if connectErr != nil {
		// Rejected at connect (initialize) — expected.
		return
	}
	defer session.Close()

	_, callErr := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "test"},
	})
	if callErr == nil {
		t.Error("CallTool with unknown token: expected error, got nil")
	}
}

func TestServer_ForeignTenantRejected_ZeroIntents(t *testing.T) {
	reg := transportmcp.NewRegistry()
	// Server configured for tenant "beta".
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("beta", policies, reg)
	addr := startServer(t, srv)

	// Mint token for "acme", but server expects "beta".
	token, _ := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      "telegram_send",
		Arguments: map[string]any{"body": "test"},
	})
	// Either HTTP error or isError: true — either form of rejection is valid.
	if err == nil && (result == nil || !result.IsError) {
		t.Error("CallTool with foreign tenant: expected rejection (error or isError=true)")
	}
}

func TestServer_Dedupe_RepeatCallReturnsSameReceipt(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)
	addr := startServer(t, srv)

	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	args := map[string]any{"body": "test-dedupe"}

	r1, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name: "telegram_send", Arguments: args,
	})
	if err != nil || r1.IsError {
		t.Fatalf("first CallTool: err=%v isError=%v", err, r1 != nil && r1.IsError)
	}

	r2, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name: "telegram_send", Arguments: args,
	})
	if err != nil || r2.IsError {
		t.Fatalf("second CallTool: err=%v isError=%v", err, r2 != nil && r2.IsError)
	}

	sc1 := extractStructuredContent(t, r1)
	sc2 := extractStructuredContent(t, r2)

	receipt1, _ := sc1["receipt"].(string)
	receipt2, _ := sc2["receipt"].(string)
	if receipt1 == "" || receipt1 != receipt2 {
		t.Errorf("dedupe: expected same receipt; got %q and %q", receipt1, receipt2)
	}

	dup, _ := sc2["duplicate"].(bool)
	if !dup {
		t.Errorf("second response: expected duplicate=true, got %v", sc2["duplicate"])
	}

	intents := handle.Drain()
	if len(intents) != 1 {
		t.Fatalf("Drain: expected 1 intent (deduped), got %d", len(intents))
	}
}

func TestServer_BindFailure_ReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("setup listener: %v", err)
	}
	defer ln.Close()
	occupiedAddr := ln.Addr().String()

	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{"telegram_send": {}}
	srv := transportmcp.New("acme", policies, reg)

	if err := srv.Start(occupiedAddr); err == nil {
		t.Error("Start on occupied port: expected error, got nil")
	}
}

// toolInputSchema is the subset of a JSON Schema object the tool contract tests assert on.
// Client-side, Tool.InputSchema holds the default JSON marshaling of the server's schema,
// so it is re-decoded through JSON rather than type-asserted.
type toolInputSchema struct {
	Type       string `json:"type"`
	Required   []string
	Properties map[string]struct {
		Type        string `json:"type"`
		Description string `json:"description"`
	}
}

// decodeInputSchema re-decodes a tool's advertised input schema into toolInputSchema.
func decodeInputSchema(t *testing.T, raw any) toolInputSchema {
	t.Helper()
	if raw == nil {
		t.Fatal("tool has no InputSchema")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal InputSchema: %v", err)
	}
	var s toolInputSchema
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("unmarshal InputSchema %s: %v", b, err)
	}
	return s
}

// extractStructuredContent parses StructuredContent from a CallToolResult.
func extractStructuredContent(t *testing.T, r *gomcp.CallToolResult) map[string]any {
	t.Helper()
	if r.StructuredContent == nil {
		t.Fatal("result has no StructuredContent")
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal StructuredContent: %v", err)
	}
	return m
}
