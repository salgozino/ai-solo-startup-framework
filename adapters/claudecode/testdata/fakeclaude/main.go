// fakeclaude simulates the claude CLI for unit testing the Claude Code adapter.
// It reads argv[2] (after the -p flag) and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// The adapter passes input as argv[2] via `claude -p <input>`, not via sh -c.
// Shell metacharacters in argv[2] appear verbatim in the printed output.
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

	// Args: [-p <input>] — skip the -p flag, take the prompt.
	input := os.Args[2]

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
		// Echo the input verbatim — proves argv-as-slice: no shell expansion occurred.
		fmt.Print(input)
	}
}
