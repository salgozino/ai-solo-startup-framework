package supervisor

import (
	"context"
	"os"
	"strings"
	"testing"

	a2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

func finishIntent(answer string) port.ActionIntent {
	return port.ActionIntent{Kind: port.KindFinishTask, Payload: map[string]any{"body": answer}}
}

// S3 backstop: a turn that both delegates and calls finish_task (in either order)
// does not end the task. The finish is discarded and logged, the delegation runs, and
// the loop continues into the next round with the peer result; the task's output is
// the next round's answer, never the discarded finish text. finish_task is a control
// tool: it never reaches a gateway.
func TestFinishTask_DiscardedInDelegatingTurn(t *testing.T) {
	cases := map[string][]port.ActionIntent{
		"finish after delegation":  {delegateIntent("engineer", "draft"), finishIntent("premature answer")},
		"finish before delegation": {finishIntent("premature answer"), delegateIntent("engineer", "draft")},
	}
	for name, intents := range cases {
		t.Run(name, func(t *testing.T) {
			del := &fake.Delegator{Results: map[string]port.DelegationResult{
				"engineer": {State: string(a2a.TaskStateCompleted), Output: "peer draft"},
			}}
			h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
			h.prov.ReturnRunResults = []port.ProviderResult{
				{Output: "turn prose", ActionIntents: intents},
				{Output: "second round answer"},
			}
			last, rec := h.run(t, "finish-in-delegating-turn")
			if last.Status.State != a2a.TaskStateCompleted || rec.State != string(a2a.TaskStateCompleted) {
				t.Fatalf("event/record = %s/%s, want COMPLETED", last.Status.State, rec.State)
			}
			if got := h.prov.RunTaskCallCount(); got != 2 {
				t.Fatalf("RunTask calls = %d, want 2 (the rejected finish must not end the task)", got)
			}
			if len(del.Calls) != 1 {
				t.Errorf("delegations = %d, want the delegation still executed once", len(del.Calls))
			}
			if in := h.prov.RunCalls[1].Input; !strings.Contains(in, "peer draft") {
				t.Errorf("round two must carry the peer result; input = %q", in)
			}
			if rec.Output != "second round answer" || messageText(last.Status.Message) != "second round answer" {
				t.Errorf("record/event output = %q/%q, want the second round's answer", rec.Output, messageText(last.Status.Message))
			}
			if rec.FinishRequested {
				t.Error("the discarded finish must not linger on the record")
			}
			if len(h.gw.Calls) != 0 {
				t.Errorf("gateway sends = %d, a control tool must never reach a gateway", len(h.gw.Calls))
			}
			if !strings.Contains(h.logs.String(), "finish.rejected") {
				t.Errorf("log lacks finish.rejected: %s", h.logs.String())
			}
		})
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
	if strings.Contains(h.logs.String(), "finish.rejected") {
		t.Errorf("a finish-only turn must not log finish.rejected: %s", h.logs.String())
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
