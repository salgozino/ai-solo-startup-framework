package supervisor

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

func mustAddr(t *testing.T, name, tenant string) address.A2AAddress {
	t.Helper()
	addr, err := address.New(name, tenant)
	if err != nil {
		t.Fatalf("address.New(%q, %q): %v", name, tenant, err)
	}
	return addr
}

func TestStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addr := mustAddr(t, "ceo", "acme")
	rec := TaskRecord{
		TaskID: "task-1",
		State:  "TASK_STATE_WORKING",
		Input:  "do the thing",
		Owner:  string(addr),
	}

	if err := s.Save(addr, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load(addr, "task-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.TaskID != rec.TaskID {
		t.Errorf("TaskID: got %q, want %q", got.TaskID, rec.TaskID)
	}
	if got.State != rec.State {
		t.Errorf("State: got %q, want %q", got.State, rec.State)
	}
	if got.Input != rec.Input {
		t.Errorf("Input: got %q, want %q", got.Input, rec.Input)
	}
}

func TestStore_Update(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addr := mustAddr(t, "ceo", "acme")
	rec := TaskRecord{TaskID: "task-1", State: "TASK_STATE_WORKING", Input: "init", Owner: string(addr)}
	if err := s.Save(addr, rec); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	rec.State = "TASK_STATE_COMPLETED"
	if err := s.Save(addr, rec); err != nil {
		t.Fatalf("update Save: %v", err)
	}

	got, err := s.Load(addr, "task-1")
	if err != nil {
		t.Fatalf("Load after update: %v", err)
	}
	if got.State != "TASK_STATE_COMPLETED" {
		t.Errorf("expected updated state TASK_STATE_COMPLETED, got %q", got.State)
	}
}

func TestStore_TenantIsolation(t *testing.T) {
	// Loading with one tenant address must NOT return records of another tenant.
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addrAcme := mustAddr(t, "ceo", "acme")
	addrBeta := mustAddr(t, "ceo", "beta")

	recAcme := TaskRecord{TaskID: "task-acme", State: "TASK_STATE_WORKING", Input: "acme task", Owner: string(addrAcme)}
	recBeta := TaskRecord{TaskID: "task-beta", State: "TASK_STATE_WORKING", Input: "beta task", Owner: string(addrBeta)}

	if err := s.Save(addrAcme, recAcme); err != nil {
		t.Fatalf("Save acme: %v", err)
	}
	if err := s.Save(addrBeta, recBeta); err != nil {
		t.Fatalf("Save beta: %v", err)
	}

	// Loading acme's task-beta must return ErrTaskNotFound.
	_, err = s.Load(addrAcme, "task-beta")
	if err == nil {
		t.Error("expected ErrTaskNotFound when loading beta task under acme address, got nil")
	}

	// Loading beta's task-acme must return ErrTaskNotFound.
	_, err = s.Load(addrBeta, "task-acme")
	if err == nil {
		t.Error("expected ErrTaskNotFound when loading acme task under beta address, got nil")
	}

	// Each address returns only its own tasks.
	acmeTasks, err := s.LoadAll(addrAcme)
	if err != nil {
		t.Fatalf("LoadAll acme: %v", err)
	}
	if len(acmeTasks) != 1 || acmeTasks[0].TaskID != "task-acme" {
		t.Errorf("acme LoadAll: expected [task-acme], got %v", acmeTasks)
	}

	betaTasks, err := s.LoadAll(addrBeta)
	if err != nil {
		t.Fatalf("LoadAll beta: %v", err)
	}
	if len(betaTasks) != 1 || betaTasks[0].TaskID != "task-beta" {
		t.Errorf("beta LoadAll: expected [task-beta], got %v", betaTasks)
	}
}

func TestStore_Delete(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addr := mustAddr(t, "worker", "acme")
	rec := TaskRecord{TaskID: "task-del", State: "TASK_STATE_WORKING", Input: "x", Owner: string(addr)}
	if err := s.Save(addr, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Delete(addr, "task-del"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = s.Load(addr, "task-del")
	if err == nil {
		t.Error("expected ErrTaskNotFound after Delete, got nil")
	}
}

// TestStore_LoadsRecordWrittenBeforeRemainingIntents proves that a record file
// written by a build that predates PendingIntentTarget and RemainingIntents still
// loads. The bytes below are the exact on-disk shape of the older schema, not a
// re-marshalled TaskRecord, so the assertion cannot drift with the struct.
// Satisfies: feature task T3, "records written before this change still load".
func TestStore_LoadsRecordWrittenBeforeRemainingIntents(t *testing.T) {
	dir := t.TempDir()
	addr := mustAddr(t, "ceo", "acme")
	legacy := `[{"task_id":"task-legacy","state":"TASK_STATE_INPUT_REQUIRED",` +
		`"input":"send a telegram","owner":"ceo/acme",` +
		`"pending_intent_kind":"telegram_send","pending_intent_body":"Hello from CEO"}]`
	if err := os.WriteFile(filepath.Join(dir, filenameFor(addr)), []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed legacy record: %v", err)
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, err := s.Load(addr, "task-legacy")
	if err != nil {
		t.Fatalf("Load legacy record: %v", err)
	}

	if got.State != "TASK_STATE_INPUT_REQUIRED" {
		t.Errorf("State: got %q, want TASK_STATE_INPUT_REQUIRED", got.State)
	}
	if got.PendingIntentKind != "telegram_send" {
		t.Errorf("PendingIntentKind: got %q, want telegram_send", got.PendingIntentKind)
	}
	if got.PendingIntentBody != "Hello from CEO" {
		t.Errorf("PendingIntentBody: got %q, want %q", got.PendingIntentBody, "Hello from CEO")
	}
	// The fields the older writer knew nothing about must read back as zero
	// values, never as an error and never as garbage.
	if got.PendingIntentTarget != "" {
		t.Errorf("PendingIntentTarget: got %q, want empty", got.PendingIntentTarget)
	}
	if got.RemainingIntents != nil {
		t.Errorf("RemainingIntents: got %v, want nil", got.RemainingIntents)
	}

	// Re-saving must not corrupt the record: the absent fields stay absent.
	if err := s.Save(addr, got); err != nil {
		t.Fatalf("Save round trip: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, filenameFor(addr)))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, key := range []string{"pending_intent_target", "remaining_intents"} {
		if strings.Contains(string(data), key) {
			t.Errorf("omitempty must keep %q out of a record that has none; got %s", key, data)
		}
	}
}

// TestStore_RoundTripsTurns proves a task record carries its conversation
// transcript across a Save/Load cycle. Without this the supervisor can persist a
// multi-round task but reads back an amnesiac record, which is the whole point of
// the field.
// Satisfies: feature task T1, "a TaskRecord round-trips a non-empty turn list".
func TestStore_RoundTripsTurns(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addr := mustAddr(t, "ceo", "acme")
	// Fixed, UTC instants: a wall-clock now() would make the comparison depend on
	// the JSON encoder's monotonic-clock stripping rather than on persistence.
	first := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	second := first.Add(90 * time.Second)
	rec := TaskRecord{
		TaskID: "task-turns",
		State:  "TASK_STATE_WORKING",
		Input:  "plan the migration",
		Owner:  string(addr),
		Turns: []port.ContextMessage{
			{Role: "assistant", Content: "delegating the draft to the engineer", At: first},
			{Role: "peer", Content: "draft ready: three phases", At: second},
		},
	}

	if err := s.Save(addr, rec); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := s.Load(addr, "task-turns")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Compare each decoded message as a whole value rather than field by field:
	// a field added to port.ContextMessage later must break this test if it fails
	// to persist, instead of being silently skipped by a hand-written field list.
	//
	// reflect.DeepEqual is safe here precisely because the fixtures above are
	// fixed UTC instants. time.Time carries an optional monotonic reading and a
	// *Location pointer, neither of which survives JSON, so DeepEqual would be
	// wrong for a wall-clock now() or a non-UTC zone; for a UTC instant the
	// decoded value is bit-identical to the encoded one.
	if len(got.Turns) != len(rec.Turns) {
		t.Fatalf("Turns length: got %d, want %d", len(got.Turns), len(rec.Turns))
	}
	for i, want := range rec.Turns {
		if !reflect.DeepEqual(got.Turns[i], want) {
			t.Errorf("Turns[%d]: got %+v, want %+v", i, got.Turns[i], want)
		}
	}
}

// TestStore_SavePreservesOtherRecordTurns proves that saving one task leaves a
// different task's transcript in the same per-agent file untouched. Store.Save
// rewrites the ENTIRE per-agent array on every call, so silently clobbering or
// truncating a neighbour's turns is the most likely failure mode this field
// introduces, and a single-record store can never catch it.
// Closes native-review finding R3-single-record-store.
func TestStore_SavePreservesOtherRecordTurns(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addr := mustAddr(t, "ceo", "acme")
	// Fixed, UTC instants for the same reason as TestStore_RoundTripsTurns.
	base := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)

	neighbour := TaskRecord{
		TaskID: "task-neighbour",
		State:  "TASK_STATE_WORKING",
		Input:  "audit the invoices",
		Owner:  string(addr),
		Turns: []port.ContextMessage{
			{Role: "assistant", Content: "neighbour round one", At: base},
			{Role: "peer", Content: "neighbour round two", At: base.Add(30 * time.Second)},
			{Role: "assistant", Content: "neighbour round three", At: base.Add(60 * time.Second)},
		},
	}
	subject := TaskRecord{
		TaskID: "task-subject",
		State:  "TASK_STATE_WORKING",
		Input:  "plan the migration",
		Owner:  string(addr),
		Turns: []port.ContextMessage{
			{Role: "assistant", Content: "subject round one", At: base.Add(90 * time.Second)},
		},
	}

	if err := s.Save(addr, neighbour); err != nil {
		t.Fatalf("Save neighbour: %v", err)
	}
	if err := s.Save(addr, subject); err != nil {
		t.Fatalf("Save subject: %v", err)
	}

	// Grow the subject's transcript, exactly as a later delegation round would.
	subject.Turns = append(subject.Turns, port.ContextMessage{
		Role: "peer", Content: "subject round two", At: base.Add(120 * time.Second),
	})
	subject.State = "TASK_STATE_COMPLETED"
	if err := s.Save(addr, subject); err != nil {
		t.Fatalf("Save subject update: %v", err)
	}

	// The neighbour must survive content, order and count intact.
	got, err := s.Load(addr, "task-neighbour")
	if err != nil {
		t.Fatalf("Load neighbour: %v", err)
	}
	if !reflect.DeepEqual(got.Turns, neighbour.Turns) {
		t.Errorf("neighbour Turns: got %+v, want %+v", got.Turns, neighbour.Turns)
	}

	// And the write that triggered the rewrite must itself have landed, so a
	// Save that quietly does nothing cannot pass this test either.
	gotSubject, err := s.Load(addr, "task-subject")
	if err != nil {
		t.Fatalf("Load subject: %v", err)
	}
	if !reflect.DeepEqual(gotSubject.Turns, subject.Turns) {
		t.Errorf("subject Turns: got %+v, want %+v", gotSubject.Turns, subject.Turns)
	}

	// Neither record may be dropped or duplicated by the rewrite.
	all, err := s.LoadAll(addr)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("LoadAll: got %d records, want 2 (%+v)", len(all), all)
	}
}

// TestStore_LoadsRecordWrittenBeforeTurns proves that a record file written by a
// build that predates Turns still loads. The bytes below are the exact on-disk
// shape of the older schema, not a re-marshalled TaskRecord, so the assertion
// cannot drift with the struct.
// Satisfies: feature task T1, "literal pre-change JSON still loads with the new
// field as zero value".
func TestStore_LoadsRecordWrittenBeforeTurns(t *testing.T) {
	dir := t.TempDir()
	addr := mustAddr(t, "ceo", "acme")
	legacy := `[{"task_id":"task-legacy-schema","state":"TASK_STATE_INPUT_REQUIRED",` +
		`"input":"send a telegram","owner":"ceo/acme",` +
		`"pending_intent_kind":"telegram_send","pending_intent_body":"Hello from CEO",` +
		`"pending_intent_target":"engineer",` +
		`"remaining_intents":[{"kind":"telegram_send","body":"second message"}],` +
		`"output":"queued"}]`
	if err := os.WriteFile(filepath.Join(dir, filenameFor(addr)), []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed legacy record: %v", err)
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	got, err := s.Load(addr, "task-legacy-schema")
	if err != nil {
		t.Fatalf("Load legacy record: %v", err)
	}

	// Everything the older writer did know about must survive untouched.
	if got.State != "TASK_STATE_INPUT_REQUIRED" {
		t.Errorf("State: got %q, want TASK_STATE_INPUT_REQUIRED", got.State)
	}
	if got.PendingIntentTarget != "engineer" {
		t.Errorf("PendingIntentTarget: got %q, want engineer", got.PendingIntentTarget)
	}
	if len(got.RemainingIntents) != 1 || got.RemainingIntents[0].Kind != "telegram_send" {
		t.Errorf("RemainingIntents: got %v, want one telegram_send intent", got.RemainingIntents)
	}
	if got.Output != "queued" {
		t.Errorf("Output: got %q, want queued", got.Output)
	}
	// The field the older writer knew nothing about must read back as a zero
	// value, never as an error and never as garbage.
	if got.Turns != nil {
		t.Errorf("Turns: got %v, want nil", got.Turns)
	}

	// Re-saving must not corrupt the record: the absent field stays absent.
	if err := s.Save(addr, got); err != nil {
		t.Fatalf("Save round trip: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, filenameFor(addr)))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	// Match the quoted JSON key, not the bare word: a bare "turns" also matches
	// task IDs and free-text content, which would make this assertion lie.
	if strings.Contains(string(data), `"turns"`) {
		t.Errorf("omitempty must keep the %q key out of a record that has none; got %s", "turns", data)
	}
}

func TestStore_LoadNotFound(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	addr := mustAddr(t, "ceo", "acme")
	_, err = s.Load(addr, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent task, got nil")
	}
}
