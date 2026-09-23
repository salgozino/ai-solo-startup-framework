// Package claudecode_test — input delivery tests (B5 / Slice 2).
//
// The adapter used to pass the task input as a trailing positional argv argument.
// Linux caps a SINGLE argv string at MAX_ARG_STRLEN (32 pages = 131072 bytes on every
// mainstream configuration), independently of the much larger total ARG_MAX. A prompt
// past that ceiling makes execve fail with E2BIG, which Go surfaces from cmd.Start() as
// "argument list too long" — the whole invocation dies before the CLI runs.
//
// That ceiling is the hard blocker for the CEO orchestration loop: a re-injected
// transcript grows every round, so the prompt is expected to cross it. These tests pin
// the delivery channel to one that has no such cap.
package claudecode_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
)

// maxArgStrLen mirrors the Linux kernel's MAX_ARG_STRLEN (32 * PAGE_SIZE, PAGE_SIZE 4096).
// It is the per-argument ceiling, not the total argv budget.
const maxArgStrLen = 32 * 4096

// oversizedInput builds a deterministic prompt comfortably past the argv ceiling.
// The marker bookends make a truncated delivery (as opposed to a failed one) visible.
// It is deliberately 2x MAX_ARG_STRLEN rather than a few bytes over, so the test stays
// meaningful on a kernel configured with a larger PAGE_SIZE.
func oversizedInput() string {
	const head = "HEAD-MARKER|"
	const tail = "|TAIL-MARKER"
	filler := strings.Repeat("abcdefgh", (2*maxArgStrLen)/8)
	return head + filler + tail
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestRunTask_DeliversInputLargerThanArgvCeiling proves that an input larger than
// MAX_ARG_STRLEN reaches the subprocess intact — byte for byte, not truncated and not
// rejected by execve.
//
// fakeclaude echoes the prompt it received back on stdout, so a round-trip comparison
// against the original input is a direct assertion about delivery, not about the fake's
// own flag parsing. The "iso:1|" prefix is fakeclaude's isolation-flags receipt and is
// stripped before comparison.
func TestRunTask_DeliversInputLargerThanArgvCeiling(t *testing.T) {
	bin := helperBinary(t)
	adapter := claudecode.New(bin, claudecode.Options{OutputLimit: 1 << 20}, "", "")

	input := oversizedInput()
	if len(input) <= maxArgStrLen {
		t.Fatalf("test fixture is not past the argv ceiling: len=%d, ceiling=%d", len(input), maxArgStrLen)
	}

	result, err := adapter.RunTask(context.Background(), "task-oversized-input", input)
	if err != nil {
		t.Fatalf("RunTask with a %d-byte input (argv ceiling is %d): %v", len(input), maxArgStrLen, err)
	}

	const isoPrefix = "iso:1|"
	delivered := strings.TrimPrefix(result.Output, isoPrefix)
	if delivered == result.Output {
		t.Fatalf("expected fakeclaude's %q isolation receipt on the output; got a %d-byte output starting with %q",
			isoPrefix, len(result.Output), truncateForMsg(result.Output))
	}

	if len(delivered) != len(input) {
		t.Fatalf("input was not delivered intact: got %d bytes, want %d", len(delivered), len(input))
	}
	if got, want := digest(delivered), digest(input); got != want {
		t.Fatalf("input was delivered with altered content: sha256 got %s, want %s", got, want)
	}
}

// TestRunTask_OversizedInputIsNotOnArgv is the structural companion: it proves the
// delivery channel is not argv at all, rather than argv that merely happened to fit.
// A future change that puts the prompt back on argv — even a small one — fails here
// before it can fail in production with E2BIG on a long transcript.
func TestRunTask_OversizedInputIsNotOnArgv(t *testing.T) {
	const prompt = "the CEO asks the engineer for a draft"
	argv := dumpArgvForRun(t, claudecode.Options{OutputLimit: 1 << 20}, "task-prompt-off-argv", prompt)

	for i, a := range argv {
		if strings.Contains(a, prompt) {
			t.Errorf("prompt found at argv[%d]=%q; the prompt must travel on stdin, because a single argv string is capped at %d bytes on Linux; argv=%v",
				i, a, maxArgStrLen, argv)
		}
	}
}

// truncateForMsg keeps failure messages readable when the value under test is huge.
func truncateForMsg(s string) string {
	const max = 120
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
