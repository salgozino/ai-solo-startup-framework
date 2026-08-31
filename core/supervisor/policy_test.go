// Tests for policy engine wiring into the supervisor.
// Tasks 4.6: full escalation cycle SUBMITTED→WORKING→INPUT_REQUIRED→WORKING→COMPLETED
// Task 4.7: resume recognized as resume, not new task.
package supervisor

import (
	"context"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// makePolicyCfg is a convenience helper for risk_policy maps in tests.
func makePolicyCfg(kind, risk string, allowedRoles []string) map[string]config.Policy {
	return map[string]config.Policy{
		kind: {Risk: risk, AllowedRoles: allowedRoles},
	}
}

// collectEvents drives a supervisor Execute iter and returns all yielded task states.
func collectEvents(
	ctx context.Context,
	sup *Supervisor,
	execCtx *a2asrv.ExecutorContext,
) []sdka2a.TaskState {
	var states []sdka2a.TaskState
	for event, err := range sup.Execute(ctx, execCtx) {
		if err != nil {
			break
		}
		switch e := event.(type) {
		case *sdka2a.TaskStatusUpdateEvent:
			states = append(states, e.Status.State)
		case *sdka2a.Task:
			states = append(states, e.Status.State)
		}
	}
	return states
}

// newTestSupervisor builds a supervisor with policy wiring for unit tests.
func newTestSupervisor(
	t *testing.T,
	role string,
	prov port.Provider,
	gw port.Gateway,
	eng *policy.Engine,
	policyCfg map[string]config.Policy,
) *Supervisor {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	addr := mustTestAddr(t, role, "acme")
	return New(Config{
		Addr:         addr,
		Provider:     prov,
		Store:        store,
		PolicyEngine: eng,
		Gateway:      gw,
		Role:         role,
		PolicyConfig: policyCfg,
	})
}

func mustTestAddr(t *testing.T, name, tenant string) address.A2AAddress {
	t.Helper()
	addr, err := address.New(name, tenant)
	if err != nil {
		t.Fatalf("address.New(%q,%q): %v", name, tenant, err)
	}
	return addr
}

// newExecCtx builds a minimal ExecutorContext for a new task.
func newExecCtx(taskID, text string) *a2asrv.ExecutorContext {
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart(text))
	return &a2asrv.ExecutorContext{
		TaskID:     sdka2a.TaskID(taskID),
		Message:    msg,
		StoredTask: nil, // new task
	}
}

// newResumeExecCtx builds a minimal ExecutorContext for a resume (approval).
func newResumeExecCtx(taskID, approvalText string) *a2asrv.ExecutorContext {
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart(approvalText))
	msg.TaskID = sdka2a.TaskID(taskID)
	// StoredTask != nil signals a resume to the supervisor.
	storedTask := &sdka2a.Task{
		ID: sdka2a.TaskID(taskID),
		Status: sdka2a.TaskStatus{
			State: sdka2a.TaskStateInputRequired,
		},
	}
	return &a2asrv.ExecutorContext{
		TaskID:     sdka2a.TaskID(taskID),
		Message:    msg,
		StoredTask: storedTask,
	}
}

// TestSupervisor_HardDeny_ProducesRejected verifies that a disallowed role
// results in REJECTED (not FAILED), zero gateway calls, and no escalation.
// Satisfies: task 4.6, threat-matrix gateway-effect case (e).
func TestSupervisor_HardDeny_ProducesRejected(t *testing.T) {
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
		},
	}
	gw := &fake.Gateway{}
	eng := policy.NewEngine()
	policyCfg := makePolicyCfg("telegram_send", "risky", []string{"ceo"}) // engineer not in allowed_roles

	sup := newTestSupervisor(t, "engineer", prov, gw, eng, policyCfg)
	sup.MarkReady()

	ctx := context.Background()
	execCtx := newExecCtx("task-hd-1", "do something")

	states := collectEvents(ctx, sup, execCtx)

	// Must contain REJECTED.
	hasRejected := false
	for _, s := range states {
		if s == sdka2a.TaskStateRejected {
			hasRejected = true
		}
		// Must NEVER be FAILED (hard-deny is REJECTED, not FAILED).
		if s == sdka2a.TaskStateFailed {
			t.Error("hard-deny must produce REJECTED, not FAILED")
		}
	}
	if !hasRejected {
		t.Errorf("expected REJECTED state, got: %v", states)
	}

	// Gateway must never be called on hard-deny.
	if gw.CallCount() != 0 {
		t.Errorf("gateway.Send must not be called on hard-deny; got %d calls", gw.CallCount())
	}
}

// TestSupervisor_EscalationCycle verifies the full state sequence:
// SUBMITTED → WORKING → INPUT_REQUIRED → (resume with approve) → WORKING → COMPLETED
// Satisfies: task 4.6, approval-flow spec "Full escalation cycle traversal".
func TestSupervisor_EscalationCycle(t *testing.T) {
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
		},
	}
	gw := &fake.Gateway{}
	eng := policy.NewEngine()
	policyCfg := makePolicyCfg("telegram_send", "risky", []string{"ceo"}) // ceo IS allowed, risky → escalate

	sup := newTestSupervisor(t, "ceo", prov, gw, eng, policyCfg)
	sup.MarkReady()

	ctx := context.Background()
	const taskID = "task-esc-1"

	// Step 1: send initial task → should reach INPUT_REQUIRED.
	execCtx := newExecCtx(taskID, "send a telegram")
	states := collectEvents(ctx, sup, execCtx)

	hasWorking := false
	hasInputRequired := false
	for _, s := range states {
		if s == sdka2a.TaskStateWorking {
			hasWorking = true
		}
		if s == sdka2a.TaskStateInputRequired {
			hasInputRequired = true
		}
	}
	if !hasWorking {
		t.Errorf("expected WORKING state in escalation path, got: %v", states)
	}
	if !hasInputRequired {
		t.Errorf("expected INPUT_REQUIRED state in escalation path, got: %v", states)
	}

	// Gateway must NOT be called before approval.
	if gw.CallCount() != 0 {
		t.Errorf("gateway must not be called before approval; got %d calls", gw.CallCount())
	}

	// Step 2: deliver approval as a new SendMessage with the same TaskID.
	resumeCtx := newResumeExecCtx(taskID, "approve")
	resumeStates := collectEvents(ctx, sup, resumeCtx)

	hasWorkingAfterResume := false
	hasCompleted := false
	for _, s := range resumeStates {
		if s == sdka2a.TaskStateWorking {
			hasWorkingAfterResume = true
		}
		if s == sdka2a.TaskStateCompleted {
			hasCompleted = true
		}
	}
	if !hasWorkingAfterResume {
		t.Errorf("expected WORKING after approval resume, got: %v", resumeStates)
	}
	if !hasCompleted {
		t.Errorf("expected COMPLETED after approval, got: %v", resumeStates)
	}

	// Gateway must be called exactly once after approval.
	if gw.CallCount() != 1 {
		t.Errorf("gateway.Send must be called exactly once after approval; got %d calls", gw.CallCount())
	}
}

// TestSupervisor_RestartPreservesInputRequired verifies that a task parked in
// INPUT_REQUIRED survives a supervisor restart (the record is in the store).
// Satisfies: task 4.6, approval-flow spec "Escalated task still resumable after restart".
func TestSupervisor_RestartPreservesInputRequired(t *testing.T) {
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
		},
	}
	gw := &fake.Gateway{}
	eng := policy.NewEngine()
	policyCfg := makePolicyCfg("telegram_send", "risky", []string{"ceo"})

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	ceoAddr := mustTestAddr(t, "ceo", "acme")
	sup := New(Config{
		Addr:         ceoAddr,
		Provider:     prov,
		Store:        store,
		PolicyEngine: eng,
		Gateway:      gw,
		Role:         "ceo",
		PolicyConfig: policyCfg,
	})
	sup.MarkReady()

	ctx := context.Background()
	const taskID = "task-restart-1"

	// Park the task in INPUT_REQUIRED.
	execCtx := newExecCtx(taskID, "send a telegram")
	_ = collectEvents(ctx, sup, execCtx)

	// Verify the store has the task in INPUT_REQUIRED state.
	rec, err := store.Load(ceoAddr, taskID)
	if err != nil {
		t.Fatalf("store.Load after escalation: %v", err)
	}
	if rec.State != string(sdka2a.TaskStateInputRequired) {
		t.Errorf("expected INPUT_REQUIRED in store, got %q", rec.State)
	}
	if rec.PendingIntentKind != "telegram_send" {
		t.Errorf("expected PendingIntentKind=telegram_send, got %q", rec.PendingIntentKind)
	}

	// Simulate restart: create a new supervisor with the SAME store.
	sup2 := New(Config{
		Addr:         ceoAddr,
		Provider:     prov,
		Store:        store,
		PolicyEngine: eng,
		Gateway:      gw,
		Role:         "ceo",
		PolicyConfig: policyCfg,
	})
	sup2.MarkReady()

	// Verify store still has the task (not lost on restart).
	rec2, err := store.Load(ceoAddr, taskID)
	if err != nil {
		t.Fatalf("store.Load after restart: %v", err)
	}
	if rec2.State != string(sdka2a.TaskStateInputRequired) {
		t.Errorf("task must remain INPUT_REQUIRED after restart, got %q", rec2.State)
	}
}

// TestSupervisor_ResumeRecognized verifies that a new SendMessage carrying an
// existing TaskID is recognized as a resume, not a new task.
// Satisfies: task 4.7, approval-flow spec "An approval resumes the correct parked task".
func TestSupervisor_ResumeRecognized(t *testing.T) {
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
		},
	}
	gw := &fake.Gateway{}
	eng := policy.NewEngine()
	policyCfg := makePolicyCfg("telegram_send", "risky", []string{"ceo"})

	sup := newTestSupervisor(t, "ceo", prov, gw, eng, policyCfg)
	sup.MarkReady()

	ctx := context.Background()
	const taskID = "task-resume-1"

	// Park the task.
	execCtx := newExecCtx(taskID, "send a telegram")
	_ = collectEvents(ctx, sup, execCtx)

	// Track RunTask call count before resume.
	runCountBefore := prov.RunTaskCallCount()

	// Deliver approval as new SendMessage with the same TaskID.
	// StoredTask != nil tells the supervisor this is a resume.
	resumeCtx := newResumeExecCtx(taskID, "approve")
	resumeStates := collectEvents(ctx, sup, resumeCtx)

	// Resume must NOT call RunTask again — it's a resume, not a new task.
	runCountAfter := prov.RunTaskCallCount()
	if runCountAfter != runCountBefore {
		t.Errorf("resume must not call RunTask again; before=%d after=%d", runCountBefore, runCountAfter)
	}

	// Resume must reach COMPLETED.
	hasCompleted := false
	for _, s := range resumeStates {
		if s == sdka2a.TaskStateCompleted {
			hasCompleted = true
		}
	}
	if !hasCompleted {
		t.Errorf("resume should reach COMPLETED, got: %v", resumeStates)
	}

	// Gateway called exactly once (approval → send).
	if gw.CallCount() != 1 {
		t.Errorf("expected exactly 1 gateway.Send on approval; got %d", gw.CallCount())
	}
}

// TestSupervisor_RejectionPreventsGateway verifies that when a human rejects the
// escalation, the gateway is never called and the task reaches REJECTED.
// Satisfies: approval-flow spec "Rejection prevents the send entirely".
func TestSupervisor_RejectionPreventsGateway(t *testing.T) {
	prov := &fake.Provider{
		ReturnRunResult: port.ProviderResult{
			ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
		},
	}
	gw := &fake.Gateway{}
	eng := policy.NewEngine()
	policyCfg := makePolicyCfg("telegram_send", "risky", []string{"ceo"})

	sup := newTestSupervisor(t, "ceo", prov, gw, eng, policyCfg)
	sup.MarkReady()

	ctx := context.Background()
	const taskID = "task-reject-1"

	// Park the task.
	execCtx := newExecCtx(taskID, "send a telegram")
	_ = collectEvents(ctx, sup, execCtx)

	// Deliver rejection.
	resumeCtx := newResumeExecCtx(taskID, "reject")
	rejectStates := collectEvents(ctx, sup, resumeCtx)

	hasRejected := false
	for _, s := range rejectStates {
		if s == sdka2a.TaskStateRejected {
			hasRejected = true
		}
	}
	if !hasRejected {
		t.Errorf("expected REJECTED after rejection, got: %v", rejectStates)
	}
	// Gateway must not be called after rejection.
	if gw.CallCount() != 0 {
		t.Errorf("gateway must not be called after rejection; got %d calls", gw.CallCount())
	}
}
