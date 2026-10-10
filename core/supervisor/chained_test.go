// Tests for chained approval (Slice 8, T17/T18 of odd/tasks/ceo-orchestration-loop.md).
//
// A peer that escalates to INPUT_REQUIRED awaits ITS human's verdict. The delegating
// task stays parked on the peer, flagged AwaitingPeerApproval, and is resumed only
// when the peer becomes terminal; the human approves once, on the peer's task.
package supervisor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

func peerEscalated() port.DelegationResult {
	return port.DelegationResult{PeerTaskID: "P1", State: string(sdka2a.TaskStateInputRequired)}
}

func peerEnded(state sdka2a.TaskState) port.DelegationResult {
	return port.DelegationResult{PeerTaskID: "P1", State: string(state)}
}

// T17/T18: replaces TestPark_PeerInputRequiredFailsTask. A peer that escalates
// while the CEO is parked does not fail the CEO task, does not start a new round,
// and is flagged as awaiting the peer's human approval until the peer finishes.
func TestChained_PeerEscalationKeepsCEOParkedUntilPeerTerminal(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")},
		peerWorking(), peerEscalated(), peerEscalated(), peerEscalated(), peerDone("peer out"))
	collectStatusEvents(context.Background(), h.sup, newExecCtx("chain-1", "go"))

	rec := waitForRecord(t, h.sup, "chain-1", func(r TaskRecord) bool { return r.AwaitingPeerApproval })
	if rec.State != string(sdka2a.TaskStateInputRequired) || rec.AwaitingPeerTaskID != "P1" || rec.PendingIntentKind != "" {
		t.Fatalf("record = %+v, want parked on P1 awaiting its approval", rec)
	}
	if got := h.prov.RunTaskCallCount(); got != 1 {
		t.Fatalf("RunTask calls = %d while the peer awaits approval, want 1 (no new round)", got)
	}

	// The peer finishes after its human approved: the CEO resumes into round two.
	done := waitForRecord(t, h.sup, "chain-1", isState(sdka2a.TaskStateCompleted))
	if done.AwaitingPeerApproval || done.AwaitingPeerTaskID != "" {
		t.Errorf("awaiting fields must clear on completion; got %+v", done)
	}
	if got := h.prov.RunTaskCallCount(); got != 2 {
		t.Fatalf("RunTask calls = %d, want 2", got)
	}
	if !strings.Contains(h.prov.RunCalls[1].Input, "peer out") {
		t.Errorf("round two must carry the peer output; input = %q", h.prov.RunCalls[1].Input)
	}
}

// A peer that escalated at the moment of delegation (Delegate already reports
// INPUT_REQUIRED) parks the CEO instead of failing it. Replaces the
// "INPUT_REQUIRED escalation ... not yet wired" case of
// TestExecuteDelegation_ErrorTextNamesTheFailure.
func TestChained_ImmediatePeerEscalationParksCEO(t *testing.T) {
	del := &fake.Delegator{
		Results:       map[string]port.DelegationResult{"engineer": peerEscalated()},
		StateSequence: []port.DelegationResult{peerEscalated()},
	}
	h := wrapPark(t, newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0))
	events := collectStatusEvents(context.Background(), h.sup, newExecCtx("chain-2", "go"))

	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("state = %v, want a parked %v", got, sdka2a.TaskStateInputRequired)
	}
	rec, _ := h.sup.cfg.Store.Load(h.sup.cfg.Addr, "chain-2")
	if !rec.AwaitingPeerApproval || rec.AwaitingPeerTaskID != "P1" {
		t.Errorf("record = %+v, want awaiting the peer's approval on P1", rec)
	}
}

// Once the peer's human approved and the peer is running again, the CEO is no
// longer awaiting an approval: the flag follows the peer.
func TestChained_ApprovalFlagClearsWhenPeerResumesWork(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")},
		peerEscalated(), peerEscalated(), peerWorking(), peerWorking())
	// A wider poll interval keeps the flagged window longer than waitForRecord's step.
	h.sup.cfg.PeerPollInterval = 30 * time.Millisecond
	collectStatusEvents(context.Background(), h.sup, newExecCtx("chain-3", "go"))

	waitForRecord(t, h.sup, "chain-3", func(r TaskRecord) bool { return r.AwaitingPeerApproval })
	rec := waitForRecord(t, h.sup, "chain-3", func(r TaskRecord) bool { return !r.AwaitingPeerApproval })
	if rec.State != string(sdka2a.TaskStateInputRequired) || rec.AwaitingPeerTaskID != "P1" {
		t.Errorf("record = %+v, want still parked on P1", rec)
	}
}

// (Sequences repeat each state because a resume re-reads the peer, consuming one.)
// The peer's human rejects: the rejection is the peer turn the CEO reads and
// judges in its next round, not a failure of the CEO task.
func TestChained_PeerRejectionBecomesPeerTurn(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")},
		peerEscalated(), peerEscalated(), peerEnded(sdka2a.TaskStateRejected), peerEnded(sdka2a.TaskStateRejected))
	collectStatusEvents(context.Background(), h.sup, newExecCtx("chain-4", "go"))

	rec := waitForRecord(t, h.sup, "chain-4", isState(sdka2a.TaskStateCompleted))
	if got := h.prov.RunTaskCallCount(); got != 2 {
		t.Fatalf("RunTask calls = %d, want 2 (the CEO judges the rejection)", got)
	}
	if in := h.prov.RunCalls[1].Input; !strings.Contains(in, "REJECTED") {
		t.Errorf("round two must carry the rejection; input = %q", in)
	}
	if rec.Output != agentOwnOutput {
		t.Errorf("Output = %q, want the CEO's own answer", rec.Output)
	}
}

// Recovery: the awaiting-approval marker is persisted, so a restarted supervisor
// re-arms a watcher that keeps waiting through the escalation and resumes the CEO
// only when the peer finishes.
func TestChained_RestartKeepsWaitingThenResumes(t *testing.T) {
	first := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")}, peerEscalated())
	collectStatusEvents(context.Background(), first.sup, newExecCtx("chain-5", "go"))
	waitForRecord(t, first.sup, "chain-5", func(r TaskRecord) bool { return r.AwaitingPeerApproval })
	first.sup.StopWatchers()

	del := &fake.Delegator{StateSequence: []port.DelegationResult{peerEscalated(), peerEscalated(), peerDone("peer out")}}
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "final after restart"}}
	second := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	second.sup.cfg.Store = first.sup.cfg.Store
	second.sup.cfg.Provider = prov
	h := wrapPark(t, second)

	noRegister := func(context.Context, string, string) error { return nil }
	if err := second.sup.RecoverOpenTasks(context.Background(), nil, noRegister); err != nil {
		t.Fatalf("RecoverOpenTasks: %v", err)
	}
	waitForRecord(t, second.sup, "chain-5", isState(sdka2a.TaskStateCompleted))
	if len(prov.RunCalls) != 1 || !strings.Contains(prov.RunCalls[0].Input, "peer out") {
		t.Fatalf("post-restart calls = %+v, want one round carrying the peer output", prov.RunCalls)
	}
	// Already-flagged record: the escalated polls must not have woken the CEO.
	for _, in := range h.resumer.inputs {
		if in != resumeInputPeerUpdate {
			t.Errorf("unexpected resume input %q", in)
		}
	}
	if len(h.resumer.inputs) != 1 {
		t.Errorf("resumes = %d, want exactly 1 (the terminal peer)", len(h.resumer.inputs))
	}
}

// Compat: a record written before AwaitingPeerApproval existed still loads, and
// the new field stays out of the JSON when unset.
func TestStore_LoadsRecordWrittenBeforeAwaitingPeerApproval(t *testing.T) {
	legacy := `{"task_id":"t","state":"TASK_STATE_INPUT_REQUIRED","input":"go","awaiting_peer_role":"engineer","awaiting_peer_task_id":"P1"}`
	var rec TaskRecord
	if err := json.Unmarshal([]byte(legacy), &rec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if rec.AwaitingPeerApproval || rec.AwaitingPeerTaskID != "P1" {
		t.Errorf("rec = %+v", rec)
	}
	out, _ := json.Marshal(rec)
	if strings.Contains(string(out), "awaiting_peer_approval") {
		t.Errorf("omitempty must keep awaiting_peer_approval out; got %s", out)
	}
}
