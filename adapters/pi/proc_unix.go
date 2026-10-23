//go:build unix

package pi

import (
	"os/exec"
	"syscall"
)

// configureProcessTree runs the child in its own process group and makes cancellation
// SIGKILL the whole group, so descendants spawned by pi or its extensions die with it.
func configureProcessTree(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
}
