package port

import "context"

// KindDelegateTask is the action-kind string for delegation. Single source of truth
// shared by the MCP tool schema (which must require a target argument for it) and the
// supervisor's routing switch — a rename is a compile error, not a silent drift.
const KindDelegateTask = "delegate_task"

// TargetArg is the ActionIntent payload key carrying the delegation's target ROLE.
const TargetArg = "target"

// DelegationResult is the observed state of a peer task.
type DelegationResult struct {
	// PeerTaskID is the peer's A2A task ID. Always set on a nil-error return,
	// including when the peer parked in INPUT_REQUIRED.
	PeerTaskID string
	// State is the peer task's A2A TaskState string as of this call
	// (e.g. "TASK_STATE_COMPLETED", "TASK_STATE_INPUT_REQUIRED").
	State string
	// Output is the peer's terminal text. Empty unless State is COMPLETED.
	Output string
}

// Delegator sends work to a role-addressed peer agent over A2A.
// Implementations live in transport/a2a; core/ never imports an A2A client.
type Delegator interface {
	// Delegate sends body to the peer fulfilling role and blocks until that peer's
	// task stops advancing — either a terminal TaskState, or INPUT_REQUIRED (the peer
	// escalated and awaits its own human verdict). Any other non-terminal state is a
	// protocol violation and MUST be returned as an error, never as a result.
	Delegate(ctx context.Context, role, body string) (DelegationResult, error)

	// PeerTaskState reports the current state and terminal output of a peer task
	// previously returned by Delegate. Used both to await a parked peer and to
	// re-establish that wait after a process restart.
	PeerTaskState(ctx context.Context, role, peerTaskID string) (DelegationResult, error)
}
