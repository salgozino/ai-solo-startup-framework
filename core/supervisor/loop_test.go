package supervisor

import (
	"strconv"
	"strings"
	"testing"

	a2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

func TestDelegationLoop_FeedsPeerOutputToSecondTurn(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {State: string(a2a.TaskStateCompleted), Output: "peer draft"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	h.prov.ReturnRunResults = []port.ProviderResult{{
		Output: "requesting draft", ActionIntents: []port.ActionIntent{delegateIntent("engineer", "draft")},
	}}
	h.prov.ReturnRunResult = port.ProviderResult{Output: "CEO final answer"}
	last, rec := h.run(t, "feedback")
	if got := h.prov.RunTaskCallCount(); got != 2 {
		t.Fatalf("RunTask calls = %d, want 2", got)
	}
	if h.prov.RunCalls[0].Input != "please delegate" {
		t.Fatalf("first input changed: %q", h.prov.RunCalls[0].Input)
	}
	for _, text := range []string{"please delegate", "requesting draft", "peer draft"} {
		if !strings.Contains(h.prov.RunCalls[1].Input, text) {
			t.Errorf("second input %q lacks %q", h.prov.RunCalls[1].Input, text)
		}
	}
	if rec.Output != "CEO final answer" || messageText(last.Status.Message) != rec.Output {
		t.Errorf("record/event output = %q/%q, want CEO final answer", rec.Output, messageText(last.Status.Message))
	}
	if len(rec.Turns) != 4 {
		t.Fatalf("persisted turns = %d, want user, assistant, peer, assistant", len(rec.Turns))
	}
	for i, role := range []string{"user", "assistant", "peer", "assistant"} {
		if rec.Turns[i].Role != role || rec.Turns[i].At.IsZero() {
			t.Errorf("turn %d = %+v, want timestamped %s", i, rec.Turns[i], role)
		}
	}
}

func TestDelegationLoop_RoundExhaustionIsTerminal(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {State: string(a2a.TaskStateCompleted), Output: "again"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	h.prov.ReturnRunResults = nil // delegate on every turn, including the first
	h.prov.ReturnRunResult = port.ProviderResult{Output: "keep going", ActionIntents: []port.ActionIntent{delegateIntent("engineer", "repeat")}}
	last, rec := h.run(t, "exhaustion")
	if last.Status.State != a2a.TaskStateFailed || rec.State != string(a2a.TaskStateFailed) {
		t.Fatalf("round exhaustion event/record = %s/%s, want FAILED", last.Status.State, rec.State)
	}
	if !strings.Contains(messageText(last.Status.Message), "round limit") || !strings.Contains(rec.Output, "round limit") {
		t.Errorf("exhaustion reason missing from event/record: %q/%q", messageText(last.Status.Message), rec.Output)
	}
	if got := h.prov.RunTaskCallCount(); got != 8 {
		t.Errorf("RunTask calls = %d, want default cap 8", got)
	}
	if len(rec.Turns) != 17 {
		t.Errorf("persisted turns = %d, want initial user + 8 assistant/peer pairs", len(rec.Turns))
	}
}

func TestDelegationLoop_UsesEffectiveBudget(t *testing.T) {
	for _, override := range []int{0, 160} {
		t.Run(strconv.Itoa(override), func(t *testing.T) {
			del := &fake.Delegator{Results: map[string]port.DelegationResult{
				"engineer": {State: string(a2a.TaskStateCompleted), Output: "short peer reply"},
			}}
			h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
			h.sup.cfg.ContextBudget = override
			h.prov.ReturnCapabilities.ContextBudget = 100
			h.prov.ReturnRunResults = []port.ProviderResult{{Output: strings.Repeat("x", 200), ActionIntents: []port.ActionIntent{delegateIntent("engineer", "draft")}}}
			_, rec := h.run(t, "budget")
			if len(h.prov.RunCalls) != 2 {
				t.Fatalf("RunTask calls = %d, want 2", len(h.prov.RunCalls))
			}
			budget := override
			if budget == 0 {
				budget = 100
			}
			want := contextText(assembleBoundedContext(rec.Turns[:len(rec.Turns)-1], budget))
			if got := h.prov.RunCalls[1].Input; got != want || !strings.Contains(got, truncationMarker) {
				t.Errorf("bounded input = %q, want %q with truncation marker", got, want)
			}
			if len(rec.Turns[1].Content) != 200 {
				t.Error("budget must trim injection, not persisted history")
			}
		})
	}
}
