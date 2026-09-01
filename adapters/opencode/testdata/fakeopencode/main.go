// fakeopencode simulates the opencode CLI for unit testing the OpenCode adapter.
// It reads argv and behaves as follows:
//
//	"fail"   — exits with code 1, prints nothing
//	"hang"   — sleeps until SIGKILL (simulates a hung process)
//	"large"  — prints 1 MiB of 'x' characters then exits 0
//	anything else — prints the argument as output text then exits 0
//
// When --model is present, it prepends "model:<model>|" to the output so tests
// can verify the model flag was passed correctly.
// When --agent is present, it prepends "agent:<agent>|" to the output so tests
// can verify the agent flag was passed correctly.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "fakeopencode: expected run <argument>")
		os.Exit(2)
	}

	// Args: [run [--model <model>] [--agent <agent>]] <input>
	// Parse flags, then take the last argument as the prompt.
	var model string
	var agent string
	input := ""
	for i := 1; i < len(os.Args); i++ {
		if os.Args[i] == "--model" && i+1 < len(os.Args) {
			model = os.Args[i+1]
			i++ // skip model value
			continue
		}
		if os.Args[i] == "--agent" && i+1 < len(os.Args) {
			agent = os.Args[i+1]
			i++ // skip agent value
			continue
		}
		input = os.Args[i]
	}

	if input == "" {
		fmt.Fprintln(os.Stderr, "fakeopencode: no input provided")
		os.Exit(2)
	}

	switch input {
	case "fail":
		os.Exit(1)

	case "hang":
		time.Sleep(24 * time.Hour)

	case "large":
		fmt.Print(strings.Repeat("x", 1<<20))

	default:
		output := input
		if agent != "" {
			output = "agent:" + agent + "|" + output
		}
		if model != "" {
			output = "model:" + model + "|" + output
		}
		fmt.Print(output)
	}
}
