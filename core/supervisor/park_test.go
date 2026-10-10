// Tests for park/resume (Slice 6 of odd/tasks/ceo-orchestration-loop.md).
//
// A delegating task whose peer is still running is PARKED: its execution ends in a
// resumable, non-terminal state and a watcher polls the peer. When the peer stops
// advancing the watcher resumes the task into the next provider round through
// port.TaskResumer. These tests drive the supervisor directly (Execute) with a
// fake delegator and a resumer that re-enters Execute the way a2asrv would.
package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

// pollTick keeps every watcher test fast; production uses defaultPeerPollInterval.
const pollTick = 2 * time.Millisecond

// executeResumer is a port.TaskResumer that re-enters Execute as a resume, the
// same thing the a2asrv handler does when a message carries an existing TaskID.
type executeResumer struct {
	sup *Supervisor
	mu  sync.Mutex
	// inputs records the ResumePoint input of every resume, in order.
	inputs []string
}

func (r *executeResumer) Resume(ctx context.Context, rp port.ResumePoint) error {
	r.mu.Lock()
	r.inputs = append(r.inputs, rp.Input)
	r.mu.Unlock()
	collectStatusEvents(ctx, r.sup, newResumeExecCtx(rp.TaskID, rp.Input))
	return nil
}

// parkHarness is a delegating CEO whose peer reports the scripted PeerTaskState
// sequence, wired with a resumer and a fast poll interval.
type parkHarness struct {
	*delegationHarness
	resumer *executeResumer
}

// peerWorking is the Delegate result of a peer that accepted the task and is
// still running; the PeerTaskState sequence decides when it finishes.
func peerWorking() port.DelegationResult {
	return port.DelegationResult{PeerTaskID: "P1", State: string(sdka2a.TaskStateWorking)}
}

func peerDone(output string) port.DelegationResult {
	return port.DelegationResult{PeerTaskID: "P1", State: string(sdka2a.TaskStateCompleted), Output: output}
}

// newParkSupervisor builds a parkHarness. states is the PeerTaskState sequence.
func newParkSupervisor(t *testing.T, intents []port.ActionIntent, states ...port.DelegationResult) *parkHarness {
	t.Helper()
	del := &fake.Delegator{
		Results:       map[string]port.DelegationResult{"engineer": peerWorking()},
		StateSequence: states,
	}
	return wrapPark(t, newDelegatingSupervisor(t, "ceo", intents, del, 0))
}

func wrapPark(t *testing.T, h *delegationHarness) *parkHarness {
	t.Helper()
	h.sup.cfg.PeerPollInterval = pollTick
	r := &executeResumer{sup: h.sup}
	h.sup.SetResumer(r)
	t.Cleanup(h.sup.StopWatchers)
	return &parkHarness{delegationHarness: h, resumer: r}
}

// waitForRecord polls the store until pred holds, failing the test on timeout.
func waitForRecord(t *testing.T, sup *Supervisor, taskID string, pred func(TaskRecord) bool) TaskRecord {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec, err := sup.cfg.Store.Load(sup.cfg.Addr, taskID)
		if err == nil && pred(rec) {
			return rec
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for task %s; last record %+v (err %v)", taskID, rec, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func isState(state sdka2a.TaskState) func(TaskRecord) bool {
	return func(r TaskRecord) bool { return r.State == string(state) }
}

// T14/T15: a peer that is still running parks the CEO task in a resumable state
// instead of blocking the execution; the peer wait is recorded on the record.
func TestPark_PeerStillRunningParksTask(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")},
		peerWorking(), peerWorking(), peerWorking())
	events := collectStatusEvents(context.Background(), h.sup, newExecCtx("park-1", "go"))

	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("a task waiting on a peer must end its execution parked in %v, got %v", sdka2a.TaskStateInputRequired, got)
	}
	rec, err := h.sup.cfg.Store.Load(h.sup.cfg.Addr, "park-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec.State != string(sdka2a.TaskStateInputRequired) || rec.AwaitingPeerRole != "engineer" || rec.AwaitingPeerTaskID != "P1" {
		t.Errorf("record = state %q awaiting %q/%q, want parked on engineer/P1", rec.State, rec.AwaitingPeerRole, rec.AwaitingPeerTaskID)
	}
	if rec.PendingIntentKind != "" {
		t.Errorf("a peer wait is not an escalation; PendingIntentKind = %q", rec.PendingIntentKind)
	}
	if got := h.prov.RunTaskCallCount(); got != 1 {
		t.Errorf("RunTask calls = %d, want 1 (no second round until the peer answers)", got)
	}
}

// T15: when the peer completes the watcher appends its turn and resumes the task
// into the next provider round, which produces the CEO's final answer.
func TestPark_WatcherResumesNextRoundAfterPeerCompletes(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")},
		peerWorking(), peerWorking(), peerDone("peer out"))
	collectStatusEvents(context.Background(), h.sup, newExecCtx("park-2", "go"))

	rec := waitForRecord(t, h.sup, "park-2", isState(sdka2a.TaskStateCompleted))
	if rec.Output != agentOwnOutput {
		t.Errorf("Output = %q, want the CEO's final answer %q", rec.Output, agentOwnOutput)
	}
	if rec.AwaitingPeerTaskID != "" || rec.AwaitingPeerRole != "" {
		t.Errorf("awaiting fields must clear once consumed; got %q/%q", rec.AwaitingPeerRole, rec.AwaitingPeerTaskID)
	}
	if got := h.prov.RunTaskCallCount(); got != 2 {
		t.Fatalf("RunTask calls = %d, want 2", got)
	}
	if !strings.Contains(h.prov.RunCalls[1].Input, "peer out") {
		t.Errorf("second round must see the peer result; input = %q", h.prov.RunCalls[1].Input)
	}
	roles := make([]string, len(rec.Turns))
	for i, turn := range rec.Turns {
		roles[i] = turn.Role
	}
	if strings.Join(roles, ",") != "user,assistant,peer,assistant" {
		t.Errorf("turn roles = %v, want user,assistant,peer,assistant", roles)
	}
}

// T15: Cancel on a parked task stops its watcher; nothing polls the peer afterwards.
func TestPark_CancelStopsWatcher(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")}, peerWorking())
	collectStatusEvents(context.Background(), h.sup, newExecCtx("park-3", "go"))
	waitFor(t, func() bool { return h.del.StateCallCount() > 0 })

	for range h.sup.Cancel(context.Background(), newResumeExecCtx("park-3", "")) {
	}
	h.sup.StopWatchers() // waits for every watcher goroutine; hangs the test on a leak
	settled := h.del.StateCallCount()
	time.Sleep(10 * pollTick)
	if got := h.del.StateCallCount(); got != settled {
		t.Errorf("peer polled %d more times after cancel", got-settled)
	}
	if n := h.sup.watcherCount(); n != 0 {
		t.Errorf("watchers after cancel = %d, want 0", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// T14: a verdict that arrives while the peer is still running is accepted (the
// resume is delivered, no execution is in flight) and leaves the task parked.
func TestPark_VerdictWhilePeerRunningKeepsTaskParked(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")}, peerWorking())
	h.sup.cfg.PeerPollInterval = time.Hour // keep the watcher out of this test
	collectStatusEvents(context.Background(), h.sup, newExecCtx("park-4", "go"))

	events := collectStatusEvents(context.Background(), h.sup, newResumeExecCtx("park-4", "approve"))
	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("a verdict for a task awaiting a peer must leave it parked, got %v", got)
	}
	rec, _ := h.sup.cfg.Store.Load(h.sup.cfg.Addr, "park-4")
	if rec.AwaitingPeerTaskID != "P1" {
		t.Errorf("still awaiting P1; got %q", rec.AwaitingPeerTaskID)
	}
	if got := h.prov.RunTaskCallCount(); got != 1 {
		t.Errorf("a premature verdict must not run another round; RunTask calls = %d", got)
	}
}

// T15: the round cap counts rounds across park/resume boundaries, not per
// execution, so a peer that always answers cannot loop the CEO forever.
func TestPark_RoundCapSurvivesResume(t *testing.T) {
	h := newParkSupervisor(t, nil, peerWorking(), peerDone("again"))
	h.prov.ReturnRunResults = nil
	h.prov.ReturnRunResult = port.ProviderResult{
		Output: "more", ActionIntents: []port.ActionIntent{delegateIntent("engineer", "again")},
	}
	collectStatusEvents(context.Background(), h.sup, newExecCtx("park-5", "go"))

	rec := waitForRecord(t, h.sup, "park-5", isState(sdka2a.TaskStateFailed))
	if got := h.prov.RunTaskCallCount(); got != maxProviderRounds {
		t.Errorf("RunTask calls = %d, want exactly %d", got, maxProviderRounds)
	}
	if !strings.Contains(rec.Output, "round limit") {
		t.Errorf("exhaustion must be explicit; Output = %q", rec.Output)
	}
}

// T15's TestPark_PeerInputRequiredFailsTask was overturned by Slice 8: the
// replacement is TestChained_PeerEscalationKeepsCEOParkedUntilPeerTerminal.

// design D10, preserved across park/resume: DelegateTimeout bounds the peer wait.
// A peer that never finishes fails the parked task, naming nothing it did not do:
// the peer's own task is left running.
func TestPark_PeerWaitTimeoutFailsTask(t *testing.T) {
	del := &fake.Delegator{
		Results:       map[string]port.DelegationResult{"engineer": peerWorking()},
		StateSequence: []port.DelegationResult{peerWorking()},
	}
	h := wrapPark(t, newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 30*time.Millisecond))
	collectStatusEvents(context.Background(), h.sup, newExecCtx("park-8", "go"))

	rec := waitForRecord(t, h.sup, "park-8", isState(sdka2a.TaskStateFailed))
	if rec.Output != agentOwnOutput {
		t.Errorf("a timed-out wait must fabricate no output; got %q", rec.Output)
	}
	if rec.AwaitingPeerTaskID != "" {
		t.Errorf("awaiting fields must clear on failure; got %q", rec.AwaitingPeerTaskID)
	}
}

// T15: without a resumer a parked task could never wake, so delegation fails
// loudly instead of parking forever.
func TestPark_NoResumerFailsDelegation(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{"engineer": peerWorking()}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)
	last, _ := h.run(t, "park-7")
	if last.Status.State != sdka2a.TaskStateFailed {
		t.Fatalf("state = %v, want FAILED when no resumer is configured", last.Status.State)
	}
}

// T14 / R3-resume-publishes-pre-delegation-text: after a human approves an
// escalated delegate_task the resume runs the delegation AND re-invokes the
// provider with the peer result, ending with the CEO's final answer.
func TestResume_ApprovedEscalatedDelegationReinvokesProvider(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{"engineer": peerDone("peer out")}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)
	h.sup.cfg.PolicyConfig[port.KindDelegateTask] = config.Policy{Risk: "risky", AllowedRoles: []string{"ceo"}}

	events := collectStatusEvents(context.Background(), h.sup, newExecCtx("resume-1", "go"))
	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("risky delegation must escalate, got %v", got)
	}
	events = collectStatusEvents(context.Background(), h.sup, newResumeExecCtx("resume-1", "approve"))
	last := terminalEvent(t, events)

	if last.Status.State != sdka2a.TaskStateCompleted {
		t.Fatalf("state = %v, want COMPLETED", last.Status.State)
	}
	if got := h.prov.RunTaskCallCount(); got != 2 {
		t.Fatalf("RunTask calls = %d, want 2 (resume must re-invoke the provider)", got)
	}
	if !strings.Contains(h.prov.RunCalls[1].Input, "peer out") {
		t.Errorf("re-invocation must carry the peer result; input = %q", h.prov.RunCalls[1].Input)
	}
	rec, _ := h.sup.cfg.Store.Load(h.sup.cfg.Addr, "resume-1")
	if rec.Output != agentOwnOutput || messageText(last.Status.Message) != agentOwnOutput {
		t.Errorf("Output = %q / event %q, want the CEO's final answer, not the pre-delegation text",
			rec.Output, messageText(last.Status.Message))
	}
}

// T14 / R3-finish-lost-on-halt: a turn that called finish_task and then halted on
// an escalation must remember it finished. The approved intent runs and the task
// completes with the finish text, with no further provider round.
func TestResume_FinishPersistedAcrossEscalation(t *testing.T) {
	h := newMultiIntentSupervisor(t, []port.ActionIntent{
		telegramIntent("shipped"), finishIntent("final text"),
	}, riskyTelegramSafeDelegate())

	events := collectStatusEvents(context.Background(), h.sup, newExecCtx("resume-2", "go"))
	if got := lastState(t, events); got != sdka2a.TaskStateInputRequired {
		t.Fatalf("telegram must escalate, got %v", got)
	}
	if rec := h.record(t, "resume-2"); !rec.FinishRequested {
		t.Fatalf("a finishing turn that parks must persist the finish signal: %+v", rec)
	}
	events = collectStatusEvents(context.Background(), h.sup, newResumeExecCtx("resume-2", "approve"))
	last := terminalEvent(t, events)

	rec := h.record(t, "resume-2")
	if last.Status.State != sdka2a.TaskStateCompleted || rec.Output != "final text" {
		t.Errorf("state/output = %v/%q, want COMPLETED/final text", last.Status.State, rec.Output)
	}
	if got := h.gw.CallCount(); got != 1 {
		t.Errorf("approved telegram must be sent once; gateway calls = %d", got)
	}
	if got := len(h.prov.RunCalls); got != 1 {
		t.Errorf("RunTask calls = %d, want 1 (the turn already finished)", got)
	}
	if rec.FinishRequested {
		t.Error("the finish signal is spent on completion and must not linger on the record")
	}
}

// T15: records written before the awaiting-peer and finish fields existed still
// load. The bytes are the literal on-disk shape of the older schema.
func TestStore_LoadsRecordWrittenBeforeAwaitingPeer(t *testing.T) {
	dir := t.TempDir()
	addr := mustAddr(t, "ceo", "acme")
	legacy := `[{"task_id":"task-old","state":"TASK_STATE_INPUT_REQUIRED",` +
		`"input":"go","owner":"ceo/acme","pending_intent_kind":"telegram_send",` +
		`"turns":[{"Role":"user","Content":"go","At":"2026-10-01T00:00:00Z"}]}]`
	if err := os.WriteFile(filepath.Join(dir, filenameFor(addr)), []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, err := s.Load(addr, "task-old")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AwaitingPeerRole != "" || got.AwaitingPeerTaskID != "" || got.FinishRequested {
		t.Errorf("absent fields must read back zero: %+v", got)
	}
	if err := s.Save(addr, got); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, filenameFor(addr)))
	for _, key := range []string{"awaiting_peer", "finish_requested"} {
		if strings.Contains(string(data), key) {
			t.Errorf("omitempty must keep %q out of the record; got %s", key, data)
		}
	}
}

// T16: a parked task persisted to the store survives a restart. A NEW supervisor
// over the same store re-arms the watcher at startup and the conversation
// continues with the earlier rounds intact.
func TestPark_RestartRearmsWatcherAndContinuesConversation(t *testing.T) {
	first := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")}, peerWorking())
	first.sup.cfg.PeerPollInterval = time.Hour
	collectStatusEvents(context.Background(), first.sup, newExecCtx("restart-1", "go"))
	first.sup.StopWatchers() // the process "dies": its watchers are gone, the record remains

	// New process: same store, fresh supervisor, peer has finished meanwhile.
	del := &fake.Delegator{StateSequence: []port.DelegationResult{peerDone("peer out")}}
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "final after restart"}}
	second := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	second.sup.cfg.Store = first.sup.cfg.Store
	second.sup.cfg.Provider = prov
	wrapPark(t, second)

	noRegister := func(context.Context, string, string) error { return nil }
	if err := second.sup.RecoverOpenTasks(context.Background(), nil, noRegister); err != nil {
		t.Fatalf("RecoverOpenTasks: %v", err)
	}

	rec := waitForRecord(t, second.sup, "restart-1", isState(sdka2a.TaskStateCompleted))
	if rec.Output != "final after restart" {
		t.Errorf("Output = %q, want the post-restart CEO answer", rec.Output)
	}
	if got := len(prov.RunCalls); got != 1 {
		t.Fatalf("post-restart RunTask calls = %d, want 1", got)
	}
	input := prov.RunCalls[0].Input
	for _, want := range []string{"go", agentOwnOutput, "peer out"} {
		if !strings.Contains(input, want) {
			t.Errorf("post-restart input lacks earlier-round content %q: %q", want, input)
		}
	}
}

// countResumer counts resumes without re-entering Execute.
type countResumer struct{ n atomic.Int32 }

func (r *countResumer) Resume(context.Context, port.ResumePoint) error { r.n.Add(1); return nil }

// R3: a verdict that consumes the finished peer's result completes the task
// without its watcher; the watcher must exit and never resume a settled task.
func TestPark_VerdictConsumingPeerStopsWatcher(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")}, peerDone("peer out"))
	h.sup.cfg.PeerPollInterval = 100 * time.Millisecond
	cr := &countResumer{}
	h.sup.SetResumer(cr)
	collectStatusEvents(context.Background(), h.sup, newExecCtx("park-r3", "go"))

	collectStatusEvents(context.Background(), h.sup, newResumeExecCtx("park-r3", "approve"))
	waitForRecord(t, h.sup, "park-r3", isState(sdka2a.TaskStateCompleted))
	time.Sleep(350 * time.Millisecond)
	if n := h.sup.watcherCount(); n != 0 {
		t.Errorf("watchers after non-watcher resume = %d, want 0", n)
	}
	if n := cr.n.Load(); n != 0 {
		t.Errorf("watcher resumed a settled task %d times", n)
	}
}

// R3: a task escalated to a human is not parked on its peer; the watcher exits
// without delivering "peer-update" as if it were the verdict.
func TestPark_WatcherDoesNotResumeHumanEscalation(t *testing.T) {
	h := newParkSupervisor(t, nil, peerDone("x"))
	cr := &countResumer{}
	h.sup.SetResumer(cr)
	rec := TaskRecord{TaskID: "park-r3b", State: string(sdka2a.TaskStateInputRequired), AwaitingPeerRole: "engineer",
		AwaitingPeerTaskID: "P1", PendingIntentKind: "delegate"}
	_ = h.sup.cfg.Store.Save(h.sup.cfg.Addr, rec)
	h.sup.armWatcher("park-r3b", "engineer", "P1")

	waitFor(t, func() bool { return h.sup.watcherCount() == 0 })
	if n := cr.n.Load(); n != 0 {
		t.Errorf("watcher resumed a human escalation %d times", n)
	}
}
