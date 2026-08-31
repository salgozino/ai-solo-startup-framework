// Package supervisor owns a single agent's identity, task queue, lifecycle state machine,
// and context assembly. It wraps the a2asrv handler and manages IDLE/WORKING transitions.
// Nothing outside this package observes internal FSM states directly; use Status().
package supervisor

import "sync"

// State is the supervisor lifecycle state (framework-owned, not A2A wire state).
type State string

const (
	// StateStarting is the initial state before the A2A endpoint is registered.
	StateStarting State = "STARTING"
	// StateIdle means the server is up, queue is empty, no provider running.
	StateIdle State = "IDLE"
	// StateWorking means at least one task is being processed by a provider.
	StateWorking State = "WORKING"
	// StateDraining means the supervisor is shutting down and draining its queue.
	StateDraining State = "DRAINING"
	// StateStopped is the terminal lifecycle state.
	StateStopped State = "STOPPED"
	// StateRecovering means the supervisor is replaying persisted open tasks after a restart.
	StateRecovering State = "RECOVERING"
)

// fsm is the supervisor lifecycle finite-state machine. It is internal; Status() exposes it.
type fsm struct {
	mu    sync.RWMutex
	state State
	// workers is the count of currently active task goroutines.
	workers int
}

func newFSM() *fsm {
	return &fsm{state: StateStarting}
}

// current returns the current state.
func (f *fsm) current() State {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.state
}

// ready transitions STARTING → IDLE (called after the endpoint is registered).
// Transition RECOVERING → IDLE (called after open tasks are replayed).
func (f *fsm) ready() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == StateStarting || f.state == StateRecovering {
		f.state = StateIdle
	}
}

// recover transitions IDLE → RECOVERING (called on startup when open tasks are found in the store).
func (f *fsm) recover() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == StateIdle {
		f.state = StateRecovering
	}
}

// taskStarted transitions IDLE/RECOVERING → WORKING.
func (f *fsm) taskStarted() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workers++
	if f.state == StateIdle || f.state == StateRecovering {
		f.state = StateWorking
	}
}

// taskDone decrements the active-worker counter and transitions back to IDLE when it reaches zero.
// If the supervisor is draining and the counter reaches zero, it transitions to STOPPED.
func (f *fsm) taskDone() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workers > 0 {
		f.workers--
	}
	if f.workers == 0 {
		switch f.state {
		case StateWorking:
			f.state = StateIdle
		case StateDraining:
			f.state = StateStopped
		}
	}
}

// drain transitions IDLE → DRAINING or WORKING → DRAINING (called on shutdown).
func (f *fsm) drain() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state == StateIdle {
		f.state = StateStopped // no workers — go straight to stopped
	} else if f.state == StateWorking {
		f.state = StateDraining
	}
}
