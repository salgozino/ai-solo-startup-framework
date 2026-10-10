package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
	"github.com/salgozino/ai-solo-startup-framework/ui"
)

type verdictRecorder struct {
	a2asrv.RequestHandler
	requests  []*sdka2a.SendMessageRequest
	submitted chan *sdka2a.SendMessageRequest
}

func (h *verdictRecorder) SendMessage(_ context.Context, req *sdka2a.SendMessageRequest) (sdka2a.SendMessageResult, error) {
	if h.submitted != nil {
		h.submitted <- req
		return nil, nil
	}
	h.requests = append(h.requests, req)
	return nil, nil
}

func uiRuntimes(t *testing.T, names ...string) ([]*agentRuntime, []*verdictRecorder) {
	t.Helper()
	return uiRuntimesWith(t, nil, names...)
}

// uiRuntimesWith seeds extra[name] records into that agent's store next to the
// default shared-id escalation.
func uiRuntimesWith(t *testing.T, extra map[string][]supervisor.TaskRecord, names ...string) ([]*agentRuntime, []*verdictRecorder) {
	t.Helper()
	cfg := &config.CompanyConfig{Tenant: "acme", RiskPolicy: map[string]config.Policy{}}
	for _, name := range names {
		cfg.Agents = append(cfg.Agents, config.AgentConfig{Name: name, Role: name, Provider: "claude-code"})
	}
	dir := t.TempDir()
	runtimes, err := materializeAgents(cfg, wireOptions{providerOverride: &fake.Provider{}, gatewayOverride: &fake.Gateway{}, storeDir: dir, stderr: &bytes.Buffer{}, authTokenOverride: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, rt := range runtimes {
			_ = rt.srv.Shutdown(context.Background())
		}
		_ = runtimes[0].mcpSrv.Shutdown(context.Background())
	})
	handlers := make([]*verdictRecorder, len(runtimes))
	for i, rt := range runtimes {
		store, err := supervisor.NewStore(filepath.Join(dir, names[i]))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(rt.sup.Status().Addr, supervisor.TaskRecord{TaskID: "shared-id", State: string(sdka2a.TaskStateInputRequired), Input: names[i] + " task"}); err != nil {
			t.Fatal(err)
		}
		for _, rec := range extra[names[i]] {
			if err := store.Save(rt.sup.Status().Addr, rec); err != nil {
				t.Fatal(err)
			}
		}
		handlers[i] = &verdictRecorder{}
		rt.uiAdap.handler = handlers[i]
	}
	return runtimes, handlers
}

func TestCompanyUI_NonCEOTaskListedAndVerdictRouted(t *testing.T) {
	runtimes, handlers := uiRuntimes(t, "ceo", "engineer")
	h := newCompanyUIHandler(runtimes)
	defer h.Shutdown()
	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/tasks", nil))
	var payload struct {
		Tasks []ui.TaskRecord `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Tasks) != 2 {
		t.Fatalf("listed %d tasks, want CEO and engineer", len(payload.Tasks))
	}
	for i, task := range payload.Tasks {
		wantAgent := []string{"ceo", "engineer"}[i]
		if task.Agent != wantAgent || task.TaskID != "shared-id" {
			t.Fatalf("task = %+v, want agent %s and original task ID", task, wantAgent)
		}
	}
	if payload.Tasks[0].VerdictID == payload.Tasks[1].VerdictID {
		t.Fatal("colliding local IDs must have distinct verdict IDs")
	}
	for _, action := range []string{"approve", "reject"} {
		body, _ := json.Marshal(map[string]string{"task_id": payload.Tasks[1].VerdictID})
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/"+action, bytes.NewReader(body)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", action, w.Code, w.Body.String())
		}
	}
	if len(handlers[0].requests) != 0 {
		t.Fatal("engineer verdict reached CEO handler")
	}
	if len(handlers[1].requests) != 2 {
		t.Fatalf("engineer got %d verdicts", len(handlers[1].requests))
	}
	for i, req := range handlers[1].requests {
		if string(req.Message.TaskID) != "shared-id" || req.Tenant != "acme" {
			t.Fatalf("wrong resume request: %+v", req)
		}
		text := req.Message.Parts[0].Text()
		if text != []string{"approve", "reject"}[i] {
			t.Fatalf("verdict body = %q", text)
		}
	}
	for _, key := range []string{"shared-id", "unknown", payload.Tasks[1].VerdictID + "tampered"} {
		w = httptest.NewRecorder()
		body, _ := json.Marshal(map[string]string{"task_id": key})
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/approve", bytes.NewReader(body)))
		if w.Code != http.StatusConflict {
			t.Fatalf("unsafe/unknown key %q: %d, want 409", key, w.Code)
		}
	}
	if len(handlers[0].requests) != 0 || len(handlers[1].requests) != 2 {
		t.Fatal("invalid verdict was dispatched")
	}
	// A new human task still belongs to the CEO, not the peer being viewed.
	handlers[0].submitted = make(chan *sdka2a.SendMessageRequest, 1)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/send", strings.NewReader(`{"message":"plan next round"}`)))
	if w.Code != http.StatusAccepted {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	select {
	case req := <-handlers[0].submitted:
		if req.Message.TaskID != "" || req.Message.Parts[0].Text() != "plan next round" {
			t.Fatalf("wrong CEO submission: %+v", req)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new task did not reach CEO")
	}
	if len(handlers[1].requests) != 2 {
		t.Fatal("new task reached engineer")
	}
}

func TestCompanyUI_SingleAgentKeepsRawVerdictIDAndCEOStatus(t *testing.T) {
	runtimes, handlers := uiRuntimes(t, "ceo")
	a := &companyUIAdapter{runtimes: runtimes}
	tasks, err := a.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].VerdictID != "shared-id" {
		t.Fatalf("single-agent verdict ID = %q", tasks[0].VerdictID)
	}
	if a.StatusStr() != runtimes[0].uiAdap.StatusStr() {
		t.Fatal("status no longer belongs to CEO")
	}
	if err := a.PostVerdict("shared-id", true); err != nil {
		t.Fatal(err)
	}
	if len(handlers[0].requests) != 1 {
		t.Fatal("raw single-agent verdict not delivered")
	}
}

func TestCompanyUI_EveryRuntimePushesAggregateSSE(t *testing.T) {
	runtimes, _ := uiRuntimes(t, "ceo", "engineer")
	h := newCompanyUIHandler(runtimes)
	defer h.Shutdown()
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	for _, rt := range runtimes {
		rt.sup.MarkReady() // Public state-change hook; no timing sleeps required.
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var snapshot struct {
				Supervisor string          `json:"supervisor"`
				Tasks      []ui.TaskRecord `json:"tasks"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.Supervisor != runtimes[0].sup.StatusStr() || len(snapshot.Tasks) != 2 || snapshot.Tasks[1].Agent != "engineer" {
				t.Fatalf("incomplete SSE snapshot: %+v", snapshot)
			}
			break
		}
	}
}

// Slice 8 (T17/T18): a CEO task parked on a peer's human approval is exposed as
// such, so the UI offers no approve/reject on it; the peer's own escalated task is
// the one actionable item.
func TestCompanyUI_AwaitingPeerApprovalIsExposed(t *testing.T) {
	runtimes, _ := uiRuntimesWith(t, map[string][]supervisor.TaskRecord{"ceo": {{
		TaskID: "ceo-parked", State: string(sdka2a.TaskStateInputRequired), Input: "go",
		AwaitingPeerRole: "engineer", AwaitingPeerTaskID: "P1", AwaitingPeerApproval: true,
	}}}, "ceo", "engineer")
	h := newCompanyUIHandler(runtimes)
	defer h.Shutdown()

	mux := http.NewServeMux()
	h.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/tasks", nil))
	var payload struct {
		Tasks []map[string]any `json:"tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	var parked, escalated map[string]any
	for _, task := range payload.Tasks {
		switch {
		case task["task_id"] == "ceo-parked":
			parked = task
		case task["agent"] == "engineer":
			escalated = task
		}
	}
	if parked == nil || parked["awaiting_peer"] != "engineer" || parked["awaiting_peer_approval"] != true {
		t.Fatalf("parked CEO task = %v, want awaiting_peer=engineer and awaiting_peer_approval=true", parked)
	}
	if escalated == nil {
		t.Fatal("engineer's escalated task is not listed")
	}
	if _, ok := escalated["awaiting_peer"]; ok {
		t.Errorf("a plain escalation must not carry awaiting_peer: %v", escalated)
	}
}

// Compat: the pre-Slice-8 UI JSON still decodes, and the new fields stay out of it.
func TestUITaskRecord_PreChangeJSONCompat(t *testing.T) {
	var rec ui.TaskRecord
	if err := json.Unmarshal([]byte(`{"agent":"ceo","task_id":"t","state":"TASK_STATE_INPUT_REQUIRED","input":"x","pending_intent_kind":"k"}`), &rec); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(rec)
	if strings.Contains(string(out), "awaiting_peer") {
		t.Errorf("omitempty must keep awaiting_peer* out of %s", out)
	}
}
