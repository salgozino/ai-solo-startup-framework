// Tests for the delegations list persisted on the delegating task's record
// (odd/tasks/ui-task-tree.md, S2/S4): every delegate_task that yields a peer task
// leaves an ordered {role, peer task id} entry on the parent record.
package supervisor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
)

func peerResult(taskID string, state sdka2a.TaskState) port.DelegationResult {
	return port.DelegationResult{PeerTaskID: taskID, State: string(state), Output: "out"}
}

func recordOf(t *testing.T, sup *Supervisor, taskID string) TaskRecord {
	t.Helper()
	rec, err := sup.cfg.Store.Load(sup.cfg.Addr, taskID)
	if err != nil {
		t.Fatalf("store.Load(%q): %v", taskID, err)
	}
	return rec
}

// S2: a synchronously completed delegation records the peer's real task id.
func TestDelegations_CompletedPeerRecordsEntry(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": peerResult("PE-1", sdka2a.TaskStateCompleted),
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

	_, rec := h.run(t, "deleg-1")

	want := []Delegation{{Role: "engineer", TaskID: "PE-1"}}
	if !reflect.DeepEqual(rec.Delegations, want) {
		t.Fatalf("Delegations = %+v, want %+v", rec.Delegations, want)
	}
}

// S2: delegations from successive rounds accumulate in delegation order.
func TestDelegations_SuccessiveRoundsKeepOrder(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": peerResult("PE-1", sdka2a.TaskStateCompleted),
		"designer": peerResult("PD-1", sdka2a.TaskStateCompleted),
	}}
	h := newDelegatingSupervisor(t, "ceo", nil, del, 0)
	h.prov.ReturnRunResults = []port.ProviderResult{
		{Output: "r1", ActionIntents: []port.ActionIntent{delegateIntent("engineer", "a")}},
		{Output: "r2", ActionIntents: []port.ActionIntent{delegateIntent("designer", "b")}},
	}
	h.prov.ReturnRunResult = port.ProviderResult{Output: "final"}

	_, rec := h.run(t, "deleg-2")

	want := []Delegation{{Role: "engineer", TaskID: "PE-1"}, {Role: "designer", TaskID: "PD-1"}}
	if !reflect.DeepEqual(rec.Delegations, want) {
		t.Fatalf("Delegations = %+v, want %+v", rec.Delegations, want)
	}
}

// S2: a parked delegation is recorded before the resume and kept (once) after it.
func TestDelegations_ParkedPeerRecordedBeforeAndKeptAfterResume(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")},
		peerWorking(), peerDone("peer out"))
	collectStatusEvents(context.Background(), h.sup, newExecCtx("deleg-3", "go"))

	want := []Delegation{{Role: "engineer", TaskID: "P1"}}
	parked := recordOf(t, h.sup, "deleg-3")
	if parked.State != string(sdka2a.TaskStateInputRequired) || !reflect.DeepEqual(parked.Delegations, want) {
		t.Fatalf("parked record = state %q delegations %+v, want INPUT_REQUIRED with %+v", parked.State, parked.Delegations, want)
	}
	done := waitForRecord(t, h.sup, "deleg-3", isState(sdka2a.TaskStateCompleted))
	if !reflect.DeepEqual(done.Delegations, want) {
		t.Errorf("after resume Delegations = %+v, want %+v (no duplicate)", done.Delegations, want)
	}
}

// S2: a peer that escalated (chained approval) is recorded while the parent parks.
func TestDelegations_ChainedApprovalPeerRecorded(t *testing.T) {
	h := newParkSupervisor(t, []port.ActionIntent{delegateIntent("engineer", "build X")}, peerEscalated())
	collectStatusEvents(context.Background(), h.sup, newExecCtx("deleg-4", "go"))

	rec := waitForRecord(t, h.sup, "deleg-4", func(r TaskRecord) bool { return r.AwaitingPeerApproval })
	want := []Delegation{{Role: "engineer", TaskID: "P1"}}
	if !rec.AwaitingPeerApproval || !reflect.DeepEqual(rec.Delegations, want) {
		t.Fatalf("record = approval %v delegations %+v, want chained wait with %+v", rec.AwaitingPeerApproval, rec.Delegations, want)
	}
}

// S2: a peer that ended terminally without success still exists as a task.
func TestDelegations_FailedPeerRecordsEntry(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": peerResult("PE-9", sdka2a.TaskStateFailed),
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

	last, rec := h.run(t, "deleg-5")

	h.assertFailed(t, last, rec)
	want := []Delegation{{Role: "engineer", TaskID: "PE-9"}}
	if !reflect.DeepEqual(rec.Delegations, want) {
		t.Fatalf("Delegations = %+v, want %+v", rec.Delegations, want)
	}
}

// S2: a delegator error means no peer task exists, so nothing is recorded.
func TestDelegations_DelegatorErrorRecordsNothing(t *testing.T) {
	del := &fake.Delegator{Errors: map[string]error{"engineer": errors.New("boom")}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)

	_, rec := h.run(t, "deleg-6")

	if len(rec.Delegations) != 0 {
		t.Errorf("Delegations = %+v, want none", rec.Delegations)
	}
}

// S2/S4: an approved (escalated) delegation records its entry on the resume path.
func TestDelegations_ApprovedEscalatedDelegationRecordsEntry(t *testing.T) {
	del := &fake.Delegator{Results: map[string]port.DelegationResult{
		"engineer": peerResult("PE-2", sdka2a.TaskStateCompleted),
	}}
	h := newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0)
	h.sup.cfg.PolicyConfig[port.KindDelegateTask] = config.Policy{Risk: "risky", AllowedRoles: []string{"ceo"}}

	collectStatusEvents(context.Background(), h.sup, newExecCtx("deleg-7", "go"))
	collectStatusEvents(context.Background(), h.sup, newResumeExecCtx("deleg-7", "approve"))

	rec, _ := h.sup.cfg.Store.Load(h.sup.cfg.Addr, "deleg-7")
	want := []Delegation{{Role: "engineer", TaskID: "PE-2"}}
	if rec.State != string(sdka2a.TaskStateCompleted) || !reflect.DeepEqual(rec.Delegations, want) {
		t.Fatalf("record = %q %+v, want COMPLETED with %+v", rec.State, rec.Delegations, want)
	}
}

// S2/S4: an approved delegation whose peer is still running parks with the entry saved.
func TestDelegations_ApprovedDelegationThatParksKeepsEntry(t *testing.T) {
	del := &fake.Delegator{
		Results:       map[string]port.DelegationResult{"engineer": peerWorking()},
		StateSequence: []port.DelegationResult{peerWorking()},
	}
	h := wrapPark(t, newDelegatingSupervisor(t, "ceo", []port.ActionIntent{delegateIntent("engineer", "build X")}, del, 0))
	h.sup.cfg.PeerPollInterval = 1e12
	h.sup.cfg.PolicyConfig[port.KindDelegateTask] = config.Policy{Risk: "risky", AllowedRoles: []string{"ceo"}}

	collectStatusEvents(context.Background(), h.sup, newExecCtx("deleg-8", "go"))
	collectStatusEvents(context.Background(), h.sup, newResumeExecCtx("deleg-8", "approve"))

	rec := recordOf(t, h.sup, "deleg-8")
	want := []Delegation{{Role: "engineer", TaskID: "P1"}}
	if rec.AwaitingPeerTaskID != "P1" || !reflect.DeepEqual(rec.Delegations, want) {
		t.Fatalf("record = awaiting %q %+v, want parked on P1 with %+v", rec.AwaitingPeerTaskID, rec.Delegations, want)
	}
}

// S2: the entries survive a reload from disk.
func TestDelegations_SurviveStoreReload(t *testing.T) {
	dir := t.TempDir()
	addr := mustAddr(t, "ceo", "acme")
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	in := TaskRecord{TaskID: "t", State: "TASK_STATE_COMPLETED", Delegations: []Delegation{{Role: "engineer", TaskID: "P1"}, {Role: "qa", TaskID: "P2"}}}
	if err := s.Save(addr, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore reload: %v", err)
	}
	got, err := s2.Load(addr, "t")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.Delegations, in.Delegations) {
		t.Errorf("reloaded Delegations = %+v, want %+v", got.Delegations, in.Delegations)
	}
}

// S2: records written before the field existed decode with nil delegations and
// do not grow the key on re-save.
func TestDelegations_LegacyRecordDecodesNil(t *testing.T) {
	dir := t.TempDir()
	addr := mustAddr(t, "ceo", "acme")
	legacy := `[{"task_id":"task-old","state":"TASK_STATE_COMPLETED","input":"go","owner":"ceo/acme"}]`
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
	if got.Delegations != nil {
		t.Errorf("Delegations = %+v, want nil", got.Delegations)
	}
	if err := s.Save(addr, got); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, filenameFor(addr)))
	if strings.Contains(string(data), "delegations") {
		t.Errorf("omitempty must keep delegations out of the record; got %s", data)
	}
}
