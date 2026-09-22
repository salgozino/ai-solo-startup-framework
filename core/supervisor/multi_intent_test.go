// Tests for provider turns carrying more than one action intent.
// Feature: odd/tasks/preserve-intents-after-escalate.md.
//
// A single provider turn may emit several action intents. The supervisor must
// classify and execute every one of them: an escalation parks the task on the
// risky intent, and the intents that follow it must survive the park and run on
// resume, in the order the agent emitted them. No intent may be dropped without
// executing, being rejected, or producing an explicit terminal failure.
package supervisor

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// multiIntentHarness bundles the collaborators of a multi-intent supervisor test.
type multiIntentHarness struct {
	sup *Supervisor
	gw  *fake.Gateway
	del *fake.Delegator
}

// newMultiIntentSupervisor builds a "ceo" supervisor whose provider emits intents
// in the given order. The risk policy is supplied per test so one turn can mix a
// permitted and a risky action kind. The delegator always resolves role
// "engineer" to a COMPLETED peer task.
func newMultiIntentSupervisor(
	t *testing.T,
	intents []port.ActionIntent,
	policyCfg map[string]config.Policy,
) *multiIntentHarness {
	t.Helper()
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{
		Output:        agentOwnOutput,
		ActionIntents: intents,
	}}
	gw := &fake.Gateway{}
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "peer done"},
	}}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sup, err := New(Config{
		Addr:         mustTestAddr(t, "ceo", "acme"),
		Provider:     prov,
		Store:        store,
		PolicyEngine: policy.NewEngine(),
		Gateway:      gw,
		Delegator:    del,
		Role:         "ceo",
		PolicyConfig: policyCfg,
		Logger:       slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sup.MarkReady()
	return &multiIntentHarness{sup: sup, gw: gw, del: del}
}

// record returns the persisted record for taskID.
func (h *multiIntentHarness) record(t *testing.T, taskID string) TaskRecord {
	t.Helper()
	rec, err := h.sup.cfg.Store.Load(h.sup.cfg.Addr, taskID)
	if err != nil {
		t.Fatalf("store.Load(%q): %v", taskID, err)
	}
	return rec
}

// lastState returns the state of the final status event, without requiring it to
// be terminal: an escalation parks in INPUT_REQUIRED, which is not terminal.
func lastState(t *testing.T, events []*sdka2a.TaskStatusUpdateEvent) sdka2a.TaskState {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("expected at least one status event")
	}
	return events[len(events)-1].Status.State
}

// riskyTelegramSafeDelegate is the policy used by the ordering tests: a
// telegram_send needs a human verdict, a delegate_task does not.
func riskyTelegramSafeDelegate() map[string]config.Policy {
	return map[string]config.Policy{
		"telegram_send":       {Risk: "risky", AllowedRoles: []string{"ceo"}},
		port.KindDelegateTask: {Risk: "safe", AllowedRoles: []string{"ceo"}},
	}
}

// telegramIntent builds a telegram_send intent carrying body.
func telegramIntent(body string) port.ActionIntent {
	return port.ActionIntent{Kind: "telegram_send", Payload: map[string]any{"body": body}}
}

// TestMultiIntent_DelegateThenEscalate pins the manually validated path: the
// agent delegates first and notifies second. The delegation runs immediately,
// the telegram parks for approval, and the approval delivers it. This test must
// pass both before and after the preserve-intents change (feature task T1).
func TestMultiIntent_DelegateThenEscalate(t *testing.T) {
	const taskID = "task-multi-delegate-first"
	h := newMultiIntentSupervisor(t, []port.ActionIntent{
		delegateIntent("engineer", "build X"),
		telegramIntent("shipped X"),
	}, riskyTelegramSafeDelegate())

	ctx := context.Background()
	events := collectStatusEvents(ctx, h.sup, newExecCtx(taskID, "delegate then notify"))

	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("first turn must park in INPUT_REQUIRED, got %v", got)
	}
	if got := h.del.CallCount(); got != 1 {
		t.Fatalf("the delegation precedes the escalation and must run immediately; Delegate calls = %d, want 1", got)
	}
	if got := h.gw.CallCount(); got != 0 {
		t.Errorf("gateway must not be called before approval; got %d calls", got)
	}
	rec := h.record(t, taskID)
	if rec.PendingIntentKind != "telegram_send" {
		t.Errorf("PendingIntentKind = %q, want telegram_send", rec.PendingIntentKind)
	}

	// Approve the parked telegram.
	resumeEvents := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "approve"))

	if got := lastState(t, resumeEvents); got != sdka2a.TaskStateCompleted {
		t.Fatalf("approval must complete the task, got %v", got)
	}
	if got := h.gw.CallCount(); got != 1 {
		t.Fatalf("gateway.Send calls after approval = %d, want 1", got)
	}
	call, ok := h.gw.LastCall()
	if !ok {
		t.Fatal("expected a recorded gateway call")
	}
	if call.Body != "shipped X" {
		t.Errorf("gateway body = %q, want %q", call.Body, "shipped X")
	}
	if got := h.del.CallCount(); got != 1 {
		t.Errorf("the delegation must not run twice; Delegate calls = %d, want 1", got)
	}
}

// TestMultiIntent_EscalateThenDelegateIsNotDropped is the regression the feature
// exists for: the risky intent arrives first, so the task parks before the
// delegation is ever classified. The delegation must survive the park and run on
// approval instead of being silently discarded (feature task T2).
func TestMultiIntent_EscalateThenDelegateIsNotDropped(t *testing.T) {
	const taskID = "task-multi-escalate-first"
	h := newMultiIntentSupervisor(t, []port.ActionIntent{
		telegramIntent("heads up"),
		delegateIntent("engineer", "build X"),
	}, riskyTelegramSafeDelegate())

	ctx := context.Background()
	events := collectStatusEvents(ctx, h.sup, newExecCtx(taskID, "notify then delegate"))

	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("first turn must park in INPUT_REQUIRED, got %v", got)
	}
	if got := h.gw.CallCount(); got != 0 {
		t.Errorf("gateway must not be called before approval; got %d calls", got)
	}
	if got := h.del.CallCount(); got != 0 {
		t.Errorf("the delegation follows the escalation and must wait; Delegate calls = %d, want 0", got)
	}

	// Approve the parked telegram: the send happens, then the delegation runs.
	resumeEvents := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "approve"))

	if got := lastState(t, resumeEvents); got != sdka2a.TaskStateCompleted {
		t.Fatalf("approval must complete the task, got %v", got)
	}
	if got := h.gw.CallCount(); got != 1 {
		t.Errorf("gateway.Send calls after approval = %d, want 1", got)
	}
	if got := h.del.CallCount(); got != 1 {
		t.Fatalf("the intent that followed the escalation must not be dropped; Delegate calls = %d, want 1", got)
	}
	call, ok := h.del.LastCall()
	if !ok {
		t.Fatal("expected a recorded Delegate call")
	}
	if call.Role != "engineer" || call.Body != "build X" {
		t.Errorf("Delegate called with role=%q body=%q; want engineer / build X", call.Role, call.Body)
	}

	rec := h.record(t, taskID)
	if rec.State != string(sdka2a.TaskStateCompleted) {
		t.Errorf("persisted state = %q, want COMPLETED", rec.State)
	}
	if len(rec.RemainingIntents) != 0 {
		t.Errorf("a completed task must hold no remaining intents; got %v", rec.RemainingIntents)
	}
}

// TestMultiIntent_ResumedDelegationKeepsItsTarget proves the target role of an
// escalated delegate_task survives the persisted round trip. Before this change
// the resume path passed an empty target, so an approved delegation failed with
// "delegate_task intent has no target role" (feature task T5).
func TestMultiIntent_ResumedDelegationKeepsItsTarget(t *testing.T) {
	const taskID = "task-resumed-delegation-target"
	h := newMultiIntentSupervisor(t, []port.ActionIntent{
		delegateIntent("engineer", "build X"),
	}, map[string]config.Policy{
		port.KindDelegateTask: {Risk: "risky", AllowedRoles: []string{"ceo"}},
	})

	ctx := context.Background()
	events := collectStatusEvents(ctx, h.sup, newExecCtx(taskID, "delegate with approval"))

	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("a risky delegate_task must park in INPUT_REQUIRED, got %v", got)
	}
	if got := h.record(t, taskID).PendingIntentTarget; got != "engineer" {
		t.Fatalf("PendingIntentTarget = %q, want engineer", got)
	}

	resumeEvents := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "approve"))

	if got := lastState(t, resumeEvents); got != sdka2a.TaskStateCompleted {
		t.Fatalf("an approved delegation must complete, got %v", got)
	}
	call, ok := h.del.LastCall()
	if !ok {
		t.Fatal("expected a recorded Delegate call")
	}
	if call.Role != "engineer" || call.Body != "build X" {
		t.Errorf("Delegate called with role=%q body=%q; want engineer / build X", call.Role, call.Body)
	}
	if got := h.record(t, taskID).Output; got != "peer done" {
		t.Errorf("the peer's output must become the task output; got %q", got)
	}
}

// TestMultiIntent_SecondRiskyIntentReParks proves the resumed loop re-parks
// instead of executing a second risky intent on the first human verdict: each
// risky action gets its own approval (feature task T4).
func TestMultiIntent_SecondRiskyIntentReParks(t *testing.T) {
	const taskID = "task-multi-two-escalations"
	h := newMultiIntentSupervisor(t, []port.ActionIntent{
		telegramIntent("heads up"),
		delegateIntent("engineer", "build X"),
	}, map[string]config.Policy{
		"telegram_send":       {Risk: "risky", AllowedRoles: []string{"ceo"}},
		port.KindDelegateTask: {Risk: "risky", AllowedRoles: []string{"ceo"}},
	})

	ctx := context.Background()
	_ = collectStatusEvents(ctx, h.sup, newExecCtx(taskID, "two risky actions"))

	// First approval: the telegram is sent, the delegation parks for its own verdict.
	firstResume := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "approve"))

	if got := lastState(t, firstResume); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("a second risky intent must re-park the task, got %v", got)
	}
	if got := h.gw.CallCount(); got != 1 {
		t.Errorf("gateway.Send calls = %d, want 1", got)
	}
	if got := h.del.CallCount(); got != 0 {
		t.Fatalf("the second risky intent must not run on the first verdict; Delegate calls = %d, want 0", got)
	}
	rec := h.record(t, taskID)
	if rec.PendingIntentKind != port.KindDelegateTask {
		t.Errorf("PendingIntentKind = %q, want %q", rec.PendingIntentKind, port.KindDelegateTask)
	}
	if rec.PendingIntentTarget != "engineer" {
		t.Errorf("PendingIntentTarget = %q, want engineer", rec.PendingIntentTarget)
	}

	// Second approval: the delegation runs and the task completes.
	secondResume := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "approve"))

	if got := lastState(t, secondResume); got != sdka2a.TaskStateCompleted {
		t.Fatalf("the second approval must complete the task, got %v", got)
	}
	if got := h.del.CallCount(); got != 1 {
		t.Errorf("Delegate calls after the second approval = %d, want 1", got)
	}
	if got := h.gw.CallCount(); got != 1 {
		t.Errorf("the already-sent telegram must not be re-sent; gateway calls = %d, want 1", got)
	}
}

// TestMultiIntent_RejectionDiscardsRemainingIntents proves a human "reject"
// stops the whole turn: the rejected intent is not executed and neither are the
// intents that followed it.
func TestMultiIntent_RejectionDiscardsRemainingIntents(t *testing.T) {
	const taskID = "task-multi-reject"
	h := newMultiIntentSupervisor(t, []port.ActionIntent{
		telegramIntent("heads up"),
		delegateIntent("engineer", "build X"),
	}, riskyTelegramSafeDelegate())

	ctx := context.Background()
	_ = collectStatusEvents(ctx, h.sup, newExecCtx(taskID, "notify then delegate"))

	resumeEvents := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "reject"))

	if got := lastState(t, resumeEvents); got != sdka2a.TaskStateRejected {
		t.Fatalf("a rejected escalation must end REJECTED, got %v", got)
	}
	if got := h.gw.CallCount(); got != 0 {
		t.Errorf("gateway must not be called after rejection; got %d calls", got)
	}
	if got := h.del.CallCount(); got != 0 {
		t.Errorf("intents after a rejected one must not run; Delegate calls = %d, want 0", got)
	}
}
