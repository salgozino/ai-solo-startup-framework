package pi

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultMCPProbeTimeout bounds the probe of ONE declared server (spawn, initialize and
// every tools/list page). Generous because stdio servers are often started through npx.
const defaultMCPProbeTimeout = 30 * time.Second

var nonToolChar = regexp.MustCompile(`[^A-Za-z0-9_]`)

// toolName is the name pi gives a server's tool: mcp__<server>__<tool>, every character
// outside [A-Za-z0-9_] in either part replaced by "_" (pi docs/mcp.md).
func toolName(server, tool string) string {
	return "mcp__" + normalize(server) + "__" + normalize(tool)
}

// normalize maps a server or tool name the way pi does: every char outside [A-Za-z0-9_]
// becomes "_".
func normalize(s string) string { return nonToolChar.ReplaceAllString(s, "_") }

// unambiguousTool reports whether a normalized tool name keeps mcp__<server>__<tool>
// parseable one way (see ProbeMCP).
func unambiguousTool(norm string) bool {
	return !strings.Contains(norm, "__") && !strings.HasPrefix(norm, "_")
}

// frameworkTools are the exact names of the framework server's tools: every policy action
// kind plus the finish_task control tool (transport/mcp registers exactly that set).
func frameworkTools(kinds []string) []string {
	set := map[string]bool{"finish_task": true}
	for _, k := range kinds {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, toolName("framework", k))
	}
	sort.Strings(out)
	return out
}

// ProbeMCP lists the tools of every declared mcp_servers entry, once, so --tools can name
// them exactly. For each server (name order) it spawns the command over stdio, performs the
// MCP initialize handshake, follows tools/list pagination, then closes it, all within
// Options.MCPProbeTimeout. It fails when a server cannot be probed or times out, exposes no
// tools, or has two tools that normalize to the same pi name (pi would hash-suffix one, so
// the exact name could not be allowlisted). Tools a server adds after this probe are not
// callable: the allowlist is fixed at startup.
//
// pi flattens every tool to mcp__<server>__<tool>, so the allowlist is only sound when that
// string parses one way: the separator must be its only "__". That holds when the
// normalized server has no "__" and does not end in "_", and the normalized tool has no
// "__" and does not start with "_"; then an ambient server can only produce an allowlisted
// name by having exactly the same server name. ProbeMCP rejects any server name, tool name
// or policy kind (a framework tool) that breaks this, before or during the probe.
func (a *Adapter) ProbeMCP(ctx context.Context) error {
	for _, kind := range a.policyActionKinds {
		if norm := normalize(kind); !unambiguousTool(norm) {
			return fmt.Errorf("pi: policy action kind %q is ambiguous in pi's flat tool namespace: its normalized name %q must not contain \"__\" or start with \"_\"", kind, norm)
		}
	}
	for _, name := range a.mcpServerNames {
		if norm := normalize(name); strings.Contains(norm, "__") || strings.HasSuffix(norm, "_") {
			return fmt.Errorf("pi: mcp server %q is ambiguous in pi's flat tool namespace: its normalized name %q must not contain \"__\" or end with \"_\"", name, norm)
		}
	}
	probed := make(map[string][]string, len(a.mcpServerNames))
	for _, name := range a.mcpServerNames {
		tools, err := a.probeServer(ctx, name, a.mcpServers[name])
		if err != nil {
			return err
		}
		probed[name] = tools
	}
	a.mcpMu.Lock()
	a.mcpTools = probed
	a.mcpMu.Unlock()
	return nil
}

func (a *Adapter) probeServer(parent context.Context, name string, srv MCPServer) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, a.mcpProbeTimeout)
	defer cancel()
	fail := func(err error) ([]string, error) {
		if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("pi: mcp server %q probe timed out after %s", name, a.mcpProbeTimeout)
		}
		return nil, fmt.Errorf("pi: mcp server %q probe failed: %v", name, err)
	}

	cmd := exec.Command(srv.Command, srv.Args...) //nolint:gosec // declared by the company owner; argv slice, no shell
	client := gomcp.NewClient(&gomcp.Implementation{Name: "asf-probe", Version: "1"}, nil)
	session, err := client.Connect(ctx, &gomcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		return fail(err)
	}
	defer session.Close()

	var tools []string
	byNormalized := map[string]string{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return fail(err)
		}
		if norm := normalize(tool.Name); !unambiguousTool(norm) {
			return nil, fmt.Errorf("pi: mcp server %q tool %q is ambiguous in pi's flat tool namespace: its normalized name %q must not contain \"__\" or start with \"_\"", name, tool.Name, norm)
		}
		full := toolName(name, tool.Name)
		if prev, dup := byNormalized[full]; dup {
			return nil, fmt.Errorf("pi: mcp server %q tools %q and %q both normalize to %q; pi would hash-suffix one of them", name, prev, tool.Name, full)
		}
		byNormalized[full] = tool.Name
		tools = append(tools, full)
	}
	if len(tools) == 0 {
		return nil, fmt.Errorf("pi: mcp server %q exposes no tools", name)
	}
	sort.Strings(tools)
	return tools, nil
}
