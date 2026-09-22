// Package port_test contains the canonical contract test suite for port.Provider and port.Gateway.
// These tests run against fakeProvider and fakeGateway here; PR 5 (ClaudeCodeAdapter) and
// PR 6 (TelegramGateway) MUST import and run them against the real implementations.
package port_test

import (
	"context"
	"errors"
	"reflect"
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

// TestProvider_MethodSet verifies port.Provider is scoped to local execution and lifecycle
// reporting, not A2A networking: it declares exactly Complete, CompleteError, SendTask, RunTask,
// and Capabilities — no SendMessage, SendMessageStream, or ResolveAgent method is present.
// Spec: provider-adapter — "port.Provider's method set excludes A2A network operations".
func TestProvider_MethodSet(t *testing.T) {
	want := map[string]bool{
		"Complete":      true,
		"CompleteError": true,
		"SendTask":      true,
		"RunTask":       true,
		"Capabilities":  true,
	}

	providerType := reflect.TypeOf((*port.Provider)(nil)).Elem()
	if got := providerType.NumMethod(); got != len(want) {
		names := make([]string, got)
		for i := range got {
			names[i] = providerType.Method(i).Name
		}
		t.Fatalf("expected exactly %d methods %v, got %d: %v", len(want), want, got, names)
	}
	for i := range providerType.NumMethod() {
		name := providerType.Method(i).Name
		if !want[name] {
			t.Errorf("unexpected A2A-networking method on port.Provider: %s", name)
		}
	}
	for name := range want {
		if _, ok := providerType.MethodByName(name); !ok {
			t.Errorf("port.Provider is missing required method: %s", name)
		}
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

// ---- Provider.Capabilities contract tests ----------------------------------

// TestProvider_Capabilities_ReturnsConfigured verifies that a fake configured with
// ReturnCapabilities returns the exact value from Capabilities().
// Spec: provider-adapter – "fake.Provider conforms to the capability contract".
func TestProvider_Capabilities_ReturnsConfigured(t *testing.T) {
	p := &fake.Provider{
		ReturnCapabilities: port.ProviderCapabilities{
			ContextBudget: 8000,
			ActionKinds:   []string{"telegram_send"},
		},
	}
	got := p.Capabilities()
	if got.ContextBudget != 8000 {
		t.Fatalf("expected ContextBudget=8000, got %d", got.ContextBudget)
	}
	if len(got.ActionKinds) != 1 || got.ActionKinds[0] != "telegram_send" {
		t.Fatalf("expected ActionKinds=[telegram_send], got %v", got.ActionKinds)
	}
}

// TestProvider_Capabilities_ZeroByDefault verifies that a fresh fake.Provider returns
// the zero ProviderCapabilities (zero ContextBudget means no cap, nil ActionKinds).
// Spec: provider-adapter – "fake.Provider conforms to the capability contract".
func TestProvider_Capabilities_ZeroByDefault(t *testing.T) {
	p := &fake.Provider{}
	got := p.Capabilities()
	if got.ContextBudget != 0 {
		t.Fatalf("expected ContextBudget=0, got %d", got.ContextBudget)
	}
	if len(got.ActionKinds) != 0 {
		t.Fatalf("expected empty ActionKinds, got %v", got.ActionKinds)
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
