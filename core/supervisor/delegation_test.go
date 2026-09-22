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
	"strconv"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// agentOwnOutput is the delegating agent's own provider text. Tests assert against
// it to prove a peer's result never gets confused with what the local agent said.
const agentOwnOutput = "ceo own text"

// delegationHarness bundles the collaborators of a delegating supervisor test.
type delegationHarness struct {
	sup  *Supervisor
	prov *fake.Provider
	gw   *fake.Gateway
	del  *fake.Delegator
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
// log is routed to a discarded buffer purely to keep it out of the test output;
// no test asserts on it. Delegation error text is asserted on executeDelegation's
// returned error instead, since the A2A FAILED event deliberately carries only a
// generic errorMessage.
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
		Output:        agentOwnOutput,
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
	return &delegationHarness{sup: sup, prov: prov, gw: gw, del: del}
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

// assertFailed asserts the end-to-end guarantees a full run can actually observe:
// the terminal event is FAILED, the persisted record is FAILED, and no output was
// fabricated (the record still holds the delegating agent's own provider text).
//
// It deliberately does NOT match needles against the captured log buffer. That
// proved a failure error "names the peer role" only up to "this string appears
// somewhere in the whole run log", so a regression dropping the role from the error
// while still logging it elsewhere would have passed. Error-text precision belongs
// to TestExecuteDelegation_ErrorTextNamesTheFailure, which asserts on the returned
// error string directly.
func (h *delegationHarness) assertFailed(t *testing.T, last *sdka2a.TaskStatusUpdateEvent, rec TaskRecord) {
	t.Helper()
	if last.Status.State != sdka2a.TaskStateFailed {
		t.Fatalf("expected FAILED, got %v", last.Status.State)
	}
	if rec.State != string(sdka2a.TaskStateFailed) {
		t.Errorf("persisted state = %q, want FAILED", rec.State)
	}
	if rec.Output != agentOwnOutput {
		t.Errorf("a failed delegation must fabricate no output; got %q, want the agent's own text %q", rec.Output, agentOwnOutput)
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
	call, ok := del.LastCall()
	if !ok {
		t.Fatal("Delegator.LastCall reported no recorded call")
	}
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

	last, rec := h.run(t, "task-tg-1")

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	// Regression guard for the empty-delegation-output fix: a non-delegation action
	// returns a zero actionOutcome, and that must never wipe the agent's own output.
	if rec.Output != "ceo own text" {
		t.Errorf("a gateway action must leave the agent's own output intact; got %q", rec.Output)
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

// TestExecuteAction_CompletedPeerWithEmptyOutputReplacesAgentText — reliability fix.
// transport/a2a's resultFromTask documents that Output stays empty for a COMPLETED
// task that produced no output. Gating the copy on a non-empty Output therefore left
// the delegating agent's OWN provider text in the record, so the task reported
// COMPLETED while presenting local agent babble as the peer's result — the worst
// failure mode for a human supervising agents through the monitoring UI.
func TestExecuteAction_CompletedPeerWithEmptyOutputReplacesAgentText(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted)},
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

	last, rec := h.run(t, "task-empty-output-1")

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("expected COMPLETED, got %v", last.Status.State)
	}
	if rec.Output == "ceo own text" {
		t.Errorf("the delegating agent's own text must never be presented as the peer's result; got %q", rec.Output)
	}
	// Core stores the truth: the peer produced nothing. Rendering an absent result
	// is a UI concern, so no substitute display text is fabricated here.
	if rec.Output != "" {
		t.Errorf("an empty peer result must be stored verbatim, not fabricated; got %q", rec.Output)
	}
}

// TestExecuteAction_DelegationFailedPeerFailsDelegatingTask — task 6.6.
// Spec: agent-delegation "A failed peer delegation fails the delegating task".
// This end-to-end half proves persistence and that the gateway stays untouched;
// that the error names the peer role is proved by
// TestExecuteDelegation_ErrorTextNamesTheFailure.
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

			h.assertFailed(t, last, rec)
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
// The role-and-duration naming half is proved by
// TestExecuteDelegation_ErrorTextNamesTheFailure's timeout case.
func TestExecuteAction_DelegationTimeoutFailsTaskNamingPeerAndDuration(t *testing.T) {
	del := &fake.Delegator{BlockUntilCtxDone: true}
	const timeout = 20 * time.Millisecond
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, timeout)

	last, rec := h.run(t, "task-timeout-1")

	h.assertFailed(t, last, rec)
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

	h.assertFailed(t, last, rec)
}

// TestExecuteAction_DelegationNonTerminalPeerFailsWithNotYetWiredError — task 6.12.
// Spec: agent-delegation "A Non-Terminal Peer Result Fails the Delegating Task With
// an Explicit Not-Yet-Wired Error". This half proves the task fails immediately,
// persists FAILED, fabricates no output, and never polls PeerTaskState; the error
// wording is proved by TestExecuteDelegation_ErrorTextNamesTheFailure.
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

	h.assertFailed(t, last, rec)
	if del.StateCallCount() != 0 {
		t.Errorf("no PeerTaskState polling in this change; got %d calls", del.StateCallCount())
	}
}

// TestExecuteDelegation_UnrecognizedPeerStateIsProtocolViolation — reliability fix.
// port.Delegator documents that INPUT_REQUIRED is the ONLY legal non-terminal state
// on a nil-error return: "Any other non-terminal state is a protocol violation and
// MUST be returned as an error, never as a result". An unrecognized or empty state
// is therefore a defect in the Delegator implementation, not a peer escalation, and
// must never be reported with the escalation wording.
func TestExecuteDelegation_UnrecognizedPeerStateIsProtocolViolation(t *testing.T) {
	cases := []struct {
		name  string
		state string
	}{
		{name: "empty state", state: ""},
		{name: "unrecognized state", state: "TASK_STATE_BOGUS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			del := &fake.Delegator{Results: map[string]port.DelegationResult{
				"engineer": {PeerTaskID: "P1", State: tc.state},
			}}
			h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

			_, err := h.sup.executeDelegation(context.Background(), "engineer", "build X")
			if err == nil {
				t.Fatal("an unrecognized peer state must be an error, not a silent success")
			}
			msg := err.Error()

			// The point of this fix: a protocol violation is not an escalation.
			for _, forbidden := range []string{"escalated", "chained approval"} {
				if strings.Contains(msg, forbidden) {
					t.Errorf("protocol violation must not be reported as an escalation; error contains %q: %s", forbidden, msg)
				}
			}
			for _, needle := range []string{"engineer", strconv.Quote(tc.state), "port.Delegator"} {
				if !strings.Contains(msg, needle) {
					t.Errorf("protocol-violation error must name %q; got: %s", needle, msg)
				}
			}

			last, rec := h.run(t, "task-violation-1")
			if last.Status.State != sdka2a.TaskStateFailed {
				t.Fatalf("expected FAILED, got %v", last.Status.State)
			}
			if rec.State != string(sdka2a.TaskStateFailed) {
				t.Errorf("persisted state = %q, want FAILED", rec.State)
			}
		})
	}
}

// TestExecuteDelegation_CompletedPeerReturnsOutcome — reliability fix, direct call.
// The success half of the mapping, asserted on the returned outcome rather than
// through a full run: a COMPLETED peer yields no error, carries the peer's output,
// and marks the outcome Delegated so the caller replaces the agent's own text.
func TestExecuteDelegation_CompletedPeerReturnsOutcome(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "Done: implemented X"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)

	outcome, err := h.sup.executeDelegation(context.Background(), "engineer", "build X")
	if err != nil {
		t.Fatalf("executeDelegation: unexpected error: %v", err)
	}
	if outcome.Output != "Done: implemented X" {
		t.Errorf("outcome.Output = %q, want the peer's output", outcome.Output)
	}
	if !outcome.Delegated {
		t.Error("outcome.Delegated must be true so an empty peer result still replaces the agent's text")
	}
}

// TestExecuteDelegation_ErrorTextNamesTheFailure — reliability fix, direct call.
// The delegation error reaches only the structured log end-to-end (the A2A FAILED
// event deliberately keeps a generic errorMessage), so the end-to-end tests could
// only match needles against the whole captured log buffer: a regression dropping
// the role from the error while still logging it elsewhere would have passed.
// executeDelegation is a method in this package, so assert the returned error
// string directly — no log encoding, no buffer ambiguity.
func TestExecuteDelegation_ErrorTextNamesTheFailure(t *testing.T) {
	cases := []struct {
		name    string
		del     *fake.Delegator
		role    string
		timeout time.Duration
		want    []string
	}{
		{
			name: "terminal FAILED names the role and the peer task",
			del: &fake.Delegator{Results: map[string]port.DelegationResult{
				"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateFailed)},
			}},
			role: "engineer",
			want: []string{"engineer", `peer task "P1"`, string(sdka2a.TaskStateFailed)},
		},
		{
			name: "delegator error is surfaced with the role",
			del:  &fake.Delegator{Errors: map[string]error{"designer": errors.New(`unknown role "designer"`)}},
			role: "designer",
			want: []string{"designer", "unknown role"},
		},
		{
			// The peer task ID is asserted UNESCAPED here. The end-to-end version of
			// this needle had to spell it `peer task \"P\"` to match slog TextHandler
			// quoting, coupling a behavioral requirement to the harness's log handler.
			name: "INPUT_REQUIRED escalation names the role and the peer task",
			del: &fake.Delegator{Results: map[string]port.DelegationResult{
				"engineer": {PeerTaskID: "P", State: string(sdka2a.TaskStateInputRequired)},
			}},
			role: "engineer",
			want: []string{"escalated", "chained approval", "not yet wired", "engineer", `peer task "P"`},
		},
		{
			name: "no delegator configured",
			del:  nil,
			role: "engineer",
			want: []string{"delegator"},
		},
		{
			name: "empty target role",
			del:  &fake.Delegator{},
			role: "",
			want: []string{"target role"},
		},
		{
			name:    "timeout names the role and the configured duration",
			del:     &fake.Delegator{BlockUntilCtxDone: true},
			role:    "engineer",
			timeout: 20 * time.Millisecond,
			want:    []string{"engineer", "timed out", (20 * time.Millisecond).String()},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newDelegatingSupervisor(t, "ceo", nil, tc.del, tc.timeout)

			_, err := h.sup.executeDelegation(context.Background(), tc.role, "build X")
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			for _, needle := range tc.want {
				if !strings.Contains(err.Error(), needle) {
					t.Errorf("error must name %q; got: %s", needle, err)
				}
			}
		})
	}
}

// TestExecuteDelegation_DefaultTimeoutAppliesWhenUnset — reliability fix.
// A non-positive Config.DelegateTimeout maps to defaultDelegateTimeout before
// context.WithTimeout. Nothing observed that: every test passing timeout 0 used a
// fake that ignores the context on its non-blocking path, so deleting the fallback
// — leaving context.WithTimeout(ctx, 0), an already-expired deadline — kept them
// all green. The fallback becomes load-bearing once the real client is wired, so
// assert the deadline the Delegate call actually receives.
func TestExecuteDelegation_DefaultTimeoutAppliesWhenUnset(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "ok"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)

	if _, err := h.sup.executeDelegation(context.Background(), "engineer", "build X"); err != nil {
		t.Fatalf("executeDelegation: unexpected error: %v", err)
	}

	deadline, ok := del.ObservedDeadline()
	if !ok {
		t.Fatal("Delegate must receive a context carrying the delegation deadline")
	}
	// A generous window: wide enough not to flake on a slow runner, tight enough
	// that an already-expired or wildly different deadline fails.
	if remaining := time.Until(deadline); remaining <= 9*time.Minute || remaining > defaultDelegateTimeout {
		t.Errorf("remaining time on the default deadline = %s, want (9m, %s]", remaining, defaultDelegateTimeout)
	}
}

// TestExecuteDelegation_ConfiguredTimeoutBoundsTheCall — reliability fix.
// The counterpart to the default: an explicit DelegateTimeout must be the deadline
// handed to Delegate, not silently replaced by the fallback.
func TestExecuteDelegation_ConfiguredTimeoutBoundsTheCall(t *testing.T) {
	const timeout = 2 * time.Second
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": {PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: "ok"},
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, timeout)

	if _, err := h.sup.executeDelegation(context.Background(), "engineer", "build X"); err != nil {
		t.Fatalf("executeDelegation: unexpected error: %v", err)
	}

	deadline, ok := del.ObservedDeadline()
	if !ok {
		t.Fatal("Delegate must receive a context carrying the delegation deadline")
	}
	if remaining := time.Until(deadline); remaining <= timeout/2 || remaining > timeout {
		t.Errorf("remaining time on the configured deadline = %s, want (%s, %s]", remaining, timeout/2, timeout)
	}
}

// TestExecuteDelegation_ParentCancellationIsNotReportedAsTimeout — reliability fix.
// executeDelegation only blames the delegation timeout when the PARENT context is
// still live (errors.Is(err, DeadlineExceeded) && ctx.Err() == nil). That guard was
// unexercised. A parent deadline far shorter than DelegateTimeout must not produce
// "timed out after 1h0m0s", because the delegation timeout was never the cause.
func TestExecuteDelegation_ParentCancellationIsNotReportedAsTimeout(t *testing.T) {
	const delegateTimeout = time.Hour
	del := &fake.Delegator{BlockUntilCtxDone: true}
	h := newDelegatingSupervisor(t, "ceo", nil, del, delegateTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := h.sup.executeDelegation(ctx, "engineer", "build X")
	if err == nil {
		t.Fatal("a canceled parent context must fail the delegation")
	}
	msg := err.Error()
	if strings.Contains(msg, delegateTimeout.String()) || strings.Contains(msg, "timed out") {
		t.Errorf("parent cancellation must not be blamed on the delegation timeout; got: %s", msg)
	}
	// The generic failure form instead: role named, underlying cause wrapped.
	if !strings.Contains(msg, "engineer") || !strings.Contains(msg, "failed") {
		t.Errorf("error must name the role and report a failure; got: %s", msg)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the parent's cause must stay wrapped; got: %s", msg)
	}
}

// TestExecuteAction_DelegationWithoutDelegatorFailsExplicitly — a supervisor
// without a configured Delegator must fail a delegate_task explicitly, never
// nil-pointer-panic.
func TestExecuteAction_DelegationWithoutDelegatorFailsExplicitly(t *testing.T) {
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, nil, 0)

	last, rec := h.run(t, "task-nodelegator-1")

	h.assertFailed(t, last, rec)
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

	h.assertFailed(t, last, rec)
	if del.CallCount() != 0 {
		t.Errorf("must not delegate to an empty role; got %d Delegate calls", del.CallCount())
	}
}
