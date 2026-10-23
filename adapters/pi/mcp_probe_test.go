package pi_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/adapters/pi"
)

var (
	fakeMCPOnce sync.Once
	fakeMCPPath string
	fakeMCPErr  error
)

// fakeMCP builds testdata/fakemcp once per test binary and returns its path.
func fakeMCP(t *testing.T) string {
	t.Helper()
	fakeMCPOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakemcp-")
		if err != nil {
			fakeMCPErr = err
			return
		}
		fakeMCPPath = filepath.Join(dir, "fakemcp")
		if runtime.GOOS == "windows" {
			fakeMCPPath += ".exe"
		}
		out, err := exec.Command("go", "build", "-o", fakeMCPPath, filepath.Join("testdata", "fakemcp", "main.go")).CombinedOutput()
		if err != nil {
			fakeMCPErr = &buildError{string(out), err}
		}
	})
	if fakeMCPErr != nil {
		t.Fatalf("build fakemcp: %v", fakeMCPErr)
	}
	return fakeMCPPath
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

func probeAdapter(t *testing.T, timeout time.Duration, servers map[string]pi.MCPServer) *pi.Adapter {
	t.Helper()
	return pi.New("pi-unused", pi.Options{MCPServers: servers, MCPProbeTimeout: timeout}, "", "", nil)
}

func TestProbeMCP_FailureModesNamedExactly(t *testing.T) {
	bin := fakeMCP(t)
	cases := []struct {
		name string
		srv  pi.MCPServer
		want string
	}{
		{"zero tools", pi.MCPServer{Command: bin, Args: []string{"empty"}},
			`pi: mcp server "srv" exposes no tools`},
		{"collision", pi.MCPServer{Command: bin, Args: []string{"collide"}},
			`pi: mcp server "srv" tools "a-b" and "a_b" both normalize to "mcp__srv__a_b"; pi would hash-suffix one of them`},
		{"timeout", pi.MCPServer{Command: bin, Args: []string{"hang"}},
			`pi: mcp server "srv" probe timed out after 500ms`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := probeAdapter(t, 500*time.Millisecond, map[string]pi.MCPServer{"srv": c.srv}).ProbeMCP(context.Background())
			if err == nil || err.Error() != c.want {
				t.Fatalf("err = %v\nwant %q", err, c.want)
			}
		})
	}
	for name, srv := range map[string]pi.MCPServer{
		"crash":   {Command: bin, Args: []string{"crash"}},
		"missing": {Command: filepath.Join(t.TempDir(), "nope")},
	} {
		err := probeAdapter(t, 5*time.Second, map[string]pi.MCPServer{"srv": srv}).ProbeMCP(context.Background())
		if err == nil || !strings.HasPrefix(err.Error(), `pi: mcp server "srv" probe failed: `) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestProbeMCP_NoServersIsNoop(t *testing.T) {
	if err := probeAdapter(t, time.Second, nil).ProbeMCP(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// A declared server that was never probed must not run with a guessed allowlist.
func TestRunTask_UnprobedServersFailClosed(t *testing.T) {
	bin := helperBinary(t)
	a := pi.New(bin, pi.Options{MCPServers: map[string]pi.MCPServer{"s": {Command: fakeMCP(t)}}}, "", "", nil)
	_, err := a.RunTask(context.Background(), "t", "hello")
	if err == nil || err.Error() != `pi: mcp server "s" was not probed; call ProbeMCP at startup` {
		t.Fatalf("err = %v", err)
	}
}

func TestRunTask_FrameworkToolsExactAndSorted(t *testing.T) {
	bin := helperBinary(t)
	argv := dumpArgv(t)
	t.Setenv("FAKEPI_CONNECT_MCP_ONLY", "1")
	srv, registry := startTestMCPServer(t)
	a := pi.New(bin, pi.Options{
		MCPRegistry: &registryMinter{reg: registry}, MCPServerAddr: srv.Addr(), Tenant: "acme", AgentName: "ceo",
		PolicyActionKinds: []string{"telegram_send", "delegate_task", "finish_task"},
	}, "", "", nil)
	if _, err := a.RunTask(context.Background(), "t", "hello"); err != nil {
		t.Fatal(err)
	}
	want := "read,bash,edit,write,mcp__framework__delegate_task,mcp__framework__finish_task,mcp__framework__telegram_send"
	if tools, _ := valueAfter(argv(), "--tools"); tools != want {
		t.Errorf("--tools = %q, want %q", tools, want)
	}
}
