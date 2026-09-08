// fakeclaude simulates the claude CLI for unit testing the Claude Code adapter.
// It reads argv and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// When --model is present, it prepends "model:<model>|" to the output so tests
// can verify the model flag was passed correctly.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "fakeclaude: expected -p <argument>")
		os.Exit(2)
	}

	// Args: [-p [--model <model>]] <input>
	// Parse flags, then take the last argument as the prompt.
	var model string
	input := ""
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "-p" {
			continue // skip -p flag
		}
		if os.Args[i] == "--model" && i+1 < len(os.Args) {
			model = os.Args[i+1]
			i++ // skip model value
			continue
		}
		input = os.Args[i]
	}

	// Model-level behaviour: checked before input-level sentinels so that
	// ProbeModel tests can trigger model-specific outcomes via --model flag.
	if model == "badmodel" {
		fmt.Fprintln(os.Stderr, "There's an issue with the selected model (badmodel). It may not exist or you may not have access to it.")
		os.Exit(1)
	}
	if model == "hangmodel" {
		// Simulates a model probe that never responds — killed by ctx deadline.
		time.Sleep(24 * time.Hour)
	}

	// Empty prompt: simulate CLI behaviour for ProbeModel.
	// Valid model + empty prompt → "Input must be provided" error (exit 1).
	if input == "" {
		fmt.Fprintln(os.Stderr, "Error: Input must be provided either through stdin or as a prompt argument when using --print")
		os.Exit(1)
	}

	switch input {
	case "fail":
		os.Exit(1)

	case "fail-stderr":
		// Exits non-zero AND writes to stderr — used to test stderr capture.
		fmt.Fprintln(os.Stderr, "simulated stderr output from failed subprocess")
		os.Exit(1)

	case "hang":
		// Sleep until killed — simulates a hung process.
		time.Sleep(24 * time.Hour)

	case "large":
		// Emit 1 MiB of data so the adapter's io.LimitReader is triggered.
		fmt.Print(strings.Repeat("x", 1<<20))

	default:
		output := input
		if model != "" {
			output = "model:" + model + "|" + input
		}
		fmt.Print(output)
	}
}
