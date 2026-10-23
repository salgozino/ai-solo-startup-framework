package main

import (
	"errors"
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/ui"
)

func find(t *testing.T, f *fake, id string) ui.TaskRecord {
	t.Helper()
	tasks, _ := f.ListTasks()
	for _, tr := range tasks {
		if tr.TaskID == id {
			return tr
		}
	}
	t.Fatalf("task %s not found", id)
	return ui.TaskRecord{}
}

func TestCanFollowUpOnlyOnTerminalCEOTasks(t *testing.T) {
	f := newFake()
	tasks, _ := f.ListTasks()
	for _, tr := range tasks {
		want := tr.TaskID == "ceo-done"
		if tr.CanFollowUp != want {
			t.Errorf("%s: can_follow_up=%v, want %v", tr.TaskID, tr.CanFollowUp, want)
		}
	}
}

func TestPostVerdict(t *testing.T) {
	f := newFake()
	if err := f.PostVerdict("ceo-input", false); err != nil {
		t.Fatal(err)
	}
	if got := find(t, f, "ceo-input").State; got != stateRejected {
		t.Errorf("state=%s, want rejected", got)
	}
	if err := f.PostVerdict("ceo-input", true); !errors.Is(err, ui.ErrNotInputRequired) {
		t.Errorf("second verdict err=%v, want ErrNotInputRequired", err)
	}
}

func TestFollowUpRules(t *testing.T) {
	f := newFake()
	if err := f.FollowUp("nope", "x"); !errors.Is(err, ui.ErrTaskNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if err := f.FollowUp("eng-done", "x"); !errors.Is(err, ui.ErrTaskNotFound) {
		t.Errorf("non-CEO: %v", err)
	}
	if err := f.FollowUp("ceo-working", "x"); !errors.Is(err, ui.ErrNotTerminal) {
		t.Errorf("working: %v", err)
	}
	if err := f.FollowUp("ceo-done", "tweak"); err != nil {
		t.Fatal(err)
	}
	tasks, _ := f.ListTasks()
	last := tasks[len(tasks)-1]
	if last.FollowUpOf != "ceo-done" || last.Input != "tweak" {
		t.Errorf("follow-up not appended correctly: %+v", last)
	}
}

func TestResetRestoresSeed(t *testing.T) {
	f := newFake()
	before, _ := f.ListTasks()
	_ = f.SendTask("extra")
	f.reset()
	after, _ := f.ListTasks()
	if len(before) != len(after) {
		t.Errorf("len %d != %d", len(after), len(before))
	}
}
