//go:build unix

package pi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/pi"
)

// S7: cancelling kills pi's whole process group, so a descendant holding the stdout/stderr
// descriptors cannot delay RunTask past the deadline nor outlive it.
func TestRunTask_DeadlineKillsDescendants(t *testing.T) {
	bin := helperBinary(t)
	pidFile := filepath.Join(t.TempDir(), "pid.txt")
	t.Setenv("FAKEPI_PID_FILE", pidFile)
	t.Cleanup(func() { // never leak the sleeper, even on RED
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, err := pi.New(bin, pi.Options{}, "", "", nil).RunTask(ctx, "t", "hang-descendant")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected deadline error, got %v", err)
		}
		if el := time.Since(start); el > 8*time.Second {
			t.Errorf("RunTask returned after %v, want <= 8s", el)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("RunTask blocked on a descendant holding stdout past the deadline")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("descendant pid file: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return
		}
	}
	t.Errorf("descendant pid %d still alive after cancel", pid)
}
