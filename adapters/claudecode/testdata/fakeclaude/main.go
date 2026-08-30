// fakeclaude simulates the claude CLI for unit testing the Claude Code adapter.
// It reads the first argument and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// The adapter must pass the input as argv[1], not via sh -c.
// Shell metacharacters in argv[1] appear verbatim in the printed output.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "fakeclaude: missing argument")
		os.Exit(2)
	}

	input := os.Args[1]

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
