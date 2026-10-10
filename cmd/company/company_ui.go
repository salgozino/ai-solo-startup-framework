package main

import (
	"encoding/base64"

	"github.com/salgozino/ai-solo-startup-framework/ui"
)

// companyUIAdapter keeps human task submission and lifecycle status on the CEO.
// The runtime list is non-empty, as enforced by runMaterialize.
type companyUIAdapter struct {
	runtimes []*agentRuntime
}

func (a *companyUIAdapter) StatusStr() string { return a.runtimes[0].uiAdap.StatusStr() }
func (a *companyUIAdapter) SendTask(text string) error {
	return a.runtimes[0].uiAdap.SendTask(text)
}

// verdictID preserves the single-agent API. In multi-agent companies every key
// is qualified, never inferred from a bare task ID. The delimiter is outside
// the base64url alphabet, so arbitrary agent names and task IDs cannot collide.
func (a *companyUIAdapter) verdictID(agent, task string) string {
	if len(a.runtimes) == 1 {
		return task
	}
	return base64.RawURLEncoding.EncodeToString([]byte(agent)) + "." + base64.RawURLEncoding.EncodeToString([]byte(task))
}

func (a *companyUIAdapter) ListTasks() ([]ui.TaskRecord, error) {
	out := make([]ui.TaskRecord, 0)
	for _, rt := range a.runtimes {
		tasks, err := rt.uiAdap.ListTasks()
		if err != nil {
			return nil, err
		}
		agent := rt.sup.Status().Addr.Name()
		for _, task := range tasks {
			task.Agent = agent
			task.VerdictID = a.verdictID(agent, task.TaskID)
			out = append(out, task)
		}
	}
	return out, nil
}

func (a *companyUIAdapter) PostVerdict(id string, approve bool) error {
	for _, rt := range a.runtimes {
		tasks, err := rt.uiAdap.ListTasks()
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if a.verdictID(rt.sup.Status().Addr.Name(), task.TaskID) == id {
				return rt.uiAdap.PostVerdict(task.TaskID, approve)
			}
		}
	}
	// Unknown or unqualified multi-agent IDs must never reach a supervisor.
	return ui.ErrNotInputRequired
}

func newCompanyUIHandler(runtimes []*agentRuntime) *ui.UIHandler {
	a := &companyUIAdapter{runtimes: runtimes}
	h := ui.NewUIHandler(a)
	for _, rt := range runtimes {
		rt.sup.SetOnStateChange(func() {
			tasks, err := a.ListTasks()
			if err != nil {
				return
			}
			h.Broadcast(map[string]any{"supervisor": a.StatusStr(), "tasks": tasks})
		})
	}
	return h
}
