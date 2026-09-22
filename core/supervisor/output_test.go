// Tests for the terminal COMPLETED event carrying the task output on the wire.
// Design D9 (agent-delegation-over-a2a): the supervisor's terminal COMPLETED
// status event MUST carry rec.Output as a text part in Status.Message so a
// delegating peer can read the result back via GetTask. FAILED keeps the
// deliberately generic errorMessage(err) and is not touched by this change.
package supervisor

import (
	"context"
	"errors"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// collectStatusEvents drives Execute and returns every yielded status update event.
func collectStatusEvents(
	ctx context.Context,
	sup *Supervisor,
	execCtx *a2asrv.ExecutorContext,
) []*sdka2a.TaskStatusUpdateEvent {
	var events []*sdka2a.TaskStatusUpdateEvent
	for event, err := range sup.Execute(ctx, execCtx) {
		if err != nil {
			break
		}
		if e, ok := event.(*sdka2a.TaskStatusUpdateEvent); ok {
			events = append(events, e)
		}
	}
	return events
}

// terminalEvent returns the last status event, which must be terminal.
func terminalEvent(t *testing.T, events []*sdka2a.TaskStatusUpdateEvent) *sdka2a.TaskStatusUpdateEvent {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("expected at least one status event")
	}
	last := events[len(events)-1]
	if !last.Status.State.Terminal() {
		t.Fatalf("last event must be terminal, got %v", last.Status.State)
	}
	return last
}

// TestExecuteWithPolicy_PermitCompletionCarriesOutput verifies that the
// terminal COMPLETED event on the non-delegated Permit path carries the
// provider's output as a text part instead of a nil message.
// Satisfies: task 5.1, design D9.
func TestExecuteWithPolicy_PermitCompletionCarriesOutput(t *testing.T) {
	const wantOutput = "Done: sent the telegram"
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			Output: wantOutput,
			ActionIntents: []port.ActionIntent{{
				Kind:    "telegram_send",
				Payload: map[string]any{"body": "hello"},
			}},
		},
	}
	gw := &fake.Gateway{}
	policyCfg := makePolicyCfg("telegram_send", "low", []string{"ceo"}) // low → Permit

	sup := newTestSupervisor(t, "ceo", prov, gw, policy.NewEngine(), policyCfg)
	sup.MarkReady()

	events := collectStatusEvents(context.Background(), sup, newExecCtx("task-out-permit", "send it"))
	last := terminalEvent(t, events)

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	if last.Status.Message == nil {
		t.Fatal("COMPLETED event must carry a status message with the task output; got nil")
	}
	if got := messageText(last.Status.Message); got != wantOutput {
		t.Errorf("COMPLETED message text: got %q, want %q", got, wantOutput)
	}
	if last.Status.Message.Role != sdka2a.MessageRoleAgent {
		t.Errorf("COMPLETED message role: got %v, want agent", last.Status.Message.Role)
	}
	if gw.CallCount() != 1 {
		t.Errorf("Permit path regression: expected 1 gateway call, got %d", gw.CallCount())
	}
}

// TestExecuteWithPolicy_NoIntentsCompletionCarriesOutput covers the plain
// "provider answered, no action intents" path — the most common completion.
// Satisfies: task 5.1, design D9.
func TestExecuteWithPolicy_NoIntentsCompletionCarriesOutput(t *testing.T) {
	const wantOutput = "Here is my answer"
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: wantOutput}}

	sup := newTestSupervisor(t, "ceo", prov, &fake.Gateway{}, policy.NewEngine(), nil)
	sup.MarkReady()

	events := collectStatusEvents(context.Background(), sup, newExecCtx("task-out-plain", "hola"))
	last := terminalEvent(t, events)

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	if got := messageText(last.Status.Message); got != wantOutput {
		t.Errorf("COMPLETED message text: got %q, want %q", got, wantOutput)
	}
}

// TestResume_CompletionCarriesPersistedOutput verifies that when a task
// escalates, is approved, and completes via the resume path, the terminal
// COMPLETED event carries the output persisted in the store at escalation time.
// Satisfies: task 5.1, design D9 ("every completed task, delegated or not").
func TestResume_CompletionCarriesPersistedOutput(t *testing.T) {
	const wantOutput = "Drafted the announcement"
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			Output: wantOutput,
			ActionIntents: []port.ActionIntent{{
				Kind:    "telegram_send",
				Payload: map[string]any{"body": "announcement"},
			}},
		},
	}
	gw := &fake.Gateway{}
	policyCfg := makePolicyCfg("telegram_send", "risky", []string{"ceo"}) // risky → Escalate

	sup := newTestSupervisor(t, "ceo", prov, gw, policy.NewEngine(), policyCfg)
	sup.MarkReady()

	ctx := context.Background()
	const taskID = "task-out-resume"

	parked := terminalEventOrLast(collectStatusEvents(ctx, sup, newExecCtx(taskID, "announce")))
	if parked.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("expected INPUT_REQUIRED after escalation, got %v", parked.Status.State)
	}

	events := collectStatusEvents(ctx, sup, newResumeExecCtx(taskID, "approve"))
	last := terminalEvent(t, events)

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED after approval, got %v", last.Status.State)
	}
	if got := messageText(last.Status.Message); got != wantOutput {
		t.Errorf("resume COMPLETED message text: got %q, want %q", got, wantOutput)
	}
}

// TestExecuteWithPolicy_EmptyOutputCompletesWithNilMessage documents the
// additive nature of the change: with no output there is nothing to carry, so
// the COMPLETED event keeps a nil message rather than an empty text part.
// Satisfies: task 5.3 (regression: non-delegated completion still transitions).
func TestExecuteWithPolicy_EmptyOutputCompletesWithNilMessage(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{}}

	sup := newTestSupervisor(t, "ceo", prov, &fake.Gateway{}, policy.NewEngine(), nil)
	sup.MarkReady()

	events := collectStatusEvents(context.Background(), sup, newExecCtx("task-out-empty", "hola"))
	last := terminalEvent(t, events)

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	if last.Status.Message != nil {
		t.Errorf("empty output must not fabricate a message; got %+v", last.Status.Message)
	}
}

// TestExecuteWithPolicy_FailedKeepsGenericMessage pins the FAILED path: it must
// keep the deliberately generic error text and never leak the provider error
// or any partial output. Design D9 explicitly leaves this path untouched.
// Satisfies: task 5.3.
func TestExecuteWithPolicy_FailedKeepsGenericMessage(t *testing.T) {
	prov := &fake.Provider{ReturnRunErr: errors.New("secret /internal/path exploded")}

	sup := newTestSupervisor(t, "ceo", prov, &fake.Gateway{}, policy.NewEngine(), nil)
	sup.MarkReady()

	events := collectStatusEvents(context.Background(), sup, newExecCtx("task-out-failed", "hola"))
	last := terminalEvent(t, events)

	if last.Status.State != sdka2a.TaskStateFailed {
		t.Fatalf("expected FAILED, got %v", last.Status.State)
	}
	if got := messageText(last.Status.Message); got != "task execution failed" {
		t.Errorf("FAILED message must stay generic: got %q", got)
	}
}

// terminalEventOrLast returns the last event without asserting terminality
// (INPUT_REQUIRED is not terminal but is the expected parking state).
func terminalEventOrLast(events []*sdka2a.TaskStatusUpdateEvent) *sdka2a.TaskStatusUpdateEvent {
	if len(events) == 0 {
		return &sdka2a.TaskStatusUpdateEvent{}
	}
	return events[len(events)-1]
}
