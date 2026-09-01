package ui_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/salgozino/ai-solo-startup-framework/ui"
)

// stubSupervisor implements ui.Supervisor for tests.
type stubSupervisor struct {
	state       string
	tasks       []ui.TaskRecord
	verdErr     error // if non-nil, PostVerdict returns this error
	sendErr     error // if non-nil, SendTask returns this error
	sendCalled  bool  // set to true after SendTask is called
}

func (s *stubSupervisor) StatusStr() string { return s.state }

func (s *stubSupervisor) ListTasks() ([]ui.TaskRecord, error) {
	return s.tasks, nil
}

func (s *stubSupervisor) PostVerdict(taskID string, approve bool) error {
	return s.verdErr
}

func (s *stubSupervisor) SendTask(text string) error {
	s.sendCalled = true
	return s.sendErr
}

// buildHandler wires a UIHandler into an httptest server and returns the server.
func buildHandler(sup ui.Supervisor) *httptest.Server {
	mux := http.NewServeMux()
	h := ui.NewUIHandler(sup)
	h.Register(mux)
	return httptest.NewServer(mux)
}

// TestGetRoot checks that GET / returns 200 and HTML content.
func TestGetRoot(t *testing.T) {
	sup := &stubSupervisor{state: "IDLE"}
	srv := buildHandler(sup)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / want 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("GET / want text/html content-type, got %q", ct)
	}
}

// TestApproveNonInputRequired checks that POST /api/approve on a non-INPUT_REQUIRED
// task returns 409 (satisfies spec: "completed task offers no approve/reject control").
func TestApproveNonInputRequired(t *testing.T) {
	cases := []struct {
		name  string
		state string
	}{
		{"completed", "TASK_STATE_COMPLETED"},
		{"working", "TASK_STATE_WORKING"},
		{"failed", "TASK_STATE_FAILED"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sup := &stubSupervisor{
				tasks:   []ui.TaskRecord{{TaskID: "t1", State: tc.state}},
				verdErr: ui.ErrNotInputRequired,
			}
			srv := buildHandler(sup)
			defer srv.Close()

			body := bytes.NewBufferString(`{"task_id":"t1"}`)
			resp, err := http.Post(srv.URL+"/api/approve", "application/json", body)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusConflict {
				t.Fatalf("approve on %s: want 409, got %d", tc.name, resp.StatusCode)
			}
		})
	}
}

// TestApproveInputRequired checks that POST /api/approve on an INPUT_REQUIRED task
// returns 200 (satisfies spec: "pending escalation offers approve/reject").
func TestApproveInputRequired(t *testing.T) {
	sup := &stubSupervisor{
		tasks: []ui.TaskRecord{{TaskID: "t2", State: "TASK_STATE_INPUT_REQUIRED"}},
		// verdErr nil → PostVerdict succeeds
	}
	srv := buildHandler(sup)
	defer srv.Close()

	body := bytes.NewBufferString(`{"task_id":"t2"}`)
	resp, err := http.Post(srv.URL+"/api/approve", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("approve on INPUT_REQUIRED: want 200, got %d", resp.StatusCode)
	}
}

// TestRejectInputRequired checks that POST /api/reject on an INPUT_REQUIRED task returns 200.
func TestRejectInputRequired(t *testing.T) {
	sup := &stubSupervisor{
		tasks: []ui.TaskRecord{{TaskID: "t3", State: "TASK_STATE_INPUT_REQUIRED"}},
	}
	srv := buildHandler(sup)
	defer srv.Close()

	body := bytes.NewBufferString(`{"task_id":"t3"}`)
	resp, err := http.Post(srv.URL+"/api/reject", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reject on INPUT_REQUIRED: want 200, got %d", resp.StatusCode)
	}
}

// TestGetTasks checks that /api/tasks returns the task list as JSON.
func TestGetTasks(t *testing.T) {
	sup := &stubSupervisor{
		tasks: []ui.TaskRecord{
			{TaskID: "t1", State: "TASK_STATE_WORKING", Input: "do something"},
		},
	}
	srv := buildHandler(sup)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/tasks want 200, got %d", resp.StatusCode)
	}

	var payload struct {
		Tasks []ui.TaskRecord `json:"tasks"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal("decode tasks response:", err)
	}
	if len(payload.Tasks) != 1 || payload.Tasks[0].TaskID != "t1" {
		t.Fatalf("unexpected tasks: %+v", payload.Tasks)
	}
}

// TestSSEReceivesEvent verifies that a client connected to /api/events receives a
// Broadcast event without polling (satisfies spec: "escalation appears live without refresh").
func TestSSEReceivesEvent(t *testing.T) {
	sup := &stubSupervisor{state: "IDLE"}
	mux := http.NewServeMux()
	h := ui.NewUIHandler(sup)
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Open SSE connection.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE connect: want 200, got %d", resp.StatusCode)
	}

	// Channel to receive the first 'state' event data.
	received := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				received <- strings.TrimPrefix(line, "data: ")
				return
			}
		}
	}()

	// Give the client goroutine time to register.
	time.Sleep(20 * time.Millisecond)

	// Inject a state-change event via Broadcast — no polling needed.
	h.Broadcast(map[string]any{
		"supervisor": "WORKING",
		"tasks": []map[string]any{
			{"task_id": "t99", "state": "TASK_STATE_INPUT_REQUIRED"},
		},
	})

	select {
	case data := <-received:
		var payload map[string]any
		if err := json.Unmarshal([]byte(data), &payload); err != nil {
			t.Fatal("unmarshal SSE data:", err)
		}
		if payload["supervisor"] != "WORKING" {
			t.Fatalf("SSE event: want supervisor=WORKING, got %v", payload["supervisor"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout: SSE event not received")
	}
}

// TestSendTaskSuccess checks that POST /api/send with a valid message returns 200.
func TestSendTaskSuccess(t *testing.T) {
	sup := &stubSupervisor{}
	srv := buildHandler(sup)
	defer srv.Close()

	body := bytes.NewBufferString(`{"message":"review the quarterly report"}`)
	resp, err := http.Post(srv.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("send valid message: want 200, got %d", resp.StatusCode)
	}
	if !sup.sendCalled {
		t.Fatal("SendTask was not called on supervisor")
	}

	var payload struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal("decode send response:", err)
	}
	if !payload.OK {
		t.Fatal("send response: want ok=true")
	}
}

// TestSendTaskEmptyMessage checks that POST /api/send with an empty message returns 400.
func TestSendTaskEmptyMessage(t *testing.T) {
	sup := &stubSupervisor{}
	srv := buildHandler(sup)
	defer srv.Close()

	body := bytes.NewBufferString(`{"message":""}`)
	resp, err := http.Post(srv.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("send empty message: want 400, got %d", resp.StatusCode)
	}
	if sup.sendCalled {
		t.Fatal("SendTask should not be called for empty message")
	}
}

// TestSendTaskFailedState checks that POST /api/send returns 409 when the
// supervisor adapter rejects the send because the last task is FAILED.
func TestSendTaskFailedState(t *testing.T) {
	sup := &stubSupervisor{
		sendErr: ui.ErrAgentFailed,
	}
	srv := buildHandler(sup)
	defer srv.Close()

	body := bytes.NewBufferString(`{"message":"new task"}`)
	resp, err := http.Post(srv.URL+"/api/send", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("send with failed state: want 409, got %d", resp.StatusCode)
	}
}
