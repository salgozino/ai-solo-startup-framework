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
		{"tool with double underscore", pi.MCPServer{Command: bin, Args: []string{"dunder"}},
			`pi: mcp server "srv" tool "do__thing" is ambiguous in pi's flat tool namespace: its normalized name "do__thing" must not contain "__" or start with "_"`},
		{"tool with leading underscore", pi.MCPServer{Command: bin, Args: []string{"leading"}},
			`pi: mcp server "srv" tool "_x" is ambiguous in pi's flat tool namespace: its normalized name "_x" must not contain "__" or start with "_"`},
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

// R3 regression: "alpha"+"do__thing" and "alpha__do"+"thing" would both flatten to
// mcp__alpha__do__thing. The server name is rejected before any probe runs.
func TestProbeMCP_AmbiguousServerNameRejected(t *testing.T) {
	bin := fakeMCP(t)
	for name, want := range map[string]string{
		"alpha__do": `pi: mcp server "alpha__do" is ambiguous in pi's flat tool namespace: its normalized name "alpha__do" must not contain "__" or end with "_"`,
		"alpha-_do": `pi: mcp server "alpha-_do" is ambiguous in pi's flat tool namespace: its normalized name "alpha__do" must not contain "__" or end with "_"`,
		"alpha_":    `pi: mcp server "alpha_" is ambiguous in pi's flat tool namespace: its normalized name "alpha_" must not contain "__" or end with "_"`,
	} {
		servers := map[string]pi.MCPServer{"alpha": {Command: bin, Args: []string{"thing"}}, name: {Command: bin, Args: []string{"thing"}}}
		err := probeAdapter(t, 5*time.Second, servers).ProbeMCP(context.Background())
		if err == nil || err.Error() != want {
			t.Errorf("%s: err = %v\nwant %q", name, err, want)
		}
	}
}

// A policy kind is a framework tool name, so it must parse unambiguously too.
func TestProbeMCP_AmbiguousPolicyKindRejected(t *testing.T) {
	a := pi.New("pi-unused", pi.Options{PolicyActionKinds: []string{"telegram_send", "send__all"}}, "", "", nil)
	want := `pi: policy action kind "send__all" is ambiguous in pi's flat tool namespace: its normalized name "send__all" must not contain "__" or start with "_"`
	if err := a.ProbeMCP(context.Background()); err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %q", err, want)
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
