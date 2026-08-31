// Package claudecode_test — contract test suite for ClaudeCodeAdapter.
//
// Task 5.4: import and run the Provider contract assertions against ClaudeCodeAdapter.
// The contract tests that require A2A network calls (Complete, SendMessage, etc.) are
// intentionally skipped here — those methods are implemented by transport/a2a, not this
// adapter. RunTask is the only method this adapter owns, so the contract test focuses on
// the RunTask sub-contract: stateless, output-parsed, failure-mapped.
//
// Integration tests that require a real claude binary are guarded by testing.Short().
package claudecode_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
)

// TestContract_RunTask_Stateless verifies the RunTask sub-contract:
// two calls produce independent results with no shared in-process state.
func TestContract_RunTask_Stateless(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20})
	ctx := context.Background()

	r1, err := adapter.RunTask(ctx, "task-a", "hello")
	if err != nil {
		t.Fatalf("first RunTask: %v", err)
	}
	r2, err := adapter.RunTask(ctx, "task-b", "world")
	if err != nil {
		t.Fatalf("second RunTask: %v", err)
	}
	if r1.Output == r2.Output {
		t.Errorf("two distinct inputs produced identical outputs: %q", r1.Output)
	}
}

// TestContract_RunTask_OutputParsed verifies that the result carries the output as a
// parsed string (port.ProviderResult.Output), not raw bytes or a hidden nil.
func TestContract_RunTask_OutputParsed(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20})
	ctx := context.Background()

	result, err := adapter.RunTask(ctx, "task-parse", "parsed-content")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	if result.Output == "" {
		t.Error("parsed Output must be non-empty for successful invocation")
	}
}

// TestContract_RunTask_FailureIsMapped verifies that RunTask maps a failing provider
// to a non-nil error, not a success result with a hidden error.
func TestContract_RunTask_FailureIsMapped(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20})
	ctx := context.Background()

	_, err := adapter.RunTask(ctx, "task-fail-contract", "fail")
	if err == nil {
		t.Fatal("contract violation: non-zero exit must map to error, got nil")
	}
}

// TestContract_RunTask_RealClaude is an integration test that runs the real claude binary.
// It is skipped in -short mode and when claude is not on PATH.
func TestContract_RunTask_RealClaude(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test: skipped in -short mode")
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude binary not found on PATH — skipping real-claude integration test")
	}

	adapter := claudecode.New(claudePath, claudecode.Options{OutputLimit: 1 << 20})
	ctx := context.Background()

	// We only verify the adapter does not panic and returns a result.
	// The actual output depends on the real claude binary's behaviour.
	result, err := adapter.RunTask(ctx, "task-integration", "--version")
	// --version typically exits 0 with version info; accept either outcome.
	if err == nil && result.Output == "" {
		t.Error("real claude returned success but empty output")
	}
}
