package a2a_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
	transa2a "github.com/salgozino/ai-solo-startup-framework/transport/a2a"
)

const testToken = "test-bearer-token"

// TestNew_EmptyToken_ReturnsError asserts that New() refuses to start when
// no auth token is configured (satisfies spec: "Server refuses to start without a token").
func TestNew_EmptyToken_ReturnsError(t *testing.T) {
	addr, err := address.New("ceo", "acme")
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}
	store, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sup := supervisor.New(supervisor.Config{
		Addr:     addr,
		Provider: &fake.Provider{ReturnTaskID: "task-1"},
		Store:    store,
	})
	_, newErr := transa2a.New(sup, "")
	if newErr == nil {
		t.Fatal("expected error from New() with empty token, got nil")
	}
}

func newTestSupervisor(t *testing.T, name, tenant string) (*supervisor.Supervisor, *transa2a.Server) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	addr, err := address.New(name, tenant)
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}

	storeDir := t.TempDir()
	store, err := supervisor.NewStore(storeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	fp := &fake.Provider{ReturnTaskID: "task-1"}
	sup := supervisor.New(supervisor.Config{
		Addr:     addr,
		Provider: fp,
		Store:    store,
	})

	srv, err := transa2a.New(sup, testToken)
	if err != nil {
		t.Fatalf("transport/a2a.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	return sup, srv
}

// TestAuthInterceptor_Before covers the authInterceptor.Before logic:
// nil ServiceParams (trusted in-process), valid bearer, missing header, wrong token, malformed prefix.
func TestAuthInterceptor_Before(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	cases := []struct {
		name    string
		direct  bool   // true = invoke srv.Handler() directly (nil ServiceParams, trusted caller)
		header  string // Authorization header for HTTP cases; empty = omit
		wantErr bool
	}{
		{name: "nil ServiceParams (trusted internal call)", direct: true, wantErr: false},
		{name: "valid bearer token", header: "Bearer " + testToken, wantErr: false},
		{name: "missing Authorization header", header: "", wantErr: true},
		{name: "wrong token", header: "Bearer wrong-token", wantErr: true},
		{name: "malformed prefix (no Bearer)", header: testToken, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.direct {
				msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("hello"))
				_, err := srv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{
					Tenant:  "acme",
					Message: msg,
				})
				if tc.wantErr && err == nil {
					t.Error("expected error, got success")
				}
				if !tc.wantErr && err != nil {
					t.Errorf("expected success, got error: %v", err)
				}
				return
			}

			body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"tenant":"acme","message":{"messageId":"msg-1","role":"ROLE_USER","parts":[{"text":"hello"}]}}}`
			req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("HTTP request: %v", err)
			}
			defer resp.Body.Close()

			var rpcResp struct {
				Error *struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
				t.Fatalf("decode response: %v", err)
			}

			if tc.wantErr && rpcResp.Error == nil {
				t.Errorf("expected JSON-RPC error, got success")
			}
			if !tc.wantErr && rpcResp.Error != nil {
				t.Errorf("expected success, got JSON-RPC error: %v", rpcResp.Error.Message)
			}
		})
	}
}

// TestUnauthenticated_RequestRejected asserts that a request with no
// Authorization header is rejected by authInterceptor before any handler
// runs (satisfies spec: "Missing Authorization header is rejected").
func TestUnauthenticated_RequestRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"tenant":"acme","message":{"messageId":"msg-1","role":"ROLE_USER","parts":[{"text":"hello"}]}}}`
	resp, err := http.Post( //nolint:noctx
		srv.BaseURL()+"/invoke",
		"application/json",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatal("expected JSON-RPC unauthorized error for missing Authorization header, got success")
	}
}

// TestAgentCardDiscoverable asserts that a newly started supervisor serves a
// valid Agent Card at the well-known path (satisfies "Newly started supervisor is discoverable").
func TestAgentCardDiscoverable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	url := srv.BaseURL() + a2asrv.WellKnownAgentCardPath
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET agent card: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var card sdka2a.AgentCard
	if err := json.NewDecoder(resp.Body).Decode(&card); err != nil {
		t.Fatalf("decode agent card: %v", err)
	}
	if card.Name == "" {
		t.Error("agent card Name is empty")
	}
	if len(card.SupportedInterfaces) == 0 {
		t.Error("agent card has no supported interfaces")
	}

	// satisfies spec: "Agent Card Declares httpBearer Security Scheme"
	if len(card.SecuritySchemes) == 0 {
		t.Fatal("agent card has no SecuritySchemes declared")
	}
	foundBearer := false
	for _, scheme := range card.SecuritySchemes {
		if httpScheme, ok := scheme.(sdka2a.HTTPAuthSecurityScheme); ok && httpScheme.Scheme == "bearer" {
			foundBearer = true
			break
		}
	}
	if !foundBearer {
		t.Errorf("agent card SecuritySchemes does not declare an httpBearer scheme: %+v", card.SecuritySchemes)
	}
	if len(card.SecurityRequirements) == 0 {
		t.Error("agent card has no SecurityRequirements declared")
	}
}

// TestEmptyTenantRejected asserts that a SendMessage request with an empty
// tenant is rejected before task processing begins
// (satisfies "Empty tenant rejected at the edge").
func TestEmptyTenantRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	// Build a JSON-RPC SendMessage request with tenant:"" (empty). The
	// Authorization header is valid so this exercises tenantInterceptor,
	// not authInterceptor.
	body := `{
		"jsonrpc":"2.0",
		"id":1,
		"method":"SendMessage",
		"params":{
			"tenant":"",
			"message":{
				"messageId":"msg-1",
				"role":"ROLE_USER",
				"parts":[{"text":"hello"}]
			}
		}
	}`

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	// The JSON-RPC response should be an error (interceptor rejected).
	var rpcResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatal("expected JSON-RPC error response for empty tenant, got success")
	}
}

// codeUnauthenticated is a2a-go's JSON-RPC error code for a2a.ErrUnauthenticated
// (see internal/jsonrpc.codeToError). codeInvalidParams is the code for
// a2a.ErrInvalidParams, which is what tenantInterceptor returns for an empty
// tenant. Distinguishing these two codes is what lets a test prove which
// interceptor rejected a request.
const (
	codeUnauthenticated = -31401
	codeInvalidParams   = -32602
)

// TestAuthPrecedesTenantValidation asserts that authInterceptor runs before
// tenantInterceptor: a request with both an invalid token AND an empty tenant
// must be rejected as unauthenticated (not as an invalid-tenant error),
// proving auth is checked first (satisfies spec: "Auth precedes tenant
// validation").
func TestAuthPrecedesTenantValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	body := `{
		"jsonrpc":"2.0",
		"id":1,
		"method":"SendMessage",
		"params":{
			"tenant":"",
			"message":{
				"messageId":"msg-1",
				"role":"ROLE_USER",
				"parts":[{"text":"hello"}]
			}
		}
	}`

	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer wrong-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpcResp.Error == nil {
		t.Fatal("expected JSON-RPC error for invalid token + empty tenant, got success")
	}
	if rpcResp.Error.Code == codeInvalidParams {
		t.Fatalf("got tenant-validation error (code %d) instead of unauthenticated (code %d) — "+
			"tenantInterceptor ran before authInterceptor rejected the invalid token",
			codeInvalidParams, codeUnauthenticated)
	}
	if rpcResp.Error.Code != codeUnauthenticated {
		t.Errorf("expected unauthenticated error code %d, got code %d: %s",
			codeUnauthenticated, rpcResp.Error.Code, rpcResp.Error.Message)
	}
}

// TestSupervisorStatusIdle verifies that the supervisor reports IDLE after startup.
func TestSupervisorStatusIdle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	sup, _ := newTestSupervisor(t, "worker", "acme")
	st := sup.Status()
	if st.State != supervisor.StateIdle {
		t.Errorf("expected IDLE after startup, got %s", st.State)
	}
}

// TestProviderFailureMarksFailed sends a message to a supervisor whose provider
// returns an error, and asserts the returned task is FAILED.
// (satisfies "Provider failure marks task FAILED, not silently dropped")
func TestProviderFailureMarksFailed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	addr, err := address.New("ceo", "acme")
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}

	storeDir := t.TempDir()
	store, err := supervisor.NewStore(storeDir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Provider whose ResolveAgent returns an error → supervisor marks FAILED.
	fp := &fake.Provider{ReturnErr: fmt.Errorf("provider down")}
	sup := supervisor.New(supervisor.Config{
		Addr:     addr,
		Provider: fp,
		Store:    store,
	})

	srv, err := transa2a.New(sup, testToken)
	if err != nil {
		t.Fatalf("transport/a2a.New: %v", err)
	}
	defer srv.Shutdown(context.Background()) //nolint:errcheck

	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("do something"))
	req := &sdka2a.SendMessageRequest{
		Tenant:  "acme",
		Message: msg,
	}
	result, err := srv.Handler().SendMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SendMessage returned unexpected error: %v", err)
	}

	task, ok := result.(*sdka2a.Task)
	if !ok {
		t.Fatalf("expected *a2a.Task result, got %T", result)
	}
	if task.Status.State != sdka2a.TaskStateFailed {
		t.Errorf("expected FAILED state, got %s", task.Status.State)
	}
}

// TestListTasks_OwnershipIsolation asserts that ListTasks returns only tasks
// owned by the authenticated caller and never another caller's tasks
// (satisfies spec: "Authenticated client lists only its own tasks").
//
// The shared-secret Bearer auth model in this codebase produces exactly two
// distinct caller identities end to end: "internal" (a trusted in-process
// call with nil ServiceParams — e.g. RecoverOpenTasks or a direct handler
// invocation) and "caller" (any HTTP request presenting the valid shared
// token; see authInterceptor.Before). There is no notion of distinct
// per-holder tokens in this design — everyone who has the one shared token
// is identified as "caller". This test exercises both identities that the
// system actually produces, through the real interceptor chain and
// taskstore, to prove the ownership-isolation guarantee holds.
func TestListTasks_OwnershipIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	// Task A: created via a direct handler call (nil ServiceParams → "internal").
	msgA := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("task A"))
	resultA, err := srv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{
		Tenant:  "acme",
		Message: msgA,
	})
	if err != nil {
		t.Fatalf("SendMessage (internal caller): %v", err)
	}
	taskA, ok := resultA.(*sdka2a.Task)
	if !ok {
		t.Fatalf("expected *a2a.Task result for task A, got %T", resultA)
	}

	// Task B: created via HTTP with the valid Bearer token ("caller").
	bodyB := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"tenant":"acme","message":{"messageId":"msg-b","role":"ROLE_USER","parts":[{"text":"task B"}]}}}`
	reqB, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(bodyB)) //nolint:noctx
	reqB.Header.Set("Content-Type", "application/json")
	reqB.Header.Set("Authorization", "Bearer "+testToken)
	respB, err := http.DefaultClient.Do(reqB)
	if err != nil {
		t.Fatalf("POST SendMessage (caller): %v", err)
	}
	defer respB.Body.Close()

	var rpcRespB struct {
		Result *struct {
			Task *sdka2a.Task `json:"task"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(respB.Body).Decode(&rpcRespB); err != nil {
		t.Fatalf("decode SendMessage response (caller): %v", err)
	}
	if rpcRespB.Error != nil {
		t.Fatalf("SendMessage (caller) failed: %s", rpcRespB.Error.Message)
	}
	if rpcRespB.Result == nil || rpcRespB.Result.Task == nil {
		t.Fatalf("expected a task result for task B, got %+v", rpcRespB.Result)
	}
	taskBID := rpcRespB.Result.Task.ID

	// ListTasks as "internal" (direct call) must see only task A.
	listInternal, err := srv.Handler().ListTasks(context.Background(), &sdka2a.ListTasksRequest{Tenant: "acme"})
	if err != nil {
		t.Fatalf("ListTasks (internal caller): %v", err)
	}
	if !containsTaskID(listInternal.Tasks, taskA.ID) {
		t.Errorf("internal caller cannot see its own task %s: got %v", taskA.ID, taskIDs(listInternal.Tasks))
	}
	if containsTaskID(listInternal.Tasks, taskBID) {
		t.Errorf("internal caller can see caller's task %s — ownership isolation broken: got %v", taskBID, taskIDs(listInternal.Tasks))
	}

	// ListTasks as "caller" (HTTP with valid token) must see only task B.
	bodyList := `{"jsonrpc":"2.0","id":2,"method":"ListTasks","params":{"tenant":"acme"}}`
	reqList, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(bodyList)) //nolint:noctx
	reqList.Header.Set("Content-Type", "application/json")
	reqList.Header.Set("Authorization", "Bearer "+testToken)
	respList, err := http.DefaultClient.Do(reqList)
	if err != nil {
		t.Fatalf("POST ListTasks (caller): %v", err)
	}
	defer respList.Body.Close()

	var rpcRespList struct {
		Result *sdka2a.ListTasksResponse `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(respList.Body).Decode(&rpcRespList); err != nil {
		t.Fatalf("decode ListTasks response (caller): %v", err)
	}
	if rpcRespList.Error != nil {
		t.Fatalf("ListTasks (caller) failed: %s", rpcRespList.Error.Message)
	}
	if rpcRespList.Result == nil {
		t.Fatal("expected a ListTasksResponse result, got nil")
	}
	if !containsTaskID(rpcRespList.Result.Tasks, taskBID) {
		t.Errorf("caller cannot see its own task %s: got %v", taskBID, taskIDs(rpcRespList.Result.Tasks))
	}
	if containsTaskID(rpcRespList.Result.Tasks, taskA.ID) {
		t.Errorf("caller can see internal's task %s — ownership isolation broken: got %v", taskA.ID, taskIDs(rpcRespList.Result.Tasks))
	}
}

func containsTaskID(tasks []*sdka2a.Task, id sdka2a.TaskID) bool {
	for _, task := range tasks {
		if task.ID == id {
			return true
		}
	}
	return false
}

func taskIDs(tasks []*sdka2a.Task) []sdka2a.TaskID {
	ids := make([]sdka2a.TaskID, len(tasks))
	for i, task := range tasks {
		ids[i] = task.ID
	}
	return ids
}
