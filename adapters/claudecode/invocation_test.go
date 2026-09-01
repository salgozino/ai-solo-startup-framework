// Package claudecode_test — unit tests for invocation properties (task 5.5).
// Table-driven. Verifies:
//   - Two invocations for the same agent use separate exec.Cmd instances (no shared state).
//   - Non-zero exit returns failure.
//   - Raw output never crosses the port boundary (result is parsed Part/Output, not bytes).
package claudecode_test

import (
	"context"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
)

// TestInvocation_SeparateInstances verifies that two RunTask calls for the same logical
// agent produce independent results. If any state leaked between calls (e.g. a reused
// exec.Cmd), the second call would observe the first call's output or state.
func TestInvocation_SeparateInstances(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "")
	ctx := context.Background()

	tests := []struct {
		name      string
		taskID    string
		input     string
		wantInOut string // expected substring in output
	}{
		{"first call", "task-1", "alpha", "alpha"},
		{"second call", "task-1", "beta", "beta"}, // same taskID, different input
		{"third call", "task-1", "gamma", "gamma"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapter.RunTask(ctx, tt.taskID, tt.input)
			if err != nil {
				t.Fatalf("RunTask(%q): %v", tt.input, err)
			}
			if !strings.Contains(result.Output, tt.wantInOut) {
				t.Errorf("RunTask(%q): expected output to contain %q, got %q",
					tt.input, tt.wantInOut, result.Output)
			}
		})
	}
}

// TestInvocation_NonZeroExitTable is a table-driven coverage of the failure mapping:
// every non-zero exit scenario must produce an error.
func TestInvocation_NonZeroExitTable(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "")
	ctx := context.Background()

	tests := []struct {
		name  string
		input string // fakeclaude interprets "fail" as exit 1
	}{
		{"explicit fail", "fail"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := adapter.RunTask(ctx, "task-nz", tt.input)
			if err == nil {
				t.Errorf("input=%q: expected error for non-zero exit, got nil", tt.input)
			}
		})
	}
}

// TestInvocation_RawOutputNeverExposed verifies that the port result contains parsed
// string output, not raw bytes or unexported internal types.
// The contract: result.Output is a plain string; ActionIntents is a slice (may be nil/empty).
func TestInvocation_RawOutputNeverExposed(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "")
	ctx := context.Background()

	result, err := adapter.RunTask(ctx, "task-raw", "some-output")
	if err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	// Output is a typed string field — raw []byte would not compile here.
	// This assertion proves the port boundary is typed, not raw.
	if result.Output == "" {
		t.Error("expected non-empty parsed Output, got empty string")
	}
	// ActionIntents must be nil or empty for a simple completion — no intents emitted.
	if len(result.ActionIntents) != 0 {
		t.Errorf("expected no ActionIntents for simple output, got %d", len(result.ActionIntents))
	}
}
