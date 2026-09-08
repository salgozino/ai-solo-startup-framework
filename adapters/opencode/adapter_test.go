// Package opencode_test contains threat-matrix RED tests for the OpenCode adapter.
// These tests cover the provider-subprocess threat cases:
//   (a) argv-as-slice: shell metacharacters in input are literal data, never interpreted
//   (b) hung child killed after ctx deadline → FAILED
//   (c) oversized output truncated with marker before parse
//   (d) non-zero exit → failure outcome, not success
//   (e) model flag is passed to the CLI when configured
//
// Tests use a helper binary (built from testdata/fakeopencode) that simulates opencode CLI
// exit behavior without requiring a real opencode installation.
package opencode_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/opencode"
)

// helperBinary builds the fakeopencode binary once per test run and returns its path.
func helperBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeopencode")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := filepath.Join("testdata", "fakeopencode", "main.go")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fakeopencode: %v", err)
	}
	return bin
}

// TestArgvSlice_ShellMetacharactersAreLiteral verifies threat-matrix case (a):
// shell metacharacters in input do not alter the invocation.
func TestArgvSlice_ShellMetacharactersAreLiteral(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	maliciousInput := "prefix; echo INJECTED"

	result, err := adapter.RunTask(ctx, "task-argv", maliciousInput)
	if err != nil {
		t.Fatalf("RunTask with metachar input: unexpected error: %v", err)
	}
	if result.Output != maliciousInput {
		t.Errorf("expected literal output %q, got %q", maliciousInput, result.Output)
	}
	if strings.Contains(result.Output, "\n") {
		t.Errorf("output contains newline — possible shell interpretation: %q", result.Output)
	}
}

// TestHungChild_KilledOnDeadline verifies threat-matrix case (b):
// a hung opencode process is killed when the context deadline elapses.
func TestHungChild_KilledOnDeadline(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	_, err := adapter.RunTask(ctx, "task-hung", "hang")
	if err == nil {
		t.Fatal("expected error when child is killed by deadline, got nil")
	}
}

// TestOversizedOutput_TruncatedWithMarker verifies threat-matrix case (c):
// output that exceeds the size cap is truncated; the marker is prepended.
func TestOversizedOutput_TruncatedWithMarker(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 10}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-large", "large")
	if err != nil {
		t.Fatalf("RunTask large output: %v", err)
	}
	if !strings.Contains(result.Output, opencode.TruncationMarker) {
		t.Errorf("expected truncation marker %q in output, got: %q", opencode.TruncationMarker, result.Output)
	}
}

// TestNonZeroExit_MapsToError verifies threat-matrix case (d):
// a non-zero exit from the opencode process results in an error.
func TestNonZeroExit_MapsToError(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	_, err := adapter.RunTask(ctx, "task-fail", "fail")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil (success hidden failure)")
	}
}

// TestModelFlag_PassedToCLI verifies that when a model is configured,
// the --model flag is correctly passed to the opencode CLI.
func TestModelFlag_PassedToCLI(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "anthropic/claude-sonnet-4-20250514", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-model", "hello")
	if err != nil {
		t.Fatalf("RunTask with model: unexpected error: %v", err)
	}
	// fakeopencode prepends "model:<model>|" when --model is passed.
	expected := "model:anthropic/claude-sonnet-4-20250514|hello"
	if result.Output != expected {
		t.Errorf("expected output %q, got %q", expected, result.Output)
	}
}

// TestNoModelFlag_OmitsFlag verifies that when no model is configured,
// the --model flag is not passed to the CLI.
func TestNoModelFlag_OmitsFlag(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-model", "hello")
	if err != nil {
		t.Fatalf("RunTask without model: unexpected error: %v", err)
	}
	// Without model, fakeopencode echoes input verbatim (no model prefix).
	if result.Output != "hello" {
		t.Errorf("expected output %q, got %q", "hello", result.Output)
	}
}

// TestAgentFlag_PassedToCLI verifies that when an agent name is configured,
// the --agent flag is correctly passed to the opencode CLI.
func TestAgentFlag_PassedToCLI(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "ceo")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-agent", "hello")
	if err != nil {
		t.Fatalf("RunTask with agent: unexpected error: %v", err)
	}
	// fakeopencode prepends "agent:<agent>|" when --agent is passed.
	expected := "agent:ceo|hello"
	if result.Output != expected {
		t.Errorf("expected output %q, got %q", expected, result.Output)
	}
}

// TestNoAgentFlag_OmitsFlag verifies that when no agent name is configured,
// the --agent flag is not passed to the CLI.
func TestNoAgentFlag_OmitsFlag(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-agent", "hello")
	if err != nil {
		t.Fatalf("RunTask without agent: unexpected error: %v", err)
	}
	// Without agent, fakeopencode echoes input verbatim (no agent prefix).
	if result.Output != "hello" {
		t.Errorf("expected output %q, got %q", "hello", result.Output)
	}
}

// TestRunTask_StderrInError verifies that subprocess stderr text is surfaced in the
// returned error on non-zero exit (spec: Subprocess fails with stderr output).
func TestRunTask_StderrInError(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// "fail-stderr" causes fakeopencode to write a diagnostic line to stderr then exit 1.
	_, err := adapter.RunTask(ctx, "task-fail-stderr", "fail-stderr")
	if err == nil {
		t.Fatal("expected error for non-zero exit with stderr, got nil")
	}
	if !strings.Contains(err.Error(), "stderr:") {
		t.Errorf("error must contain \"stderr:\" label; got: %v", err)
	}
	if !strings.Contains(err.Error(), "simulated stderr output") {
		t.Errorf("error must contain subprocess stderr text; got: %v", err)
	}
}

// TestRunTask_EmptyStderrOnFail verifies that a non-zero exit with no stderr
// produces a non-nil, non-empty error and does not panic
// (spec: Subprocess fails with empty stderr).
func TestRunTask_EmptyStderrOnFail(t *testing.T) {
	bin := helperBinary(t)
	adapter := opencode.New(bin, opencode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// "fail" exits 1 without writing anything to stderr.
	_, err := adapter.RunTask(ctx, "task-fail-empty-stderr", "fail")
	if err == nil {
		t.Fatal("expected non-nil error for non-zero exit with empty stderr, got nil")
	}
	if err.Error() == "" {
		t.Error("error message must not be empty string")
	}
	// Error format must include the "stderr:" label even when content is empty.
	if !strings.Contains(err.Error(), "stderr:") {
		t.Errorf("error must contain \"stderr:\" label even with empty stderr; got: %v", err)
	}
}

// NOTE: ProbeModel tests removed — opencode CLI does not distinguish invalid
// model from missing prompt (both produce the same generic error). The opencode
// adapter intentionally does not implement modelProber; materializeAgents skips
// the probe for it. See adapters/opencode/adapter.go for details.
