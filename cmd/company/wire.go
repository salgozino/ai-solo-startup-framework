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

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/adapters/claudecode"
	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
	"github.com/salgozino/ai-solo-startup-framework/gateways/telegram"
	transa2a "github.com/salgozino/ai-solo-startup-framework/transport/a2a"
	"github.com/salgozino/ai-solo-startup-framework/ui"
)

// agentRuntime groups the live objects for a single materialized agent.
type agentRuntime struct {
	sup    *supervisor.Supervisor
	srv    *transa2a.Server
	uiAdap *supervisorUIAdapter
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

	_, err = a.handler.SendMessage(context.Background(), &sdka2a.SendMessageRequest{
		Tenant:  a.tenant,
		Message: msg,
	})
	return err
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
}

// materializeAgents creates and starts one agentRuntime per agent in cfg.
// The CEO supervisor's runtime is returned first if len(runtimes) > 0.
// Callers are responsible for shutting down all returned servers on exit.
func materializeAgents(cfg *config.CompanyConfig, opts wireOptions) ([]*agentRuntime, error) {
	if opts.stderr == nil {
		opts.stderr = os.Stderr
	}

	// Build the shared gateway (or use override from tests).
	var gw port.Gateway
	if opts.gatewayOverride != nil {
		gw = opts.gatewayOverride
	} else if tg := cfg.Gateways.Telegram; tg != nil {
		var err error
		recipientEnv := tg.RecipientEnv
		if recipientEnv == "" {
			recipientEnv = "TELEGRAM_OWNER_ID"
		}
		gw, err = telegram.New(tg.TokenEnv, recipientEnv)
		if err != nil {
			return nil, fmt.Errorf("wire: telegram gateway: %w", err)
		}
	}

	// Shared policy engine — one instance per company so tokens are cross-verifiable.
	policyEngine := policy.NewEngine()

	// Determine store base directory.
	storeBase := opts.storeDir
	if storeBase == "" {
		storeBase = filepath.Join(os.TempDir(), "company-store-"+cfg.Tenant)
	}

	runtimes := make([]*agentRuntime, 0, len(cfg.Agents))

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
			// Only claude-code provider is supported in v1.
			if agCfg.Provider != "claude-code" {
				return nil, fmt.Errorf("wire: unknown provider %q for agent %q", agCfg.Provider, agCfg.Name)
			}
			prov = claudecode.New("claude", claudecode.Options{})
		}

		sup := supervisor.New(supervisor.Config{
			Addr:         addr,
			Provider:     prov,
			Store:        store,
			PolicyEngine: policyEngine,
			Gateway:      gw,
			Role:         agCfg.Role,
			PolicyConfig: cfg.RiskPolicy,
		})

		srv, err := transa2a.New(sup)
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
		})

		fmt.Fprintf(opts.stderr, "wire: agent %q started at %s (role=%s)\n",
			agCfg.Name, srv.BaseURL(), agCfg.Role)
	}

	return runtimes, nil
}
