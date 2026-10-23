package supervisor_test

import (
	"context"
	"sync"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
)

// probeProvider runs hook at the start of every RunTask after the first, so a test
// can observe the task while its next provider round is in flight.
type probeProvider struct {
	*fake.Provider
	mu    sync.Mutex
	calls int
	hook  func()
}

func (p *probeProvider) RunTask(ctx context.Context, taskID, input string) (port.ProviderResult, error) {
	p.mu.Lock()
	p.calls++
	n := p.calls
	p.mu.Unlock()
	if n > 1 && p.hook != nil {
		p.hook()
	}
	return p.Provider.RunTask(ctx, taskID, input)
}

// S2: a task resumed after its peer's result is WORKING (the store record, which
// is what the task list API and state broadcasts read) while its next provider round runs, so the UI shows no
// Approve/Reject; it is COMPLETED at the end.
func TestResume_NextRoundReportsWorkingNotInputRequired(t *testing.T) {
	tenant := "acme"
	release := make(chan struct{})
	engineerProv := &blockingUntilReleased{
		Provider: &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "peer finished"}},
		release:  release,
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	// taskID, recDuring and notified are shared with the supervisor's goroutines
	// (provider hook, state-change callback); nmu guards all three.
	var co *delegationCompany
	var nmu sync.Mutex
	var taskID sdka2a.TaskID
	var recDuring supervisor.TaskRecord
	var notified []string
	currentTask := func() sdka2a.TaskID {
		nmu.Lock()
		defer nmu.Unlock()
		return taskID
	}
	ceoProv := &probeProvider{Provider: &fake.Provider{
		ReturnRunResults: []port.ProviderResult{
			{Output: "delegating", ActionIntents: []port.ActionIntent{delegateTaskIntentTo("engineer", "build X")}},
			{Output: "wrap up"},
		},
	}}
	ceoProv.hook = func() {
		rec, _ := co.ceoStore.Load(co.ceoSup.Addr(), string(currentTask()))
		nmu.Lock()
		recDuring = rec
		nmu.Unlock()
	}
	co = startDelegationCompany(t, tenant, ceoProv, engineerProv, 0)
	co.ceoSup.SetOnStateChange(func() {
		id := currentTask()
		if id == "" {
			return
		}
		rec, err := co.ceoStore.Load(co.ceoSup.Addr(), string(id))
		if err == nil {
			nmu.Lock()
			notified = append(notified, rec.State)
			nmu.Unlock()
		}
	})

	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "please delegate")
	nmu.Lock()
	taskID = task.ID
	nmu.Unlock()
	if task.Status.State != sdka2a.TaskStateInputRequired {
		t.Fatalf("state = %v, want parked", task.Status.State)
	}
	close(release)

	final := awaitCEOTask(t, co.ceoSrv, tenant, task.ID)
	if final.Status.State != sdka2a.TaskStateCompleted || textOf(final.Status.Message) != "wrap up" {
		t.Fatalf("final = %v/%q, want COMPLETED/wrap up", final.Status.State, textOf(final.Status.Message))
	}
	nmu.Lock()
	defer nmu.Unlock()
	if recDuring.State != string(sdka2a.TaskStateWorking) {
		t.Errorf("record state during round 2 = %q, want TASK_STATE_WORKING", recDuring.State)
	}
	if recDuring.AwaitingPeerTaskID != "" || recDuring.PendingIntentKind != "" {
		t.Errorf("record during round 2 = %+v, want no awaiting peer / pending intent", recDuring)
	}
	sawWorking := false
	for _, s := range notified {
		if s == string(sdka2a.TaskStateWorking) {
			sawWorking = true
		}
	}
	if !sawWorking {
		t.Errorf("state broadcasts saw record states %v, want one reporting WORKING", notified)
	}
}

// S2 guard: parked tasks and real escalations keep reporting INPUT_REQUIRED, and a
// parked task is still told apart by awaiting_peer / pending_intent_kind.
func TestResume_ParkedAndEscalatedStillInputRequired(t *testing.T) {
	tenant := "acme"
	co, _, _ := gatedCompany(t, tenant)
	task := sendCEOTaskBounded(t, co.ceoSrv, tenant, "please delegate")
	rec, err := co.ceoStore.Load(co.ceoSup.Addr(), string(task.ID))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != string(sdka2a.TaskStateInputRequired) || rec.AwaitingPeerRole != "engineer" || rec.PendingIntentKind != "" {
		t.Errorf("parked record = %+v, want INPUT_REQUIRED awaiting engineer with no pending intent", rec)
	}
}
