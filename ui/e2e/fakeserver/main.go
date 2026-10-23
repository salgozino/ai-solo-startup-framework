// Command fakeserver serves the real monitoring UI (ui.UIHandler, embedded static
// files) backed by an in-memory ui.Supervisor with a deterministic seed. It exists
// only for the Playwright suite in ui/e2e: no provider CLI, no network.
package main

import (
	"flag"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/salgozino/ai-solo-startup-framework/ui"
)

const (
	stateWorking       = "TASK_STATE_WORKING"
	stateCompleted     = "TASK_STATE_COMPLETED"
	stateInputRequired = "TASK_STATE_INPUT_REQUIRED"
	stateRejected      = "TASK_STATE_REJECTED"
	stateFailed        = "TASK_STATE_FAILED"
	stateCanceled      = "TASK_STATE_CANCELED"

	ceo      = "ceo"
	engineer = "engineer"
)

// fake is an in-memory ui.Supervisor. Tasks are kept oldest first, like production.
type fake struct {
	mu      sync.Mutex
	tasks   []ui.TaskRecord
	nextID  int
	handler *ui.UIHandler
}

func newFake() *fake {
	f := &fake{}
	f.seed()
	return f
}

// seed restores the deterministic scenario. Callers hold f.mu or own f exclusively.
func (f *fake) seed() {
	f.nextID = 100
	f.tasks = []ui.TaskRecord{
		{Agent: engineer, TaskID: "eng-done", State: stateCompleted,
			Input: "Implement the login page", Output: "Login page implemented"},
		{Agent: ceo, TaskID: "ceo-done", State: stateCompleted,
			Input: "Write the launch announcement", Output: "Announcement drafted"},
		{Agent: ceo, TaskID: "ceo-input", State: stateInputRequired,
			Input: "Deploy the website to production", PendingIntentKind: "deploy"},
		{Agent: ceo, TaskID: "ceo-delegating", State: stateWorking,
			Input: "Build the login feature", AwaitingPeer: engineer,
			Delegations: []ui.Delegation{{Role: engineer, TaskID: "eng-done"}}},
		{Agent: ceo, TaskID: "ceo-working", State: stateWorking,
			Input: "Research competitors"},
	}
}

func terminal(state string) bool {
	switch state {
	case stateCompleted, stateFailed, stateRejected, stateCanceled:
		return true
	}
	return false
}

// view returns a copy with the server-decided can_follow_up flag filled in:
// terminal tasks of the CEO only, as in production.
func (f *fake) view() []ui.TaskRecord {
	out := make([]ui.TaskRecord, len(f.tasks))
	for i, t := range f.tasks {
		t.CanFollowUp = t.Agent == ceo && terminal(t.State)
		out[i] = t
	}
	return out
}

func (f *fake) StatusStr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statusLocked()
}

func (f *fake) statusLocked() string {
	for _, t := range f.tasks {
		if t.State == stateWorking {
			return "WORKING"
		}
	}
	return "IDLE"
}

func (f *fake) ListTasks() ([]ui.TaskRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.view(), nil
}

func (f *fake) newIDLocked() string {
	f.nextID++
	return "ceo-" + strconv.Itoa(f.nextID)
}

func (f *fake) SendTask(text string) error {
	f.mu.Lock()
	f.tasks = append(f.tasks, ui.TaskRecord{
		Agent: ceo, TaskID: f.newIDLocked(), State: stateCompleted,
		Input: text, Output: "Done: " + text,
	})
	f.mu.Unlock()
	f.broadcast()
	return nil
}

func (f *fake) PostVerdict(taskID string, approve bool) error {
	f.mu.Lock()
	found := false
	for i := range f.tasks {
		t := &f.tasks[i]
		if t.TaskID != taskID || t.State != stateInputRequired {
			continue
		}
		found = true
		t.PendingIntentKind = ""
		if approve {
			t.State = stateWorking
		} else {
			t.State = stateRejected
		}
		break
	}
	f.mu.Unlock()
	if !found {
		return ui.ErrNotInputRequired
	}
	f.broadcast()
	return nil
}

func (f *fake) FollowUp(taskID, text string) error {
	f.mu.Lock()
	var target *ui.TaskRecord
	for i := range f.tasks {
		if f.tasks[i].TaskID == taskID && f.tasks[i].Agent == ceo {
			target = &f.tasks[i]
			break
		}
	}
	if target == nil {
		f.mu.Unlock()
		return ui.ErrTaskNotFound
	}
	if !terminal(target.State) {
		f.mu.Unlock()
		return ui.ErrNotTerminal
	}
	f.tasks = append(f.tasks, ui.TaskRecord{
		Agent: ceo, TaskID: f.newIDLocked(), State: stateCompleted,
		Input: text, Output: "Changes applied: " + text, FollowUpOf: taskID,
	})
	f.mu.Unlock()
	f.broadcast()
	return nil
}

// reset restores the seed.
func (f *fake) reset() {
	f.mu.Lock()
	f.seed()
	f.mu.Unlock()
	f.broadcast()
}

// setState changes a task's state without broadcasting, so a browser that already
// rendered the task keeps a stale view (used to make a follow-up refusable).
func (f *fake) setState(taskID, state string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.tasks {
		if f.tasks[i].TaskID == taskID {
			f.tasks[i].State = state
			return true
		}
	}
	return false
}

// broadcast pushes the same snapshot shape production sends over SSE.
func (f *fake) broadcast() {
	if f.handler == nil {
		return
	}
	f.mu.Lock()
	snap := map[string]any{"supervisor": f.statusLocked(), "tasks": f.view()}
	f.mu.Unlock()
	f.handler.Broadcast(snap)
}

func (f *fake) routes() *http.ServeMux {
	mux := http.NewServeMux()
	f.handler = ui.NewUIHandler(f)
	f.handler.Register(mux)
	mux.HandleFunc("/test/reset", post(func(http.ResponseWriter, *http.Request) { f.reset() }))
	mux.HandleFunc("/test/broadcast", post(func(http.ResponseWriter, *http.Request) { f.broadcast() }))
	mux.HandleFunc("/test/set-state", post(func(w http.ResponseWriter, r *http.Request) {
		if !f.setState(r.URL.Query().Get("task"), r.URL.Query().Get("state")) {
			http.Error(w, "unknown task", http.StatusNotFound)
		}
	}))
	return mux
}

func post(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fn(w, r)
	}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "listen address")
	flag.Parse()
	log.Printf("fakeserver listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, newFake().routes()))
}
