// Package mcp_test: server-level test for the sink-full retry path.
package mcp_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

// TestServer_SinkFull_RetryDoesNotFalselySucceed: RED — a retry of a failed-to-record call must never report success.
func TestServer_SinkFull_RetryDoesNotFalselySucceed(t *testing.T) {
	reg := transportmcp.NewRegistry()
	srv := transportmcp.New("acme", map[string]config.Policy{"telegram_send": {}}, reg)
	addr := startServer(t, srv)
	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)
	call := func(args map[string]any) *gomcp.CallToolResult {
		r, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: "telegram_send", Arguments: args})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		return r
	}

	for i := range 100 {
		call(map[string]any{"body": fmt.Sprintf("fill-%d", i)})
	}
	overflow := map[string]any{"body": "overflow"}
	if r1 := call(overflow); !r1.IsError {
		t.Fatal("expected first overflow call to report IsError=true (sink full)")
	}
	// Retry with the SAME payload — same dedupe key as the failed call above.
	if r2 := call(overflow); !r2.IsError {
		t.Error("retry after sink-full: got success for an intent the sink never recorded")
	}
	if intents := handle.Drain(); len(intents) != 100 {
		t.Errorf("expected exactly 100 recorded intents, got %d", len(intents))
	}
}

// TestRegisterTools_DelegateTaskRecordsTargetAndBody — task 7.4/7.5.
// Spec: mcp-tool-server "Recording a delegation intent still does not execute or
// contact a peer". Calling delegate_task with {target, body} records both keys
// in the sink's ActionIntent and returns success synchronously; the server never
// holds a Delegator or contacts any peer as a direct result of this call.
func TestRegisterTools_DelegateTaskRecordsTargetAndBody(t *testing.T) {
	reg := transportmcp.NewRegistry()
	srv := transportmcp.New("acme", map[string]config.Policy{port.KindDelegateTask: {}}, reg)
	addr := startServer(t, srv)
	token, handle := reg.Mint("acme", "worker", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	result, err := session.CallTool(context.Background(), &gomcp.CallToolParams{
		Name:      port.KindDelegateTask,
		Arguments: map[string]any{port.TargetArg: "engineer", "body": "build X"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool: expected isError=false, got true; content: %v", result.Content)
	}

	intents := handle.Drain()
	if len(intents) != 1 {
		t.Fatalf("Drain: expected 1 intent, got %d", len(intents))
	}
	if intents[0].Kind != port.KindDelegateTask {
		t.Errorf("intent Kind = %q, want %q", intents[0].Kind, port.KindDelegateTask)
	}
	if got := intents[0].Payload[port.TargetArg]; got != "engineer" {
		t.Errorf("intent Payload[%q] = %v, want %q", port.TargetArg, got, "engineer")
	}
	if got := intents[0].Payload["body"]; got != "build X" {
		t.Errorf("intent Payload[body] = %v, want %q", got, "build X")
	}
}
