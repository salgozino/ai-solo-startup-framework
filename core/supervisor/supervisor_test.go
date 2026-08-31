package supervisor

import (
	"testing"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// fsm lifecycle tests

func TestFSM_StartingToIdle(t *testing.T) {
	f := newFSM()
	if f.current() != StateStarting {
		t.Fatalf("expected STARTING, got %s", f.current())
	}
	f.ready()
	if f.current() != StateIdle {
		t.Fatalf("expected IDLE after ready(), got %s", f.current())
	}
}

func TestFSM_IdleToWorkingToIdle(t *testing.T) {
	f := newFSM()
	f.ready()
	f.taskStarted()
	if f.current() != StateWorking {
		t.Fatalf("expected WORKING after taskStarted(), got %s", f.current())
	}
	f.taskDone()
	if f.current() != StateIdle {
		t.Fatalf("expected IDLE after taskDone(), got %s", f.current())
	}
}

func TestFSM_MultipleWorkers(t *testing.T) {
	f := newFSM()
	f.ready()
	f.taskStarted()
	f.taskStarted()
	f.taskDone()
	if f.current() != StateWorking {
		t.Errorf("expected WORKING with 1 remaining worker, got %s", f.current())
	}
	f.taskDone()
	if f.current() != StateIdle {
		t.Errorf("expected IDLE with 0 workers, got %s", f.current())
	}
}

func TestFSM_DrainFromIdle(t *testing.T) {
	f := newFSM()
	f.ready()
	f.drain()
	// No workers → straight to STOPPED.
	if f.current() != StateStopped {
		t.Fatalf("expected STOPPED after drain() from IDLE, got %s", f.current())
	}
}

func TestFSM_DrainFromWorking(t *testing.T) {
	f := newFSM()
	f.ready()
	f.taskStarted()
	f.drain()
	if f.current() != StateDraining {
		t.Fatalf("expected DRAINING, got %s", f.current())
	}
	f.taskDone()
	if f.current() != StateStopped {
		t.Fatalf("expected STOPPED after last taskDone() in DRAINING, got %s", f.current())
	}
}

func TestFSM_RecoveringToIdle(t *testing.T) {
	f := newFSM()
	f.ready()          // STARTING → IDLE
	f.recover()        // IDLE → RECOVERING
	if f.current() != StateRecovering {
		t.Fatalf("expected RECOVERING, got %s", f.current())
	}
	f.ready()          // RECOVERING → IDLE
	if f.current() != StateIdle {
		t.Fatalf("expected IDLE after second ready(), got %s", f.current())
	}
}

// bounded-context tests

func TestAssembleBoundedContext_WithinBudget(t *testing.T) {
	msgs := []port.ContextMessage{
		{Role: "user", Content: "hello"},
		{Role: "agent", Content: "world"},
	}
	bc := assembleBoundedContext(msgs, 100)
	if bc.Truncated {
		t.Error("expected not truncated when within budget")
	}
	if len(bc.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(bc.Messages))
	}
}

func TestAssembleBoundedContext_OverBudget(t *testing.T) {
	msgs := []port.ContextMessage{
		{Role: "user", Content: "old message that is long and should be dropped"},
		{Role: "agent", Content: "recent"},
	}
	// Budget smaller than total content (52 chars), so truncation is triggered.
	// Use budget=10 so the long old message cannot fit alongside the marker.
	bc := assembleBoundedContext(msgs, 10)
	if !bc.Truncated {
		t.Error("expected Truncated=true when over budget")
	}
	if bc.TruncationMarker == "" {
		t.Error("expected non-empty TruncationMarker")
	}
	// First message should be the truncation marker.
	if len(bc.Messages) == 0 || bc.Messages[0].Content != truncationMarker {
		t.Errorf("expected first message to be truncation marker, got %+v", bc.Messages)
	}
}

func TestAssembleBoundedContext_NoBudget(t *testing.T) {
	msgs := []port.ContextMessage{
		{Role: "user", Content: "hello"},
	}
	bc := assembleBoundedContext(msgs, 0)
	if bc.Truncated {
		t.Error("expected not truncated when budget is 0 (unlimited)")
	}
	if len(bc.Messages) != 1 {
		t.Errorf("expected 1 message with unlimited budget, got %d", len(bc.Messages))
	}
}

func TestAssembleBoundedContext_EmptyMessages(t *testing.T) {
	bc := assembleBoundedContext(nil, 100)
	if bc.Truncated {
		t.Error("expected not truncated for empty messages")
	}
	if len(bc.Messages) != 0 {
		t.Errorf("expected 0 messages, got %d", len(bc.Messages))
	}
}

// task-state helpers

func TestFilterOpenTasks(t *testing.T) {
	tests := []struct {
		name    string
		records []TaskRecord
		want    int
	}{
		{
			name: "working task is open",
			records: []TaskRecord{
				{TaskID: "t1", State: "TASK_STATE_WORKING"},
			},
			want: 1,
		},
		{
			name: "completed task is not open",
			records: []TaskRecord{
				{TaskID: "t1", State: "TASK_STATE_COMPLETED"},
			},
			want: 0,
		},
		{
			name: "failed task is not open",
			records: []TaskRecord{
				{TaskID: "t1", State: "TASK_STATE_FAILED"},
			},
			want: 0,
		},
		{
			name: "input_required task is not open (parked, not re-submitted)",
			records: []TaskRecord{
				{TaskID: "t1", State: "TASK_STATE_INPUT_REQUIRED"},
			},
			want: 0,
		},
		{
			name: "mixed: only WORKING re-queued",
			records: []TaskRecord{
				{TaskID: "t1", State: "TASK_STATE_WORKING"},
				{TaskID: "t2", State: "TASK_STATE_COMPLETED"},
				{TaskID: "t3", State: "TASK_STATE_INPUT_REQUIRED"},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			open := filterOpenTasks(tt.records)
			if len(open) != tt.want {
				t.Errorf("filterOpenTasks: got %d open tasks, want %d", len(open), tt.want)
			}
		})
	}
}
