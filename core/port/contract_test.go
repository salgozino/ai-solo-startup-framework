// Package port_test contains the canonical contract test suite for port.Provider and port.Gateway.
// These tests run against fakeProvider and fakeGateway here; PR 5 (ClaudeCodeAdapter) and
// PR 6 (TelegramGateway) MUST import and run them against the real implementations.
package port_test

import (
	"context"
	"errors"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// ---- helpers ---------------------------------------------------------------

func mustAddr(t *testing.T, name, tenant string) address.A2AAddress {
	t.Helper()
	addr, err := address.New(name, tenant)
	if err != nil {
		t.Fatalf("mustAddr: %v", err)
	}
	return addr
}

// ---- Provider contract tests -----------------------------------------------

// TestProvider_Complete verifies Complete records the call and returns nil on success.
func TestProvider_Complete(t *testing.T) {
	fp := &fake.Provider{ReturnTaskID: "task-1"}
	result := port.TaskResult{Output: "done", Metadata: map[string]any{"k": "v"}}

	if err := fp.Complete("task-1", result); err != nil {
		t.Fatalf("Complete returned unexpected error: %v", err)
	}
	if fp.CompleteCallCount() != 1 {
		t.Fatalf("expected 1 Complete call, got %d", fp.CompleteCallCount())
	}
}

// TestProvider_Complete_Idempotent verifies that calling Complete twice does not error.
func TestProvider_Complete_Idempotent(t *testing.T) {
	fp := &fake.Provider{}
	result := port.TaskResult{Output: "done"}

	if err := fp.Complete("task-x", result); err != nil {
		t.Fatalf("first Complete: %v", err)
	}
	if err := fp.Complete("task-x", result); err != nil {
		t.Fatalf("second Complete (idempotent): %v", err)
	}
	// Both calls are recorded; nil return on both is the contract.
	if fp.CompleteCallCount() != 2 {
		t.Fatalf("expected 2 recorded Complete calls, got %d", fp.CompleteCallCount())
	}
}

// TestProvider_CompleteError verifies failure reporting records the call.
func TestProvider_CompleteError(t *testing.T) {
	fp := &fake.Provider{}
	agentErr := errors.New("provider: timeout")

	if err := fp.CompleteError("task-2", agentErr); err != nil {
		t.Fatalf("CompleteError returned unexpected error: %v", err)
	}
	if fp.CompleteCallCount() != 1 {
		t.Fatalf("expected 1 CompleteError call, got %d", fp.CompleteCallCount())
	}
}

// TestProvider_CompleteError_Idempotent verifies calling CompleteError twice is safe.
func TestProvider_CompleteError_Idempotent(t *testing.T) {
	fp := &fake.Provider{}
	agentErr := errors.New("provider: timeout")

	if err := fp.CompleteError("task-y", agentErr); err != nil {
		t.Fatalf("first CompleteError: %v", err)
	}
	if err := fp.CompleteError("task-y", agentErr); err != nil {
		t.Fatalf("second CompleteError (idempotent): %v", err)
	}
}

// TestProvider_SendMessage_NoWait verifies SendMessage(wait=false) returns taskID immediately.
func TestProvider_SendMessage_NoWait(t *testing.T) {
	fp := &fake.Provider{ReturnTaskID: "task-42"}
	target := mustAddr(t, "worker", "acme")

	taskID, err := fp.SendMessage(context.Background(), target, "hello", false)
	if err != nil {
		t.Fatalf("SendMessage(wait=false): %v", err)
	}
	if taskID != "task-42" {
		t.Fatalf("expected taskID %q, got %q", "task-42", taskID)
	}
	if fp.SendMessageCallCount() != 1 {
		t.Fatalf("expected 1 SendMessage call, got %d", fp.SendMessageCallCount())
	}
}

// TestProvider_SendMessage_Wait verifies SendMessage(wait=true) uses the same return path.
func TestProvider_SendMessage_Wait(t *testing.T) {
	fp := &fake.Provider{ReturnTaskID: "task-99"}
	target := mustAddr(t, "ceo", "acme")

	taskID, err := fp.SendMessage(context.Background(), target, "please analyze", true)
	if err != nil {
		t.Fatalf("SendMessage(wait=true): %v", err)
	}
	if taskID != "task-99" {
		t.Fatalf("expected %q, got %q", "task-99", taskID)
	}
}

// TestProvider_SendMessage_Error verifies error propagation.
func TestProvider_SendMessage_Error(t *testing.T) {
	fp := &fake.Provider{ReturnErr: errors.New("network: unreachable")}
	target := mustAddr(t, "worker", "acme")

	_, err := fp.SendMessage(context.Background(), target, "hi", false)
	if err == nil {
		t.Fatal("expected error from SendMessage, got nil")
	}
}

// TestProvider_SendMessageStream verifies the stream channel closes with a Done event.
func TestProvider_SendMessageStream(t *testing.T) {
	fp := &fake.Provider{
		ReturnStream: []port.StreamEvent{
			{TaskID: "t1", Text: "chunk 1"},
			{TaskID: "t1", Text: "chunk 2"},
		},
	}
	target := mustAddr(t, "worker", "acme")

	ch, err := fp.SendMessageStream(context.Background(), target, "stream me")
	if err != nil {
		t.Fatalf("SendMessageStream: %v", err)
	}

	var events []port.StreamEvent
	for e := range ch {
		events = append(events, e)
	}

	// Expect the two canned events plus the auto-appended Done sentinel.
	if len(events) < 3 {
		t.Fatalf("expected ≥3 events (2 content + 1 Done), got %d", len(events))
	}
	last := events[len(events)-1]
	if !last.Done {
		t.Fatalf("last event must be Done, got %+v", last)
	}
}

// TestProvider_SendMessageStream_Error verifies error return when ReturnErr is set.
func TestProvider_SendMessageStream_Error(t *testing.T) {
	fp := &fake.Provider{ReturnErr: errors.New("stream: broken")}
	target := mustAddr(t, "worker", "acme")

	ch, err := fp.SendMessageStream(context.Background(), target, "stream")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if ch != nil {
		t.Fatal("channel must be nil when error is returned")
	}
}

// TestProvider_ResolveAgent_Known verifies a configured address is returned.
func TestProvider_ResolveAgent_Known(t *testing.T) {
	expected := mustAddr(t, "worker", "acme")
	fp := &fake.Provider{ReturnAddress: expected}

	got, err := fp.ResolveAgent(context.Background(), "engineer")
	if err != nil {
		t.Fatalf("ResolveAgent(known): %v", err)
	}
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// TestProvider_ResolveAgent_Unknown verifies an error is returned for an unknown role.
func TestProvider_ResolveAgent_Unknown(t *testing.T) {
	fp := &fake.Provider{ReturnErr: errors.New("provider: role \"janitor\" not registered")}

	_, err := fp.ResolveAgent(context.Background(), "janitor")
	if err == nil {
		t.Fatal("expected error for unknown role, got nil")
	}
}

// TestProvider_SendTask_Basic verifies task dispatch records the call.
func TestProvider_SendTask_Basic(t *testing.T) {
	fp := &fake.Provider{ReturnTaskID: "task-dispatch-1"}
	target := mustAddr(t, "worker", "acme")
	input := map[string]any{"prompt": "analyze this"}
	opts := &port.TaskOptions{Tenant: "acme", BudgetTokens: 1000, Blocking: true}

	taskID, err := fp.SendTask(context.Background(), target, "analyze", input, opts)
	if err != nil {
		t.Fatalf("SendTask: %v", err)
	}
	if taskID != "task-dispatch-1" {
		t.Fatalf("expected %q, got %q", "task-dispatch-1", taskID)
	}
}

// TestProvider_SendTask_NilOpts verifies opts=nil is accepted.
func TestProvider_SendTask_NilOpts(t *testing.T) {
	fp := &fake.Provider{ReturnTaskID: "task-no-opts"}
	target := mustAddr(t, "worker", "acme")

	taskID, err := fp.SendTask(context.Background(), target, "summarize", nil, nil)
	if err != nil {
		t.Fatalf("SendTask(nil opts): %v", err)
	}
	if taskID != "task-no-opts" {
		t.Fatalf("expected %q, got %q", "task-no-opts", taskID)
	}
}

// TestProvider_SendTask_FullAddress verifies the full address (name/tenant) is preserved.
func TestProvider_SendTask_FullAddress(t *testing.T) {
	fp := &fake.Provider{ReturnTaskID: "t-addr"}
	target := mustAddr(t, "worker", "acme")

	fp.SendTask(context.Background(), target, "run", nil, nil) //nolint:errcheck

	if len(fp.TaskCalls) != 1 {
		t.Fatalf("expected 1 TaskCall, got %d", len(fp.TaskCalls))
	}
	got := fp.TaskCalls[0].Target
	if got.Name() != "worker" || got.Tenant() != "acme" {
		t.Fatalf("full address not preserved: got %q", got)
	}
}

// ---- Gateway contract tests -----------------------------------------------

// TestGateway_Send_AllowedChannel verifies known channels are accepted.
func TestGateway_Send_AllowedChannel(t *testing.T) {
	tests := []struct {
		channel string
	}{
		{"telegram"},
		{"email"},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			fg := &fake.Gateway{}
			msg := port.OutboundMessage{Channel: tt.channel, Body: "hello"}

			if err := fg.Send(context.Background(), msg); err != nil {
				t.Fatalf("Send(%q): unexpected error: %v", tt.channel, err)
			}
			if fg.CallCount() != 1 {
				t.Fatalf("expected 1 Send call, got %d", fg.CallCount())
			}
		})
	}
}

// TestGateway_Send_UnknownChannel verifies unknown channels are rejected.
func TestGateway_Send_UnknownChannel(t *testing.T) {
	tests := []struct {
		channel string
	}{
		{"slack"},
		{"sms"},
		{""},
		{"TELEGRAM"}, // case-sensitive
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			fg := &fake.Gateway{}
			msg := port.OutboundMessage{Channel: tt.channel, Body: "hi"}

			err := fg.Send(context.Background(), msg)
			if err == nil {
				t.Fatalf("expected error for channel %q, got nil", tt.channel)
			}
			// No call should be recorded when the channel is rejected.
			if fg.CallCount() != 0 {
				t.Fatalf("Send must not be recorded for rejected channel %q", tt.channel)
			}
		})
	}
}

// TestGateway_Send_RecordsPayload verifies the payload is stored for assertion.
func TestGateway_Send_RecordsPayload(t *testing.T) {
	fg := &fake.Gateway{}
	msg := port.OutboundMessage{Channel: "telegram", Body: "test body"}

	if err := fg.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	last, ok := fg.LastCall()
	if !ok {
		t.Fatal("LastCall returned false after Send")
	}
	if last.Body != "test body" {
		t.Fatalf("expected body %q, got %q", "test body", last.Body)
	}
}

// TestGateway_Send_NonInvocation verifies CallCount stays zero when Send is never called.
// This is the foundation for E2E negative tests (hard-deny path) in PRs 4 and 8.
func TestGateway_Send_NonInvocation(t *testing.T) {
	fg := &fake.Gateway{}

	if fg.CallCount() != 0 {
		t.Fatalf("expected 0 calls on fresh gateway, got %d", fg.CallCount())
	}
	if fg.WasCalled("telegram") {
		t.Fatal("WasCalled must be false on a fresh gateway")
	}
}

// TestGateway_Send_DeliveryFailure verifies error propagation on configured failure.
func TestGateway_Send_DeliveryFailure(t *testing.T) {
	fg := &fake.Gateway{ReturnErr: errors.New("telegram: rate limited")}
	msg := port.OutboundMessage{Channel: "telegram", Body: "urgent"}

	err := fg.Send(context.Background(), msg)
	if err == nil {
		t.Fatal("expected error from Send, got nil")
	}
}

// ---- ValidateChannel unit tests -------------------------------------------

func TestValidateChannel(t *testing.T) {
	tests := []struct {
		channel string
		wantErr bool
	}{
		{"telegram", false},
		{"email", false},
		{"slack", true},
		{"", true},
		{"Telegram", true},
		{"sms", true},
	}
	for _, tt := range tests {
		t.Run(tt.channel, func(t *testing.T) {
			err := port.ValidateChannel(tt.channel)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateChannel(%q) wantErr=%v, got err=%v", tt.channel, tt.wantErr, err)
			}
		})
	}
}
