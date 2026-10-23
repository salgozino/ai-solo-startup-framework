package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	transportmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
)

func ackText(t *testing.T, r *gomcp.CallToolResult) string {
	t.Helper()
	if r.IsError || len(r.Content) == 0 {
		t.Fatalf("expected a successful ack, got %+v", r)
	}
	return r.Content[0].(*gomcp.TextContent).Text
}

// T11: finish_task is exposed with an empty risk_policy and is recorded as an intent
// the supervisor can strip. It is a control tool, not a policy-classified action.
func TestFinishTask_ExposedWithoutPolicyAndRecorded(t *testing.T) {
	reg := transportmcp.NewRegistry()
	addr := startServer(t, transportmcp.New("acme", map[string]config.Policy{"telegram_send": {}}, reg))
	token, handle := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	if !names[port.KindFinishTask] || !names["telegram_send"] || len(names) != 2 {
		t.Fatalf("tools = %v, want telegram_send plus finish_task", names)
	}

	r, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: port.KindFinishTask, Arguments: map[string]any{"body": "all done"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// T12: the finish ack must say the task ends after this turn.
	if text := ackText(t, r); !strings.Contains(text, "ends after this turn") {
		t.Errorf("finish ack %q does not say the task ends after this turn", text)
	}
	intents := handle.Drain()
	if len(intents) != 1 || intents[0].Kind != port.KindFinishTask || intents[0].Payload["body"] != "all done" {
		t.Fatalf("intents = %+v, want one finish_task with the answer body", intents)
	}
}

// A policy that happens to declare finish_task must not double-register the name.
func TestFinishTask_PolicyCannotShadowControlTool(t *testing.T) {
	reg := transportmcp.NewRegistry()
	addr := startServer(t, transportmcp.New("acme", map[string]config.Policy{port.KindFinishTask: {}}, reg))
	token, _ := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
	tools, err := connectMCPClient(t, addr, token).ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("tools/err = %v/%v, want exactly one finish_task", tools, err)
	}
}

// T12: the ack must be truthful for both a delegation (result arrives next turn) and a
// plain action, and must no longer claim the outcome is unavailable / "do not call again".
func TestAck_TruthfulForDelegationAndAction(t *testing.T) {
	reg := transportmcp.NewRegistry()
	policies := map[string]config.Policy{port.KindDelegateTask: {}, "telegram_send": {}}
	addr := startServer(t, transportmcp.New("acme", policies, reg))
	token, _ := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	call := func(name string, args map[string]any) string {
		r, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		return ackText(t, r)
	}
	deleg := call(port.KindDelegateTask, map[string]any{"body": "x", port.TargetArg: "engineer"})
	if !strings.Contains(deleg, "next turn") {
		t.Errorf("delegation ack %q must say the result arrives in the next turn", deleg)
	}
	action := call("telegram_send", map[string]any{"body": "x"})
	if !strings.Contains(action, "Pending classification") {
		t.Errorf("action ack %q must say it is pending classification", action)
	}
	for _, text := range []string{deleg, action} {
		if strings.Contains(text, "unavailable this turn") {
			t.Errorf("ack %q still carries the stale pre-loop wording (outcome unavailable this turn)", text)
		}
	}
}

// Dedupe follow-up: the bearer token is minted per provider invocation (per round), so an
// identical delegation in a later round is a fresh call, not a duplicate.
func TestDedupe_IdenticalCallInLaterInvocationIsNotSwallowed(t *testing.T) {
	reg := transportmcp.NewRegistry()
	addr := startServer(t, transportmcp.New("acme", map[string]config.Policy{port.KindDelegateTask: {}}, reg))
	args := map[string]any{"body": "repeat", port.TargetArg: "engineer"}
	for round := 1; round <= 2; round++ {
		token, handle := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
		session := connectMCPClient(t, addr, token)
		r, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: port.KindDelegateTask, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("round %d: err=%v result=%+v", round, err, r)
		}
		if dup, _ := extractStructuredContent(t, r)["duplicate"].(bool); dup {
			t.Errorf("round %d: identical call across invocations flagged duplicate", round)
		}
		if got := len(handle.Drain()); got != 1 {
			t.Errorf("round %d: recorded %d intents, want 1", round, got)
		}
	}
}

const wantFinishRejected = "finish_task rejected: this turn delegated work. The peer's result arrives in your next turn; call finish_task then."

// S3: finish_task after a delegate_task intent in the same invocation is an MCP
// error with the exact rejection text, and records no finish intent. A finish in a
// later invocation (the next turn) is accepted, and a finish-only turn is unchanged.
func TestFinishTask_RejectedAfterDelegationInSameInvocation(t *testing.T) {
	reg := transportmcp.NewRegistry()
	addr := startServer(t, transportmcp.New("acme", map[string]config.Policy{port.KindDelegateTask: {}}, reg))
	token, handle := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
	session := connectMCPClient(t, addr, token)

	if r, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: port.KindDelegateTask, Arguments: map[string]any{"body": "x", port.TargetArg: "engineer"}}); err != nil || r.IsError {
		t.Fatalf("delegate: err=%v result=%+v", err, r)
	}
	r, err := session.CallTool(context.Background(), &gomcp.CallToolParams{Name: port.KindFinishTask, Arguments: map[string]any{"body": "all done"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !r.IsError || len(r.Content) != 1 {
		t.Fatalf("finish after delegation = %+v, want one isError content", r)
	}
	if got := r.Content[0].(*gomcp.TextContent).Text; got != wantFinishRejected {
		t.Errorf("rejection text = %q, want %q", got, wantFinishRejected)
	}
	intents := handle.Drain()
	if len(intents) != 1 || intents[0].Kind != port.KindDelegateTask {
		t.Fatalf("intents = %+v, want only the delegate_task (no finish recorded)", intents)
	}

	// Next turn = new invocation token: finish is accepted again.
	token2, handle2 := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
	r2, err := connectMCPClient(t, addr, token2).CallTool(context.Background(), &gomcp.CallToolParams{Name: port.KindFinishTask, Arguments: map[string]any{"body": "all done"}})
	if err != nil || r2.IsError {
		t.Fatalf("finish in a later invocation: err=%v result=%+v", err, r2)
	}
	if got := handle2.Drain(); len(got) != 1 || got[0].Kind != port.KindFinishTask {
		t.Errorf("intents = %+v, want the finish recorded", got)
	}
}

// S3: the tool description tells the model a finish in a delegating turn is rejected.
func TestFinishTask_DescriptionSaysDelegatingTurnIsRejected(t *testing.T) {
	reg := transportmcp.NewRegistry()
	addr := startServer(t, transportmcp.New("acme", map[string]config.Policy{}, reg))
	token, _ := reg.Mint("acme", "ceo", "t1", time.Now().Add(time.Hour))
	tools, err := connectMCPClient(t, addr, token).ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) != 1 {
		t.Fatalf("tools/err = %v/%v", tools, err)
	}
	desc := tools.Tools[0].Description
	if !strings.Contains(desc, "rejected") || strings.Contains(desc, "even if you also delegated") {
		t.Errorf("finish_task description = %q, want it to say a finish in a delegating turn is rejected", desc)
	}
}
