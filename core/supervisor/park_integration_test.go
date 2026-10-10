package supervisor_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/config"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
	transa2a "github.com/salgozino/ai-solo-startup-framework/transport/a2a"
)

// sendCEOTaskBounded is sendCEOTask with a deadline: before park/resume the call
// blocked for as long as the peer worked, so a regression must fail the test
// rather than hang it.
func sendCEOTaskBounded(t *testing.T, srv *transa2a.Server, tenant, input string) *sdka2a.Task {
	t.Helper()
	done := make(chan *sdka2a.Task, 1)
	go func() { done <- sendCEOTask(t, srv, tenant, input) }()
	select {
	case task := <-done:
		return task
	case <-time.After(5 * time.Second):
		t.Fatal("CEO SendMessage still blocked after 5s: a task waiting on a peer must park, not hold its execution")
		return nil
	}
}

func getTask(t *testing.T, srv *transa2a.Server, tenant string, id sdka2a.TaskID) *sdka2a.Task {
	t.Helper()
	task, err := srv.Handler().GetTask(context.Background(), &sdka2a.GetTaskRequest{Tenant: tenant, ID: id})
	if err != nil {
		t.Fatalf("GetTask(%s): %v", id, err)
	}
	return task
}

// awaitCEOTask polls the CEO's task until it reaches a terminal state.
func awaitCEOTask(t *testing.T, srv *transa2a.Server, tenant string, id sdka2a.TaskID) *sdka2a.Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		task := getTask(t, srv, tenant, id)
		if task.Status.State.Terminal() {
			return task
		}
		if time.Now().After(deadline) {
			t.Fatalf("CEO task %s still %s after 10s", id, task.Status.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// gatedCompany starts a CEO that delegates once to an engineer whose provider is
// gated by the returned release func, so tests control when the peer finishes.
func gatedCompany(t *testing.T, tenant string) (*delegationCompany, *fake.Provider, func()) {
	t.Helper()
	release := make(chan struct{})
	released := false
	releaseFn := func() {
		if !released {
			released = true
			close(release)
		}
	}
	t.Cleanup(releaseFn)
	engineerProv := &blockingUntilReleased{
		Provider: &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "peer finished"}},
		release:  release,
	}
	ceoProv := &fake.Provider{
		ReturnRunResults: []port.ProviderResult{{
			Output:        "delegating",
			ActionIntents: []port.ActionIntent{delegateTaskIntentTo("engineer", "build X")},
		}},
		ReturnRunResult: port.ProviderResult{Output: "CEO final answer"},
	}
	return startDelegationCompany(t, tenant, ceoProv, engineerProv, 0), ceoProv, releaseFn
}

// T14: while the peer is still running the CEO task is PARKED, not WORKING inside
// a live execution: it is cancellable.
func TestPark_ParkedCEOTaskIsCancellableWhilePeerRuns(t *testing.T) {
	tenant := "acme"
	co, _, _ := gatedCompany(t, tenant)

	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "please delegate")
	if task.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("CEO task state = %v, want parked in INPUT_REQUIRED while the peer runs", task.Status.State)
	}

	canceled, err := co.ceoSrv.Handler().CancelTask(context.Background(), &sdka2a.CancelTaskRequest{Tenant: tenant, ID: task.ID})
	if err != nil {
		t.Fatalf("CancelTask on a parked task: %v", err)
	}
	if canceled.Status.State != sdka2a.TaskStateCanceled {
		t.Errorf("state after cancel = %v, want CANCELED", canceled.Status.State)
	}
}

// T14: a verdict posted while the peer is still running is accepted — it is not
// rejected with ErrExecutionInProgress, which is what made the UI answer 409.
func TestPark_VerdictAcceptedWhilePeerRuns(t *testing.T) {
	tenant := "acme"
	co, _, _ := gatedCompany(t, tenant)

	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "please delegate")
	if task.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("CEO task state = %v, want parked", task.Status.State)
	}

	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("approve"))
	msg.TaskID = task.ID
	_, err := co.ceoSrv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{Tenant: tenant, Message: msg})
	if err != nil {
		t.Fatalf("verdict on a parked task must be accepted, got: %v", err)
	}
	if got := getTask(t, co.ceoSrv, tenant, task.ID).Status.State; got != sdka2a.TaskStateInputRequired {
		t.Errorf("state after a premature verdict = %v, want still parked", got)
	}
}

// T15: once the peer finishes, the watcher resumes the parked CEO task through
// the real a2asrv handler into its next round and it ends with the CEO's answer.
func TestPark_WatcherResumesCEOTaskOverRealWire(t *testing.T) {
	tenant := "acme"
	co, ceoProv, release := gatedCompany(t, tenant)

	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "please delegate")
	if task.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("CEO task state = %v, want parked", task.Status.State)
	}
	release()

	final := awaitCEOTask(t, co.ceoSrv, tenant, task.ID)
	if final.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("CEO task state = %v, want COMPLETED", final.Status.State)
	}
	if got := textOf(final.Status.Message); got != "CEO final answer" {
		t.Errorf("output = %q, want the CEO's final answer", got)
	}
	if got := ceoProv.RunTaskCallCount(); got != 2 {
		t.Errorf("CEO RunTask calls = %d, want 2", got)
	}
}

// sendCEOTaskToEnd sends a task and waits for it to reach a terminal state,
// whether the CEO parked on its peer in between or not.
func sendCEOTaskToEnd(t *testing.T, srv *transa2a.Server, tenant, input string) *sdka2a.Task {
	t.Helper()
	task := sendCEOTaskBounded(t, srv, tenant, input)
	if task.Status.State.Terminal() {
		return task
	}
	return awaitCEOTask(t, srv, tenant, task.ID)
}

// T16: crash recovery across a round boundary, over the real wire. The CEO parks
// on a running peer; its process "dies" (server shut down, watchers gone, only the
// store survives); a NEW supervisor and server over the same store re-arm the
// watcher at startup, and when the peer finishes the conversation continues into
// the next round with the earlier round's content intact.
func TestPark_RestartRearmsWatcherOverRealWire(t *testing.T) {
	tenant := "acme"
	co, ceo1Prov, release := gatedCompany(t, tenant)
	ceo1Store := co.ceoStore
	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "please delegate")
	if task.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("CEO task state = %v, want parked", task.Status.State)
	}
	if err := co.ceoSrv.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown first CEO: %v", err)
	}
	co.ceoSup.StopWatchers()
	if got := ceo1Prov.RunTaskCallCount(); got != 1 {
		t.Fatalf("first CEO RunTask calls = %d, want 1", got)
	}

	ceo2Prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "CEO answer after restart"}}
	policyCfg := map[string]config.Policy{
		port.KindDelegateTask: {Risk: "safe", AllowedRoles: []string{"ceo"}},
	}
	_, ceo2Srv := startDelegatingSupervisorOn(t, ceo1Store, "ceo", tenant, ceo2Prov, co.delegator, 0, policyCfg)
	release()

	final := awaitCEOTask(t, ceo2Srv, tenant, task.ID)
	if final.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("CEO task after restart = %v, want COMPLETED", final.Status.State)
	}
	if got := textOf(final.Status.Message); got != "CEO answer after restart" {
		t.Errorf("output = %q", got)
	}
	if got := ceo2Prov.RunTaskCallCount(); got != 1 {
		t.Fatalf("restarted CEO RunTask calls = %d, want 1", got)
	}
	input := ceo2Prov.RunCalls[0].Input
	for _, want := range []string{"please delegate", "delegating", "peer finished"} {
		if !strings.Contains(input, want) {
			t.Errorf("restarted CEO input lacks earlier-round content %q: %q", want, input)
		}
	}
}

// T17/T18 (Slice 8) acceptance, over the real wire: the human's task is delegated to
// the engineer, whose risky action escalates. The CEO stays parked on the peer; ONE
// approval, posted on the engineer's task, completes the engineer and the CEO
// resumes by itself into round two and ends with finish_task. Nothing is ever posted
// to the CEO's task.
func TestChained_PeerApprovalResumesCEOWithoutHumanActionOnCEOTask(t *testing.T) {
	tenant := "acme"
	engineerProv := &fake.Provider{ReturnRunResult: port.ProviderResult{
		Output:        "engineer shipped it",
		ActionIntents: []port.ActionIntent{{Kind: "telegram_send", Payload: map[string]any{"body": "release notes"}}},
	}}
	ceoProv := &fake.Provider{ReturnRunResults: []port.ProviderResult{
		{Output: "delegating", ActionIntents: []port.ActionIntent{delegateTaskIntentTo("engineer", "ship X")}},
		{Output: "ignored", ActionIntents: []port.ActionIntent{{Kind: port.KindFinishTask, Payload: map[string]any{"body": "all done: engineer shipped it"}}}},
	}}
	co := startDelegationCompanyWith(t, tenant, ceoProv, engineerProv, 0, map[string]config.Policy{
		"telegram_send": {Risk: "risky", AllowedRoles: []string{"engineer"}},
	})

	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "ship X")
	if task.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("CEO task state = %v, want parked", task.Status.State)
	}

	// The CEO's record says whose approval it waits for, and which peer task to approve.
	rec := waitUntil(t, func() (supervisor.TaskRecord, bool) {
		r, err := co.ceoStore.Load(co.ceoSup.Addr(), string(task.ID))
		return r, err == nil && r.AwaitingPeerApproval
	})
	if rec.AwaitingPeerRole != "engineer" || rec.AwaitingPeerTaskID == "" {
		t.Fatalf("CEO record = %+v, want awaiting the engineer's approval", rec)
	}
	peerID := sdka2a.TaskID(rec.AwaitingPeerTaskID)
	if got := getTask(t, co.engineerSrv, tenant, peerID).Status.State; got != sdka2a.TaskStateInputRequired {
		t.Fatalf("engineer task = %v, want INPUT_REQUIRED", got)
	}

	// Parked, not failed and not in a new round, while the human has not decided.
	time.Sleep(100 * time.Millisecond)
	if got := getTask(t, co.ceoSrv, tenant, task.ID).Status.State; got != sdka2a.TaskStateInputRequired {
		t.Fatalf("CEO state while the peer awaits approval = %v, want INPUT_REQUIRED", got)
	}
	if got := ceoProv.RunTaskCallCount(); got != 1 {
		t.Fatalf("CEO RunTask calls = %d while the peer awaits approval, want 1", got)
	}

	// The single human action: approve the engineer's task (what the UI verdict does).
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("approve"))
	msg.TaskID = peerID
	if _, err := co.engineerSrv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{Tenant: tenant, Message: msg}); err != nil {
		t.Fatalf("approve engineer task: %v", err)
	}

	final := awaitCEOTask(t, co.ceoSrv, tenant, task.ID)
	if final.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("CEO task = %v, want COMPLETED", final.Status.State)
	}
	if got := textOf(final.Status.Message); got != "all done: engineer shipped it" {
		t.Errorf("CEO output = %q, want the finish_task answer", got)
	}
	if got := ceoProv.RunTaskCallCount(); got != 2 {
		t.Errorf("CEO RunTask calls = %d, want 2", got)
	}
	if in := ceoProv.RunCalls[1].Input; !strings.Contains(in, "engineer shipped it") {
		t.Errorf("round two must carry the engineer's output; input = %q", in)
	}
}

// waitUntil polls fn until it reports ok, failing the test after 5s.
func waitUntil[T any](t *testing.T, fn func() (T, bool)) T {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if v, ok := fn(); ok {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
