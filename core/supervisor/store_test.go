package supervisor

import (
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
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
