// Package main — wire.go is the ONLY file in the entire codebase that imports
// adapters/claudecode and gateways/telegram. All other packages depend solely on
// core/port interfaces; this file is the seam where concretes are injected.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
	"github.com/salgozino/ai-solo-startup-framework/adapters/opencode"
	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
	"github.com/salgozino/ai-solo-startup-framework/gateways/telegram"
	transa2a "github.com/salgozino/ai-solo-startup-framework/transport/a2a"
	transmcp "github.com/salgozino/ai-solo-startup-framework/transport/mcp"
	"github.com/salgozino/ai-solo-startup-framework/ui"
)

// claudeRegistryMinter adapts *transmcp.Registry to claudecode.TokenMinter.
// It exists in this package (not transport/mcp) because Go requires a method's
// declared return type to be IDENTICAL to the interface's declared return type
// for interface satisfaction — there is no covariant return typing.
// *transmcp.Registry.Mint returns (string, *transmcp.Handle), which does not
// literally match claudecode.TokenMinter.Mint's declared (string, claudecode.Drainer)
// signature even though *transmcp.Handle structurally satisfies claudecode.Drainer.
// This wrapper's Mint method spells the exact return type the interface requires.
// See claudecode.TokenMinter's doc comment for the full rationale.
type claudeRegistryMinter struct {
	reg *transmcp.Registry
}

func (m *claudeRegistryMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, claudecode.Drainer) {
	return m.reg.Mint(tenant, agent, taskID, exp)
}

// opencodeRegistryMinter is claudeRegistryMinter's counterpart for opencode.TokenMinter.
// opencode.Drainer is a distinct type from claudecode.Drainer (each adapter package
// defines its own copy to stay decoupled from both transport/mcp and each other), so a
// separate wrapper type is required even though the underlying registry is the same.
type opencodeRegistryMinter struct {
	reg *transmcp.Registry
}

func (m *opencodeRegistryMinter) Mint(tenant, agent, taskID string, exp time.Time) (string, opencode.Drainer) {
	return m.reg.Mint(tenant, agent, taskID, exp)
}

// policyKindKeys returns the sorted-free set of action kinds declared in policies,
// for Options.PolicyActionKinds (order is not significant to Capabilities() callers).
func policyKindKeys(policies map[string]config.Policy) []string {
	kinds := make([]string, 0, len(policies))
	for k := range policies {
		kinds = append(kinds, k)
	}
	return kinds
}

// modelProber is implemented by adapters that can verify model accessibility
// at startup. Declared here (consumer-side) per Go's structural typing idiom —
// no adapter package needs to import or declare this interface explicitly.
type modelProber interface {
	ProbeModel(ctx context.Context) error
}

// agentRuntime groups the live objects for a single materialized agent.
type agentRuntime struct {
	sup    *supervisor.Supervisor
	srv    *transa2a.Server
	uiAdap *supervisorUIAdapter
	// mcpSrv is the single long-lived MCP server shared by every agentRuntime in a
	// materializeAgents call (same *transmcp.Server instance on each entry). Callers
	// shut it down once (e.g. via runtimes[0].mcpSrv) alongside the A2A servers.
	mcpSrv *transmcp.Server
}

// supervisorUIAdapter bridges supervisor.Supervisor to ui.Supervisor.
// It converts supervisor.TaskRecord → ui.TaskRecord and routes PostVerdict
// through the A2A handler so that approvals arrive as proper SendMessage resumes.
type supervisorUIAdapter struct {
	sup     *supervisor.Supervisor
	handler a2asrv.RequestHandler
	tenant  string
}

// StatusStr implements ui.Supervisor.
func (a *supervisorUIAdapter) StatusStr() string { return a.sup.StatusStr() }

// ListTasks implements ui.Supervisor. Converts supervisor.TaskRecord → ui.TaskRecord.
func (a *supervisorUIAdapter) ListTasks() ([]ui.TaskRecord, error) {
	recs, err := a.sup.ListTasks()
	if err != nil {
		return nil, err
	}
	out := make([]ui.TaskRecord, len(recs))
	for i, r := range recs {
		out[i] = ui.TaskRecord{
			TaskID:            r.TaskID,
			State:             r.State,
			Input:             r.Input,
			PendingIntentKind: r.PendingIntentKind,
			Output:            r.Output,
		}
	}
	return out, nil
}

// PostVerdict implements ui.Supervisor.
// It verifies the task is in INPUT_REQUIRED, then delivers the verdict as a new
// SendMessage carrying the original TaskID — the a2asrv framework recognizes a
// message with TaskID as a resume, routing it to executeResume.
func (a *supervisorUIAdapter) PostVerdict(taskID string, approve bool) error {
	tasks, err := a.sup.ListTasks()
	if err != nil {
		return err
	}
	found := false
	for _, r := range tasks {
		if r.TaskID == taskID {
			if r.State != string(sdka2a.TaskStateInputRequired) {
				return ui.ErrNotInputRequired
			}
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("supervisor: task %q not found", taskID)
	}

	body := "approve"
	if !approve {
		body = "reject"
	}

	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart(body))
	msg.TaskID = sdka2a.TaskID(taskID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = a.handler.SendMessage(ctx, &sdka2a.SendMessageRequest{
		Tenant:  a.tenant,
		Message: msg,
	})
	return err
}

// SendTask implements ui.Supervisor.
// It checks the most recent task state; if the supervisor is in a FAILED task,
// it refuses to send. Otherwise it fires the message as a new SendMessage
// with an empty TaskID in a background goroutine and returns immediately —
// the a2asrv framework treats this as a new task.
func (a *supervisorUIAdapter) SendTask(text string) error {
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart(text))

	// Fire and forget: run SendMessage in background so the UI returns immediately.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, _ = a.handler.SendMessage(ctx, &sdka2a.SendMessageRequest{
			Tenant:  a.tenant,
			Message: msg,
		})
	}()
	return nil
}

// wireOptions controls how agents and gateways are constructed.
// Tests inject fakes through these fields.
type wireOptions struct {
	// providerOverride, when non-nil, is used instead of the real ClaudeCode adapter.
	providerOverride port.Provider
	// gatewayOverride, when non-nil, is used instead of the real Telegram gateway.
	gatewayOverride port.Gateway
	// storeDir overrides the base directory for task stores. Uses os.TempDir() if empty.
	storeDir string
	// stderr captures log/error output in tests; uses os.Stderr when nil.
	stderr io.Writer
	// authTokenOverride, when non-empty, bypasses env-var resolution for the A2A
	// Bearer token. Used in tests to avoid setting environment variables.
	authTokenOverride string
	// mcpServer, when non-nil, is used instead of starting a real transmcp.Server.
	// Used in tests that do not need real MCP wiring (they typically also set
	// providerOverride, bypassing per-adapter MCP registry injection entirely).
	mcpServer *transmcp.Server
}

// mcpServerAddr safely reads srv.Addr(), converting a panic from a never-Start()ed
// server (nil listener) into a plain error. *transmcp.Server.Addr() dereferences its
// listener directly, so a test (or future caller) that injects a *transmcp.Server via
// wireOptions.mcpServer without calling Start() first would otherwise crash
// materializeAgents. This guard lives here — at the injection site in cmd/company —
// rather than inside transport/mcp, which is Phase 2 / PR 2 code and must stay
// untouched on this branch.
func mcpServerAddr(srv *transmcp.Server) (addr string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mcp server address unavailable (server was not started): %v", r)
		}
	}()
	return srv.Addr(), nil
}

// materializeAgents creates and starts one agentRuntime per agent in cfg.
// The CEO supervisor's runtime is returned first if len(runtimes) > 0.
// Callers are responsible for shutting down all returned servers on exit.
func materializeAgents(cfg *config.CompanyConfig, opts wireOptions) (runtimes []*agentRuntime, err error) {
	if opts.stderr == nil {
		opts.stderr = os.Stderr
	}

	// Build the shared gateway (or use override from tests).
	var gw port.Gateway
	if opts.gatewayOverride != nil {
		gw = opts.gatewayOverride
	} else if tg := cfg.Gateways.Telegram; tg != nil {
		recipientEnv := tg.RecipientEnv
		if recipientEnv == "" {
			recipientEnv = "TELEGRAM_OWNER_ID"
		}
		var gwErr error
		gw, gwErr = telegram.New(tg.TokenEnv, recipientEnv)
		if gwErr != nil {
			return nil, fmt.Errorf("wire: telegram gateway: %w", gwErr)
		}
	}

	// Shared policy engine — one instance per company so tokens are cross-verifiable.
	policyEngine := policy.NewEngine()

	// Start the single long-lived MCP server before any supervisor is marked ready.
	// Bind failure aborts materialize immediately — no adapter is invoked
	// (spec: mcp-tool-server "Server startup failure aborts materialize").
	ownedMCP := opts.mcpServer == nil
	mcpSrv := opts.mcpServer
	if ownedMCP {
		mcpSrv = transmcp.New(cfg.Tenant, cfg.RiskPolicy, transmcp.NewRegistry())
		if startErr := mcpSrv.Start("127.0.0.1:0"); startErr != nil {
			return nil, fmt.Errorf("wire: mcp server: %w", startErr)
		}
	}

	// Ensure the MCP server this call started is torn down on every error path out of
	// this function. A server injected via opts.mcpServer is owned by the caller (tests)
	// and must never be shut down here (threat: "MCP server leaks on partial materialize
	// failure" — every return nil, err below this point previously left an owned,
	// already-bound listener leaked on the unknown-provider, A2A-start-failure, and
	// probe-failure paths).
	defer func() {
		if err != nil && ownedMCP && mcpSrv != nil {
			_ = mcpSrv.Shutdown(context.Background())
		}
	}()

	mcpAddr, addrErr := mcpServerAddr(mcpSrv)
	if addrErr != nil {
		return nil, fmt.Errorf("wire: mcp server: %w", addrErr)
	}
	if ownedMCP {
		fmt.Fprintf(opts.stderr, "mcp: server listening on %s\n", mcpAddr)
	}
	mcpActionKinds := policyKindKeys(cfg.RiskPolicy)

	// Determine store base directory.
	storeBase := opts.storeDir
	if storeBase == "" {
		storeBase = filepath.Join(os.TempDir(), "company-store-"+cfg.Tenant)
	}

	// Resolve the shared A2A auth token: use the override when provided (tests),
	// otherwise look up the env var declared in company.yaml.
	authToken := opts.authTokenOverride
	if authToken == "" {
		authToken = os.Getenv(cfg.AuthTokenEnv)
	}
	if authToken == "" {
		return nil, fmt.Errorf("wire: env var %q (auth_token_env) is not set or empty", cfg.AuthTokenEnv)
	}

	runtimes = make([]*agentRuntime, 0, len(cfg.Agents))

	for _, agCfg := range cfg.Agents {
		addr, err := address.New(agCfg.Name, cfg.Tenant)
		if err != nil {
			return nil, fmt.Errorf("wire: address for %q: %w", agCfg.Name, err)
		}

		store, err := supervisor.NewStore(filepath.Join(storeBase, agCfg.Name))
		if err != nil {
			return nil, fmt.Errorf("wire: store for %q: %w", agCfg.Name, err)
		}

		var prov port.Provider
		if opts.providerOverride != nil {
			prov = opts.providerOverride
		} else {
			switch agCfg.Provider {
			case "claude-code":
				prov = claudecode.New("claude", claudecode.Options{
					MCPRegistry:       &claudeRegistryMinter{reg: mcpSrv.Registry()},
					MCPServerAddr:     mcpAddr,
					Tenant:            cfg.Tenant,
					AgentName:         agCfg.Name,
					PolicyActionKinds: mcpActionKinds,
					// mcpSrv.Err reports the shared MCP server's abnormal-death state (see
					// transport/mcp/server.go). Without this, a dead MCP server left every
					// RunTask call reporting a false "success" with empty ActionIntents,
					// indistinguishable from an agent that simply made no tool calls.
					MCPHealthCheck: mcpSrv.Err,
				}, agCfg.Model, agCfg.SystemPrompt)
			case "opencode":
				prov = opencode.New("opencode", opencode.Options{
					MCPRegistry:       &opencodeRegistryMinter{reg: mcpSrv.Registry()},
					MCPServerAddr:     mcpAddr,
					Tenant:            cfg.Tenant,
					AgentName:         agCfg.Name,
					PolicyActionKinds: mcpActionKinds,
					MCPHealthCheck:    mcpSrv.Err,
				}, agCfg.Model, agCfg.Name, agCfg.SystemPrompt)
			default:
				return nil, fmt.Errorf("wire: unknown provider %q for agent %q", agCfg.Provider, agCfg.Name)
			}

			// Validate model accessibility before any server binds. The probe runs with
			// a 15-second deadline per agent, sequentially. Adapters that do not implement
			// modelProber skip the probe (structural opt-in — no interface change to port/).
			if prober, ok := prov.(modelProber); ok {
				probeCtx, probeCancel := context.WithTimeout(context.Background(), 15*time.Second)
				probeErr := prober.ProbeModel(probeCtx)
				probeCancel()
				if probeErr != nil {
					return nil, fmt.Errorf("agent %q model probe failed: %w", agCfg.Name, probeErr)
				}
			}
		}

		sup, err := supervisor.New(supervisor.Config{
			Addr:         addr,
			Provider:     prov,
			Store:        store,
			PolicyEngine: policyEngine,
			Gateway:      gw,
			Role:         agCfg.Role,
			PolicyConfig: cfg.RiskPolicy,
		})
		if err != nil {
			return nil, fmt.Errorf("wire: supervisor for %q: %w", agCfg.Name, err)
		}

		srv, err := transa2a.New(sup, authToken)
		if err != nil {
			return nil, fmt.Errorf("wire: transport for %q: %w", agCfg.Name, err)
		}

		adap := &supervisorUIAdapter{
			sup:     sup,
			handler: srv.Handler(),
			tenant:  cfg.Tenant,
		}

		runtimes = append(runtimes, &agentRuntime{
			sup:    sup,
			srv:    srv,
			uiAdap: adap,
			mcpSrv: mcpSrv,
		})

		fmt.Fprintf(opts.stderr, "wire: agent %q started at %s (role=%s)\n",
			agCfg.Name, srv.BaseURL(), agCfg.Role)
	}

	return runtimes, nil
}
