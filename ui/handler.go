package ui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// taskStateInputRequired is the A2A wire value for tasks awaiting human approval.
const taskStateInputRequired = "TASK_STATE_INPUT_REQUIRED"

// TaskRecord is the minimal view the UI handler needs of a persisted task.
// It maps to supervisor.TaskRecord without importing the supervisor package.
type TaskRecord struct {
	TaskID            string `json:"task_id"`
	State             string `json:"state"`
	Input             string `json:"input"`
	PendingIntentKind string `json:"pending_intent_kind,omitempty"`
}

// SupervisorStatus is the minimal view of the supervisor's observable state.
type SupervisorStatus struct {
	// State is the supervisor lifecycle state string (e.g. "IDLE", "WORKING").
	State string
}

// Supervisor is the interface the UIHandler uses to read live state.
// supervisor.Supervisor satisfies this interface directly.
type Supervisor interface {
	// StatusStr returns the current supervisor lifecycle state as a string.
	StatusStr() string
	// ListTasks returns all persisted task records.
	ListTasks() ([]TaskRecord, error)
	// PostVerdict sends an approve or reject verdict for taskID.
	// Returns an error if the task is not in INPUT_REQUIRED state.
	PostVerdict(taskID string, approve bool) error
	// SendTask submits a new task with the given message text.
	SendTask(text string) error
}

// sseClient holds the channel for a single connected SSE client.
type sseClient struct {
	ch chan string
}

// UIHandler serves the embedded monitoring UI and exposes REST + SSE endpoints.
//
// Routes:
//   GET  /            → serves index.html from the embedded FS
//   GET  /style.css   → served by the file server (static)
//   GET  /app.js      → served by the file server (static)
//   GET  /api/tasks   → JSON list of current task records
//   GET  /api/events  → SSE stream of state-change events
//   POST /api/approve → approve an INPUT_REQUIRED task (409 otherwise)
//   POST /api/reject  → reject an INPUT_REQUIRED task (409 otherwise)
//   POST /api/send    → submit a new task with a message
type UIHandler struct {
	sup Supervisor

	mu      sync.Mutex
	clients []*sseClient
}

// NewUIHandler creates a UIHandler backed by sup.
func NewUIHandler(sup Supervisor) *UIHandler {
	return &UIHandler{sup: sup}
}

// Register wires the handler's routes into mux.
func (h *UIHandler) Register(mux *http.ServeMux) {
	// Static files served from the embedded FS.
	fileServer := http.FileServer(http.FS(FS))
	mux.Handle("/", fileServer)

	mux.HandleFunc("/api/tasks", h.handleTasks)
	mux.HandleFunc("/api/events", h.handleEvents)
	mux.HandleFunc("/api/approve", h.handleApprove)
	mux.HandleFunc("/api/reject", h.handleReject)
	mux.HandleFunc("/api/send", h.handleSendTask)
}

// Broadcast sends a JSON-encoded SSE 'state' event to all connected clients.
// It is safe to call from any goroutine.
func (h *UIHandler) Broadcast(data any) {
	b, err := json.Marshal(data)
	if err != nil {
		return
	}
	msg := "event: state\ndata: " + string(b) + "\n\n"

	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		select {
		case c.ch <- msg:
		default: // slow client — drop rather than block
		}
	}
}

// handleTasks handles GET /api/tasks.
func (h *UIHandler) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	tasks, err := h.sup.ListTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tasks": tasks})
}

// handleEvents handles GET /api/events — SSE stream.
func (h *UIHandler) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	client := &sseClient{ch: make(chan string, 8)}
	h.mu.Lock()
	h.clients = append(h.clients, client)
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		filtered := h.clients[:0]
		for _, c := range h.clients {
			if c != client {
				filtered = append(filtered, c)
			}
		}
		h.clients = filtered
		h.mu.Unlock()
	}()

	// Send an initial ping to signal the connection is live.
	_, _ = fmt.Fprintf(w, ": ping\n\n")
	flusher.Flush()

	// Keep-alive ticker so proxies do not time out.
	tick := time.NewTicker(25 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case msg := <-client.ch:
			_, _ = fmt.Fprint(w, msg)
			flusher.Flush()
		}
	}
}

// ErrEmptyMessage is returned by SendTask when the message is empty.
var ErrEmptyMessage = fmt.Errorf("message must not be empty")

// ErrAgentFailed is returned by SendTask when the agent's last task is in FAILED state.
var ErrAgentFailed = fmt.Errorf("agent is in FAILED state")

// verdictRequest is the JSON body for /api/approve and /api/reject.
type verdictRequest struct {
	TaskID string `json:"task_id"`
}

// sendRequest is the JSON body for /api/send.
type sendRequest struct {
	Message string `json:"message"`
}

// handleApprove handles POST /api/approve.
func (h *UIHandler) handleApprove(w http.ResponseWriter, r *http.Request) {
	h.handleVerdict(w, r, true)
}

// handleReject handles POST /api/reject.
func (h *UIHandler) handleReject(w http.ResponseWriter, r *http.Request) {
	h.handleVerdict(w, r, false)
}

func (h *UIHandler) handleVerdict(w http.ResponseWriter, r *http.Request, approve bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req verdictRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TaskID == "" {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := h.sup.PostVerdict(req.TaskID, approve); err != nil {
		if isNotInputRequired(err) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleSendTask handles POST /api/send.
func (h *UIHandler) handleSendTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, ErrEmptyMessage.Error(), http.StatusBadRequest)
		return
	}
	if err := h.sup.SendTask(req.Message); err != nil {
		if isAgentFailed(err) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// ErrNotInputRequired is returned by PostVerdict when the task is not in INPUT_REQUIRED state.
var ErrNotInputRequired = fmt.Errorf("task is not in INPUT_REQUIRED state")

func isNotInputRequired(err error) bool {
	return err != nil && err.Error() == ErrNotInputRequired.Error()
}

func isAgentFailed(err error) bool {
	return err != nil && err.Error() == ErrAgentFailed.Error()
}
