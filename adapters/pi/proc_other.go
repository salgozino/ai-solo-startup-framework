//go:build !unix

package pi

import "os/exec"

// configureProcessTree is a no-op where process groups are unavailable; the default
// CommandContext behaviour (kill the child only) applies.
func configureProcessTree(*exec.Cmd) {}
