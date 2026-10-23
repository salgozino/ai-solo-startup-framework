package supervisor

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// newFollowUpExecCtx builds the execution context of a client follow-up: a new
// task whose message references the task it follows, in the given A2A context.
func newFollowUpExecCtx(taskID, text, contextID string, refs ...string) *a2asrv.ExecutorContext {
	ec := newExecCtx(taskID, text)
	ec.ContextID = contextID
	for _, r := range refs {
		ec.Message.ReferenceTasks = append(ec.Message.ReferenceTasks, sdka2a.TaskID(r))
	}
	return ec
}

func runTask(t *testing.T, sup *Supervisor, ec *a2asrv.ExecutorContext) (*sdka2a.TaskStatusUpdateEvent, TaskRecord) {
	t.Helper()
	last := terminalEvent(t, collectStatusEvents(context.Background(), sup, ec))
	return last, recordOf(t, sup, string(ec.TaskID))
}

func seedRecord(t *testing.T, sup *Supervisor, rec TaskRecord) {
	t.Helper()
	if err := sup.cfg.Store.Save(sup.cfg.Addr, rec); err != nil {
		t.Fatalf("seed %s: %v", rec.TaskID, err)
	}
}

// S2, S3: a follow-up of a follow-up inherits the whole chain, oldest first, and
// leaves the tasks it follows untouched.
func TestFollowUp_InheritsWholeChain(t *testing.T) {
	h := newDelegatingSupervisor(t, "ceo", nil, nil, 0)
	at := time.Unix(1, 0)
	seedRecord(t, h.sup, TaskRecord{
		TaskID: "T1", State: string(sdka2a.TaskStateCompleted), Input: "write a draft",
		ContextID: "ctx-1", Output: "draft v1",
		Turns: []port.ContextMessage{{Role: "user", Content: "write a draft", At: at}, {Role: "assistant", Content: "draft v1", At: at}},
	})
	t1 := recordOf(t, h.sup, "T1")

	h.prov.ReturnRunResults = nil
	h.prov.ReturnRunResult = port.ProviderResult{Output: "draft v2"}
	last, t2 := runTask(t, h.sup, newFollowUpExecCtx("T2", "make it longer", "ctx-1", "T1"))
	if last.Status.State != sdka2a.TaskStateCompleted || t2.State != string(sdka2a.TaskStateCompleted) {
		t.Fatalf("T2 event/record = %s/%s, want COMPLETED", last.Status.State, t2.State)
	}
	if t2.FollowUpOf != "T1" || t2.ContextID != "ctx-1" || t2.Input != "make it longer" || t2.Output != "draft v2" {
		t.Errorf("T2 = follow_up_of %q, context %q, input %q, output %q", t2.FollowUpOf, t2.ContextID, t2.Input, t2.Output)
	}

	h.prov.ReturnRunResult = port.ProviderResult{Output: "draft v3"}
	_, t3 := runTask(t, h.sup, newFollowUpExecCtx("T3", "make it shorter", "ctx-1", "T2"))
	if t3.FollowUpOf != "T2" || t3.State != string(sdka2a.TaskStateCompleted) {
		t.Fatalf("T3 = follow_up_of %q state %s, want T2/COMPLETED", t3.FollowUpOf, t3.State)
	}

	input := h.prov.RunCalls[1].Input
	pos := -1
	for _, want := range []string{"write a draft", "draft v1", "make it longer", "draft v2", "make it shorter"} {
		i := strings.Index(input[pos+1:], want)
		if i < 0 {
			t.Fatalf("T3 first input lacks %q after position %d:\n%s", want, pos, input)
		}
		pos += 1 + i
	}
	if !strings.Contains(input, "T2") || !strings.Contains(input, string(sdka2a.TaskStateCompleted)) {
		t.Errorf("T3 input does not name the followed task and its outcome:\n%s", input)
	}
	if got := recordOf(t, h.sup, "T1"); !reflect.DeepEqual(got, t1) {
		t.Errorf("T1 changed by its follow-ups:\n got %+v\nwant %+v", got, t1)
	}
	if got := recordOf(t, h.sup, "T2"); !reflect.DeepEqual(got, t2) {
		t.Errorf("T2 changed by its follow-up:\n got %+v\nwant %+v", got, t2)
	}
}

// S1: every terminal state can be followed up, and the outcome the client is
// reacting to (state and output) reaches the model.
func TestFollowUp_AnyTerminalState(t *testing.T) {
	for _, state := range []sdka2a.TaskState{sdka2a.TaskStateCompleted, sdka2a.TaskStateFailed, sdka2a.TaskStateRejected, sdka2a.TaskStateCanceled} {
		t.Run(string(state), func(t *testing.T) {
			h := newDelegatingSupervisor(t, "ceo", nil, nil, 0)
			seedRecord(t, h.sup, TaskRecord{
				TaskID: "T1", State: string(state), Input: "ship it", Output: "what happened",
				Turns: []port.ContextMessage{{Role: "user", Content: "ship it"}},
			})
			last, rec := runTask(t, h.sup, newFollowUpExecCtx("T2", "try another way", "", "T1"))
			if last.Status.State != sdka2a.TaskStateCompleted || rec.FollowUpOf != "T1" {
				t.Fatalf("follow-up of %s = %s, follow_up_of %q", state, last.Status.State, rec.FollowUpOf)
			}
			input := h.prov.RunCalls[0].Input
			for _, want := range []string{"ship it", string(state), "what happened", "try another way"} {
				if !strings.Contains(input, want) {
					t.Errorf("first input lacks %q:\n%s", want, input)
				}
			}
		})
	}
}

// S4: a follow-up of an unknown or still-open task, or naming several tasks,
// fails without invoking the provider and names the problem.
func TestFollowUp_RefusesInvalidReference(t *testing.T) {
	cases := []struct {
		name string
		refs []string
		want string
	}{
		{"unknown", []string{"nope"}, `follow-up of unknown task "nope"`},
		{"working", []string{"T1"}, `follow-up of task "T1": task is not terminal (TASK_STATE_WORKING)`},
		{"parked", []string{"T2"}, `follow-up of task "T2": task is not terminal (TASK_STATE_INPUT_REQUIRED)`},
		{"several", []string{"T3", "T4"}, "follow-up must reference exactly one task, got 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDelegatingSupervisor(t, "ceo", nil, nil, 0)
			seeded := []TaskRecord{
				{TaskID: "T1", State: string(sdka2a.TaskStateWorking), Input: "a"},
				{TaskID: "T2", State: string(sdka2a.TaskStateInputRequired), Input: "b"},
				{TaskID: "T3", State: string(sdka2a.TaskStateCompleted), Input: "c"},
				{TaskID: "T4", State: string(sdka2a.TaskStateCompleted), Input: "d"},
			}
			for _, r := range seeded {
				seedRecord(t, h.sup, r)
			}
			last, rec := runTask(t, h.sup, newFollowUpExecCtx("F", "change it", "", tc.refs...))
			if last.Status.State != sdka2a.TaskStateFailed || rec.State != string(sdka2a.TaskStateFailed) {
				t.Fatalf("event/record = %s/%s, want FAILED", last.Status.State, rec.State)
			}
			if rec.Output != "supervisor: "+tc.want {
				t.Errorf("output = %q, want %q", rec.Output, "supervisor: "+tc.want)
			}
			if n := h.prov.RunTaskCallCount(); n != 0 {
				t.Errorf("RunTask calls = %d, want 0", n)
			}
			for _, r := range seeded {
				if got := recordOf(t, h.sup, r.TaskID); !reflect.DeepEqual(got, r) {
					t.Errorf("%s changed: %+v", r.TaskID, got)
				}
			}
		})
	}
}

// S3: rounds inherited from the followed chain never count toward the cap.
func TestFollowUp_OwnRoundBudget(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {State: string(sdka2a.TaskStateCompleted), Output: "peer fix"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	h.sup.cfg.MaxRounds = 2
	seedRecord(t, h.sup, TaskRecord{
		TaskID: "T1", State: string(sdka2a.TaskStateCompleted), Input: "build", Output: "built",
		Turns: []port.ContextMessage{
			{Role: "user", Content: "build"}, {Role: "assistant", Content: "delegating"},
			{Role: "peer", Content: "done"}, {Role: "assistant", Content: "built"},
		},
	})
	h.prov.ReturnRunResults = []port.ProviderResult{{Output: "fixing", ActionIntents: []port.ActionIntent{delegateIntent("engineer", "fix")}}}
	h.prov.ReturnRunResult = port.ProviderResult{Output: "fixed"}
	last, rec := runTask(t, h.sup, newFollowUpExecCtx("T2", "it is broken", "", "T1"))
	if last.Status.State != sdka2a.TaskStateCompleted || rec.Output != "fixed" {
		t.Fatalf("follow-up = %s output %q, want COMPLETED fixed", last.Status.State, rec.Output)
	}
	if n := h.prov.RunTaskCallCount(); n != 2 {
		t.Errorf("RunTask calls = %d, want 2", n)
	}
}

// A task with no references is not a follow-up: its record carries no link and
// its first provider input stays the client's message, byte for byte.
func TestFollowUp_PlainTaskUnchanged(t *testing.T) {
	h := newDelegatingSupervisor(t, "ceo", nil, nil, 0)
	_, rec := runTask(t, h.sup, newFollowUpExecCtx("T1", "hello", "ctx-9"))
	if rec.FollowUpOf != "" || rec.ContextID != "ctx-9" || rec.BaseTurns != 0 {
		t.Errorf("plain task = follow_up_of %q context %q base %d", rec.FollowUpOf, rec.ContextID, rec.BaseTurns)
	}
	if got := h.prov.RunCalls[0].Input; got != "hello" {
		t.Errorf("first input = %q, want hello", got)
	}
}
