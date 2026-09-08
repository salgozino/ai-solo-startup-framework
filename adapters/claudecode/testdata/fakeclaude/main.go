// fakeclaude simulates the claude CLI for unit testing the Claude Code adapter.
// It reads argv and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// When --bare is present, it prepends "bare:1|" to the output.
// When --model is present, it prepends "model:<model>|" to the output.
// When --system-prompt-file is present, it prepends "sysprompt:<path>|" to the output.
// --no-session-persistence is consumed silently.
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

	// Args: [-p [--bare] [--no-session-persistence] [--model <model>] [--system-prompt-file <path>]] <input>
	// Parse flags, then take the last argument as the prompt.
	var model string
	var systemPromptFile string
	bare := false
	input := ""
	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-p":
			// skip
		case "--bare":
			bare = true
		case "--no-session-persistence":
			// consumed silently
		case "--model":
			if i+1 < len(os.Args) {
				model = os.Args[i+1]
				i++
			}
		case "--system-prompt-file":
			if i+1 < len(os.Args) {
				systemPromptFile = os.Args[i+1]
				i++
			}
		default:
			input = os.Args[i]
		}
	}

	if input == "" {
		fmt.Fprintln(os.Stderr, "fakeclaude: no input provided")
		os.Exit(2)
	}

	switch input {
	case "fail":
		os.Exit(1)

	case "hang":
		// Sleep until killed — simulates a hung process.
		time.Sleep(24 * time.Hour)

	case "large":
		// Emit 1 MiB of data so the adapter's io.LimitReader is triggered.
		fmt.Print(strings.Repeat("x", 1<<20))

	default:
		output := input
		if systemPromptFile != "" {
			output = "sysprompt:" + systemPromptFile + "|" + output
		}
		if model != "" {
			output = "model:" + model + "|" + output
		}
		if bare {
			output = "bare:1|" + output
		}
		fmt.Print(output)
	}
}
