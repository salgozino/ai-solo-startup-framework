package main

import (
	"encoding/base64"
	"fmt"

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
			if task.FollowUpOf != "" {
				// The UI keys tasks by verdict ID, so the link must use the same key.
				task.FollowUpOf = a.verdictID(agent, task.FollowUpOf)
			}
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

// FollowUp only serves the CEO (runtimes[0]): id is resolved with the same
// keying as PostVerdict, and any other id — other agents' tasks included — is
// reported as not found so it can never reach a peer supervisor.
func (a *companyUIAdapter) FollowUp(id, text string) error {
	ceo := a.runtimes[0]
	tasks, err := ceo.uiAdap.ListTasks()
	if err != nil {
		return err
	}
	agent := ceo.sup.Status().Addr.Name()
	for _, task := range tasks {
		if a.verdictID(agent, task.TaskID) == id {
			return ceo.uiAdap.FollowUp(task.TaskID, text)
		}
	}
	return fmt.Errorf("%w: %q", ui.ErrTaskNotFound, id)
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
