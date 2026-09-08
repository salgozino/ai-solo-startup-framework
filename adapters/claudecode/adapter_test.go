// Package claudecode_test contains threat-matrix RED tests for the Claude Code adapter.
// These tests cover the provider-subprocess threat cases from design.md:
//   (a) argv-as-slice: shell metacharacters in input are literal data, never interpreted
//   (b) hung child killed after ctx deadline → FAILED
//   (c) oversized output truncated with marker before parse
//   (d) non-zero exit → failure outcome, not success
//
// Tests use a helper binary (built from testdata/fakeclaude) that simulates claude CLI
// exit behavior without requiring a real claude installation.
package claudecode_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
)

// helperBinary builds the fakeclaude binary once per test run and returns its path.
// The binary is placed in t.TempDir() so it is cleaned up automatically.
func helperBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeclaude")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	src := filepath.Join("testdata", "fakeclaude", "main.go")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build fakeclaude: %v", err)
	}
	return bin
}

// TestArgvSlice_ShellMetacharactersAreLiteral verifies threat-matrix case (a):
// shell metacharacters in input do not alter the invocation — they are passed as literal data.
//
// If the adapter used "sh -c", the shell would execute `echo INJECTED` and `rm -rf /` as
// separate commands, producing a multi-line output where INJECTED appears on its own line.
// With argv-as-slice, the entire string is passed verbatim as one argument; fakeclaude echoes
// it as-is on a single line. No newline within the output means no command was interpreted.
func TestArgvSlice_ShellMetacharactersAreLiteral(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// This input would produce a second output line ("INJECTED") if run through sh -c.
	maliciousInput := "prefix; echo INJECTED"

	result, err := adapter.RunTask(ctx, "task-argv", maliciousInput)
	if err != nil {
		t.Fatalf("RunTask with metachar input: unexpected error: %v", err)
	}
	// With argv-as-slice: fakeclaude echoes the full string as one token, no newline inside.
	// The output has a "safe:1|" prefix (isolation flag is always present) then the literal input.
	expected := "safe:1|" + maliciousInput
	if result.Output != expected {
		t.Errorf("expected literal output %q, got %q", expected, result.Output)
	}
	// Secondary check: no newline inside the output — a shell would produce two lines.
	if strings.Contains(result.Output, "\n") {
		t.Errorf("output contains newline — possible shell interpretation: %q", result.Output)
	}
}

// TestHungChild_KilledOnDeadline verifies threat-matrix case (b):
// a hung claude process is killed when the context deadline elapses; outcome is FAILED.
func TestHungChild_KilledOnDeadline(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	// Very short deadline — fakeclaude in "hang" mode sleeps indefinitely.
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
	// Tiny limit so the fakeclaude "large" output exceeds it.
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 10}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-large", "large")
	if err != nil {
		t.Fatalf("RunTask large output: %v", err)
	}
	// When truncated, the marker must appear in the output.
	if !strings.Contains(result.Output, claudecode.TruncationMarker) {
		t.Errorf("expected truncation marker %q in output, got: %q", claudecode.TruncationMarker, result.Output)
	}
}

// TestNonZeroExit_MapsToError verifies threat-matrix case (d):
// a non-zero exit from the claude process results in an error, not a success result.
func TestNonZeroExit_MapsToError(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	// fakeclaude exits with code 1 when input is "fail".
	_, err := adapter.RunTask(ctx, "task-fail", "fail")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil (success hidden failure)")
	}
}

// TestSafeModeFlag_AlwaysPresent verifies that --safe-mode is unconditionally included
// in the claude invocation regardless of other settings (spec: Unconditional Isolation).
func TestSafeModeFlag_AlwaysPresent(t *testing.T) {
	bin := helperBinary(t)
	// No system prompt, no model — safe-mode must still be set.
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-safe", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// fakeclaude prepends "safe:1|" when --safe-mode is passed.
	if !strings.HasPrefix(result.Output, "safe:1|") {
		t.Errorf("expected output to start with \"safe:1|\", got %q", result.Output)
	}
}

// TestSystemPromptFile_WhenSet verifies that --system-prompt-file is included
// when a non-empty system prompt path is configured.
func TestSystemPromptFile_WhenSet(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "/abs/path/to/ceo.md")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-sysprompt", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// fakeclaude prepends "sysprompt:<path>|" when --system-prompt-file is passed.
	if !strings.Contains(result.Output, "sysprompt:/abs/path/to/ceo.md|") {
		t.Errorf("expected output to contain sysprompt path, got %q", result.Output)
	}
}

// TestSystemPromptFile_WhenNotSet verifies that --system-prompt-file is absent
// when no system prompt path is configured.
func TestSystemPromptFile_WhenNotSet(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-sysprompt", "hello")
	if err != nil {
		t.Fatalf("RunTask: unexpected error: %v", err)
	}
	// No --system-prompt-file → no sysprompt prefix in output.
	if strings.Contains(result.Output, "sysprompt:") {
		t.Errorf("expected no sysprompt in output when not set, got %q", result.Output)
	}
}

// TestModelFlag_PassedToCLI verifies that when a model is configured,
// the --model flag is correctly passed to the claude CLI.
func TestModelFlag_PassedToCLI(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "anthropic/claude-sonnet-4-20250514", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-model", "hello")
	if err != nil {
		t.Fatalf("RunTask with model: unexpected error: %v", err)
	}
	// fakeclaude prepends "safe:1|" (always, --safe-mode) then "model:<model>|" when --model is passed.
	expected := "safe:1|model:anthropic/claude-sonnet-4-20250514|hello"
	if result.Output != expected {
		t.Errorf("expected output %q, got %q", expected, result.Output)
	}
}

// TestNoModelFlag_OmitsFlag verifies that when no model is configured,
// the --model flag is not passed to the CLI.
func TestNoModelFlag_OmitsFlag(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	ctx := context.Background()
	result, err := adapter.RunTask(ctx, "task-no-model", "hello")
	if err != nil {
		t.Fatalf("RunTask without model: unexpected error: %v", err)
	}
	// Without model, fakeclaude echoes input with only the safe-mode prefix.
	if result.Output != "safe:1|hello" {
		t.Errorf("expected output %q, got %q", "safe:1|hello", result.Output)
	}
}
