package supervisor

import (
	"context"
	"os"
	"testing"

	a2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

func finishIntent(answer string) port.ActionIntent {
	return port.ActionIntent{Kind: port.KindFinishTask, Payload: map[string]any{"body": answer}}
}

// T10(a): finish_task ends the loop after the turn that called it, even when the
// same turn also delegated, and its text — not the turn's prose — is the Output.
// finish_task is a supervisor control tool: it is absent from PolicyConfig here, so
// reaching the policy engine would HardDeny it and end the task REJECTED.
func TestFinishTask_EndsLoopAfterDelegatingTurn(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {State: string(a2a.TaskStateCompleted), Output: "peer draft"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	h.prov.ReturnRunResults = []port.ProviderResult{{
		Output:        "turn prose",
		ActionIntents: []port.ActionIntent{delegateIntent("engineer", "draft"), finishIntent("final answer")},
	}}
	last, rec := h.run(t, "finish-after-delegation")
	if last.Status.State != a2a.TaskStateCompleted || rec.State != string(a2a.TaskStateCompleted) {
		t.Fatalf("event/record = %s/%s, want COMPLETED", last.Status.State, rec.State)
	}
	if got := h.prov.RunTaskCallCount(); got != 1 {
		t.Errorf("RunTask calls = %d, want 1 (finish_task ends the loop)", got)
	}
	if len(del.Calls) != 1 {
		t.Errorf("delegations = %d, want the same-turn delegation still executed", len(del.Calls))
	}
	if rec.Output != "final answer" || messageText(last.Status.Message) != "final answer" {
		t.Errorf("record/event output = %q/%q, want the finish_task text", rec.Output, messageText(last.Status.Message))
	}
	if len(h.gw.Calls) != 0 {
		t.Errorf("gateway sends = %d, a control tool must never reach a gateway", len(h.gw.Calls))
	}
}

func TestFinishTask_AloneCompletesWithItsText(t *testing.T) {
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{finishIntent("final answer")}, nil, 0)
	last, rec := h.run(t, "finish-alone")
	if last.Status.State != a2a.TaskStateCompleted || rec.Output != "final answer" {
		t.Fatalf("state/output = %s/%q, want COMPLETED/final answer", last.Status.State, rec.Output)
	}
	if got := h.prov.RunTaskCallCount(); got != 1 {
		t.Errorf("RunTask calls = %d, want 1", got)
	}
}

// A turn with neither delegation nor finish_task still ends with the turn's own output.
func TestFinishTask_AbsentKeepsSlice3Rule(t *testing.T) {
	h := newDelegatingSupervisor(t, "ceo", nil, nil, 0)
	last, rec := h.run(t, "no-signal")
	if last.Status.State != a2a.TaskStateCompleted || rec.Output != agentOwnOutput {
		t.Fatalf("state/output = %s/%q, want COMPLETED/%q", last.Status.State, rec.Output, agentOwnOutput)
	}
}

// T10(b) lives in loop_test.go: TestDelegationLoop_RoundExhaustionIsTerminal proves a
// model that keeps delegating without finish_task ends FAILED at the round cap.

// failingSaveDelegator makes the store unwritable once the first delegation happens,
// so the Store.Save between rounds fails. Requires a non-root user (chmod is ignored by root).
type failingSaveDelegator struct {
	port.Delegator
	dir string
}

func (d failingSaveDelegator) Delegate(ctx context.Context, role, body string) (port.DelegationResult, error) {
	res, err := d.Delegator.Delegate(ctx, role, body)
	_ = os.Chmod(d.dir, 0o500)
	return res, err
}

// R3-untested-mid-loop-failures: a Save failure between rounds ends FAILED, never a
// hang or a second provider call.
func TestDelegationLoop_SaveFailureBetweenRoundsFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod cannot make a directory unwritable for root")
	}
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {State: string(a2a.TaskStateCompleted), Output: "peer draft"},
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "draft")}, del, 0)
	dir := h.sup.cfg.Store.dir
	h.sup.cfg.Delegator = failingSaveDelegator{Delegator: del, dir: dir}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	events := collectStatusEvents(context.Background(), h.sup, newExecCtx("save-fails", "please delegate"))
	last := terminalEvent(t, events)
	if last.Status.State != a2a.TaskStateFailed {
		t.Fatalf("terminal event = %s, want FAILED", last.Status.State)
	}
	if got := h.prov.RunTaskCallCount(); got != 1 {
		t.Errorf("RunTask calls = %d, want 1 (no continuing after a failed save)", got)
	}
}
