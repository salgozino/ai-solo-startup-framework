// Tests for supervisor delegation routing — the synchronous half.
// Spec: agent-delegation (openspec/changes/agent-delegation-over-a2a), design D6/D10.
// A Permit-classified delegate_task intent routes to port.Delegator, never to the
// gateway; the peer's terminal result decides the delegating task's fate; a
// non-terminal peer fails the task with an explicit not-yet-wired error.
package supervisor

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// delegationHarness bundles the collaborators of a delegating supervisor test.
type delegationHarness struct {
	sup  *Supervisor
	prov *fake.Provider
	gw   *fake.Gateway
	del  *fake.Delegator
	logs *bytes.Buffer
}

// delegateIntent builds a delegate_task intent with the {target, body} payload shape.
func delegateIntent(target, body string) port.ActionIntent {
	return port.ActionIntent{
		Kind:    port.KindDelegateTask,
		Payload: map[string]any{port.TargetArg: target, "body": body},
	}
}

// newDelegatingSupervisor builds a "ceo" supervisor whose provider emits the given
// intents, with delegate_task permitted (risk safe) for ceo only. The structured
// log is captured so tests can assert on the delegation error text, which the
// A2A FAILED event deliberately does not carry (errorMessage stays generic).
func newDelegatingSupervisor(
	t *testing.T,
	role string,
	intents []port.ActionIntent,
	del *fake.Delegator,
	timeout time.Duration,
) *delegationHarness {
	// A typed-nil *fake.Delegator must become a true nil interface, otherwise
	// Config.Delegator is non-nil and the fake panics on its nil receiver.
	var delegator port.Delegator
	if del != nil {
		delegator = del
	}
	t.Helper()
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{
		Output:        "ceo own text",
		ActionIntents: intents,
	}}
	gw := &fake.Gateway{}
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	logs := &bytes.Buffer{}
	policyCfg := map[string]config.Policy{
		port.KindDelegateTask: {Risk: "safe", AllowedRoles: []string{"ceo"}},
		"telegram_send":       {Risk: "low", AllowedRoles: []string{"ceo"}},
	}
	sup, err := New(Config{
		Addr:            mustTestAddr(t, role, "acme"),
		Provider:        prov,
		Store:           store,
		PolicyEngine:    policy.NewEngine(),
		Gateway:         gw,
		Delegator:       delegator,
		DelegateTimeout: timeout,
		Role:            role,
		PolicyConfig:    policyCfg,
		Logger:          slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sup.MarkReady()
	return &delegationHarness{sup: sup, prov: prov, gw: gw, del: del, logs: logs}
}

// run drives one new task through the supervisor and returns the terminal status
// event plus the persisted record.
func (h *delegationHarness) run(t *testing.T, taskID string) (*sdka2a.TaskStatusUpdateEvent, TaskRecord) {
	t.Helper()
	events := collectStatusEvents(context.Background(), h.sup, newExecCtx(taskID, "please delegate"))
	last := terminalEvent(t, events)
	rec, err := h.sup.cfg.Store.Load(h.sup.cfg.Addr, taskID)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	return last, rec
}

// assertFailedNaming asserts the task FAILED and the logged error mentions every needle.
func (h *delegationHarness) assertFailedNaming(t *testing.T, last *sdka2a.TaskStatusUpdateEvent, rec TaskRecord, needles ...string) {
	t.Helper()
	if last.Status.State != sdka2a.TaskStateFailed {
		t.Fatalf("expected FAILED, got %v", last.Status.State)
	}
	if rec.State != string(sdka2a.TaskStateFailed) {
		t.Errorf("persisted state = %q, want FAILED", rec.State)
	}
	logged := h.logs.String()
	for _, n := range needles {
		if !strings.Contains(logged, n) {
			t.Errorf("delegation error must name %q; logged:\n%s", n, logged)
		}
	}
}

// TestExecuteAction_DelegateTaskRoutesToPortNotGateway — task 6.2.
// Spec: agent-supervisor "A Permitted delegate_task intent calls the delegation
// port, not the gateway".
func TestExecuteAction_DelegateTaskRoutesToPortNotGateway(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "ok"},
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

	h.run(t, "task-route-1")

	if got := del.CallCount(); got != 1 {
		t.Fatalf("Delegator.Delegate calls = %d, want 1", got)
	}
	call := del.Calls[0]
	if call.Role != "engineer" || call.Body != "build X" {
		t.Errorf("Delegate called with role=%q body=%q; want engineer / build X", call.Role, call.Body)
	}
	if h.gw.CallCount() != 0 {
		t.Errorf("Gateway.Send must not be called for delegate_task; got %d calls", h.gw.CallCount())
	}
}

// TestExecuteAction_TelegramSendStillRoutesToGateway — task 6.2 (regression guard).
// Spec: agent-supervisor "Any other Permitted action kind still routes to the gateway".
func TestExecuteAction_TelegramSendStillRoutesToGateway(t *testing.T) {
	del := &fake.Delegator{}
	intent := port.ActionIntent{Kind: "telegram_send", Payload: map[string]any{"body": "hi"}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{intent}, del, 0)

	last, _ := h.run(t, "task-tg-1")

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	if h.gw.CallCount() != 1 {
		t.Errorf("Gateway.Send calls = %d, want 1", h.gw.CallCount())
	}
	if del.CallCount() != 0 {
		t.Errorf("Delegator must not be called for telegram_send; got %d calls", del.CallCount())
	}
}

// TestExecuteAction_DelegationCompletesWithPeerOutput — task 6.4.
// Spec: agent-delegation "A completed peer delegation completes the delegating
// task with the peer's output".
func TestExecuteAction_DelegationCompletesWithPeerOutput(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "Done: implemented X"},
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

	last, rec := h.run(t, "task-complete-1")

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	if !strings.Contains(rec.Output, "Done: implemented X") {
		t.Errorf("persisted Output = %q, want it to contain the peer output", rec.Output)
	}
	if !strings.Contains(messageText(last.Status.Message), "Done: implemented X") {
		t.Errorf("COMPLETED event message = %q, want the peer output on the wire", messageText(last.Status.Message))
	}
	if h.prov.RunTaskCallCount() != 1 {
		t.Errorf("delegating CLI must run exactly once; got %d", h.prov.RunTaskCallCount())
	}
}

// TestExecuteAction_DelegationFailedPeerFailsDelegatingTask — task 6.6.
// Spec: agent-delegation "A failed peer delegation fails the delegating task".
func TestExecuteAction_DelegationFailedPeerFailsDelegatingTask(t *testing.T) {
	cases := []struct {
		name string
		del  *fake.Delegator
	}{
		{
			name: "peer state FAILED",
			del: &fake.Delegator{Results: map[string]port.DelegationResult{
				"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateFailed)},
			}},
		},
		{
			name: "delegator error",
			del:  &fake.Delegator{Errors: map[string]error{"engineer": errors.New("peer exploded")}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, tc.del, 0)

			last, rec := h.run(t, "task-failed-1")

			h.assertFailedNaming(t, last, rec, "engineer")
			if rec.Output != "ceo own text" {
				t.Errorf("Output must not be fabricated from a failed peer; got %q", rec.Output)
			}
			if h.gw.CallCount() != 0 {
				t.Errorf("gateway must not be touched; got %d calls", h.gw.CallCount())
			}
		})
	}
}

// TestExecuteAction_DelegationTimeoutFailsTaskNamingPeerAndDuration — task 6.8.
// The timeout is enforced with context.WithTimeout around Delegate; the fake honors
// ctx cancellation, so a tiny DelegateTimeout drives the path without sleeping.
// Spec: agent-delegation "A hung peer fails the delegating task within the timeout".
func TestExecuteAction_DelegationTimeoutFailsTaskNamingPeerAndDuration(t *testing.T) {
	del := &fake.Delegator{BlockUntilCtxDone: true}
	const timeout = 20 * time.Millisecond
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, timeout)

	last, rec := h.run(t, "task-timeout-1")

	h.assertFailedNaming(t, last, rec, "engineer", timeout.String())
	if rec.State == string(sdka2a.TaskStateCompleted) {
		t.Error("timed-out delegation must never COMPLETE")
	}
}

// TestExecuteAction_DelegationUnknownRoleFailsTaskExplicitly — task 6.10.
// The transport's ErrUnknownRole cannot be imported here (core must not depend on
// transport), so the fake returns an equivalent named error; the supervisor only
// has to surface it, never panic.
// Spec: agent-delegation "An unknown target role fails the task with a clear error".
func TestExecuteAction_DelegationUnknownRoleFailsTaskExplicitly(t *testing.T) {
	del := &fake.Delegator{Errors: map[string]error{
		"designer": errors.New(`unknown role "designer"`),
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("designer", "design X")}, del, 0)

	last, rec := h.run(t, "task-unknown-1")

	h.assertFailedNaming(t, last, rec, "designer")
}

// TestExecuteAction_DelegationNonTerminalPeerFailsWithNotYetWiredError — task 6.12.
// Spec: agent-delegation "A Non-Terminal Peer Result Fails the Delegating Task With
// an Explicit Not-Yet-Wired Error" (all four scenarios).
func TestExecuteAction_DelegationNonTerminalPeerFailsWithNotYetWiredError(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P", State: string(sdka2a.TaskStateInputRequired)},
	}}
	// A generous timeout: the failure must be immediate, not timeout-driven.
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, time.Hour)

	start := time.Now()
	last, rec := h.run(t, "task-nonterminal-1")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("non-terminal peer must fail immediately, took %s", elapsed)
	}

	// slog's TextHandler escapes the quoted peer task ID inside the error attr.
	h.assertFailedNaming(t, last, rec, "escalated", "chained approval", "not yet wired", "engineer", `peer task \"P\"`)
	if rec.Output != "ceo own text" {
		t.Errorf("no output may be fabricated for a parked peer; got %q", rec.Output)
	}
	if del.StateCallCount() != 0 {
		t.Errorf("no PeerTaskState polling in this change; got %d calls", del.StateCallCount())
	}
}

// TestExecuteAction_DelegationWithoutDelegatorFailsExplicitly — a supervisor
// without a configured Delegator must fail a delegate_task explicitly, never
// nil-pointer-panic.
func TestExecuteAction_DelegationWithoutDelegatorFailsExplicitly(t *testing.T) {
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, nil, 0)

	last, rec := h.run(t, "task-nodelegator-1")

	h.assertFailedNaming(t, last, rec, "delegator")
}

// TestDelegateTask_FromNonAllowedRoleIsHardDenied — task 6.14.
// Spec: agent-delegation "A peer role attempting to delegate onward is hard-denied"
// and "No hop-counter field exists in the delegation payload".
func TestDelegateTask_FromNonAllowedRoleIsHardDenied(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"ceo": {PeerTaskID: "P9", State: string(sdka2a.TaskStateCompleted), Output: "loop!"},
	}}
	intent := delegateIntent("ceo", "delegate back")
	h := newDelegatingSupervisor(t, "engineer", []port.ActionIntent{intent}, del, 0)

	last, rec := h.run(t, "task-harddeny-1")

	if last.Status.State != sdka2a.TaskStateRejected {
		t.Fatalf("expected REJECTED (HardDeny), got %v", last.Status.State)
	}
	if rec.State != string(sdka2a.TaskStateRejected) {
		t.Errorf("persisted state = %q, want REJECTED", rec.State)
	}
	if del.CallCount() != 0 {
		t.Errorf("hard-denied delegation must never reach the port; got %d calls", del.CallCount())
	}

	// Payload shape: exactly {target, body} — no hop counter or depth field.
	if len(intent.Payload) != 2 {
		t.Errorf("payload keys = %v, want exactly {target, body}", intent.Payload)
	}
	for _, k := range []string{port.TargetArg, "body"} {
		if _, ok := intent.Payload[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
}

// TestResume_DelegateTaskWithoutPersistedTargetFailsExplicitly documents a known
// limitation of this change: an escalated delegate_task cannot be resumed because
// TaskRecord persists only PendingIntentKind/PendingIntentBody (task 8.3 forbids
// new fields here). The shipped policy classifies delegate_task as safe, so this
// path is unreachable in production; if an operator marks it risky, approval must
// fail loudly rather than delegate to an empty role.
func TestResume_DelegateTaskWithoutPersistedTargetFailsExplicitly(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "ok"},
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)
	// Override: make delegate_task risky so it escalates instead of executing.
	h.sup.cfg.PolicyConfig[port.KindDelegateTask] = config.Policy{Risk: "risky", AllowedRoles: []string{"ceo"}}

	ctx := context.Background()
	const taskID = "task-resume-delegate-1"
	parked := terminalEventOrLast(collectStatusEvents(ctx, h.sup, newExecCtx(taskID, "please delegate")))
	if parked.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("expected INPUT_REQUIRED, got %v", parked.Status.State)
	}

	events := collectStatusEvents(ctx, h.sup, newResumeExecCtx(taskID, "approve"))
	last := terminalEvent(t, events)
	rec, err := h.sup.cfg.Store.Load(h.sup.cfg.Addr, taskID)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}

	h.assertFailedNaming(t, last, rec, "target")
	if del.CallCount() != 0 {
		t.Errorf("must not delegate to an empty role; got %d Delegate calls", del.CallCount())
	}
}
