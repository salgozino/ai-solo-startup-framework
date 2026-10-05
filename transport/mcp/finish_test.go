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
