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
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "")

	ctx := context.Background()
	// This input would produce a second output line ("INJECTED") if run through sh -c.
	maliciousInput := "prefix; echo INJECTED"

	result, err := adapter.RunTask(ctx, "task-argv", maliciousInput)
	if err != nil {
		t.Fatalf("RunTask with metachar input: unexpected error: %v", err)
	}
	// With argv-as-slice: fakeclaude echoes the full string as one token, no newline inside.
	// The output must be exactly the literal input (trimmed of trailing newline).
	if result.Output != maliciousInput {
		t.Errorf("expected literal output %q, got %q", maliciousInput, result.Output)
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
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "")

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
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 10}, "")

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
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "")

	ctx := context.Background()
	// fakeclaude exits with code 1 when input is "fail".
	_, err := adapter.RunTask(ctx, "task-fail", "fail")
	if err == nil {
		t.Fatal("expected error for non-zero exit, got nil (success hidden failure)")
	}
}
