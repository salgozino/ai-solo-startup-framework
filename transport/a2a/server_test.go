package a2a_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
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
	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     &fake.Provider{ReturnTaskID: "task-1"},
		Store:        store,
		PolicyEngine: policy.NewEngine(),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	_, newErr := transa2a.New(sup, "")
	if newErr == nil {
		t.Fatal("expected error from New() with empty token, got nil")
	}
}

// TestNew_EmptySupervisorTenant_ReturnsError asserts that New() fails fast
// with a clear error when the supervisor's address has no tenant configured,
// instead of silently constructing a server that rejects every request with
// a confusing "tenant does not match" error. address.A2AAddress is `type
// A2AAddress string` (exported, not a bare-string-hiding wrapper), so any
// code in the module — including this test — can assign a name-only or empty
// string to it directly, bypassing the address.New/Parse tenant-non-empty
// guards. This simulates a Supervisor built from a hand-constructed
// supervisor.Config with a misconfigured Addr.
func TestNew_EmptySupervisorTenant_ReturnsError(t *testing.T) {
	var addr address.A2AAddress = "ceo" // no "/tenant" suffix: Tenant() == ""
	store, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     &fake.Provider{ReturnTaskID: "task-1"},
		Store:        store,
		PolicyEngine: policy.NewEngine(),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	_, newErr := transa2a.New(sup, testToken)
	if newErr == nil {
		t.Fatal("expected error from New() with empty supervisor tenant, got nil")
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
	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     fp,
		Store:        store,
		PolicyEngine: policy.NewEngine(),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}

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

// TestTenantInterceptor_Before covers the tenantInterceptor.Before logic:
// an empty tenant and a tenant that does not match the server's own tenant
// are both rejected with codeInvalidParams, while a tenant matching the
// server's own tenant is accepted (satisfies "Empty tenant rejected at the
// edge" and issue #9: "tenant must be a security boundary, not just a
// storage key").
func TestTenantInterceptor_Before(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	cases := []struct {
		name    string
		tenant  string
		wantErr bool
	}{
		{name: "empty tenant", tenant: "", wantErr: true},
		{name: "mismatched tenant", tenant: "evil-corp", wantErr: true},
		{name: "matching tenant", tenant: "acme", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The Authorization header is valid so this exercises
			// tenantInterceptor, not authInterceptor.
			body := fmt.Sprintf(`{
				"jsonrpc":"2.0",
				"id":1,
				"method":"SendMessage",
				"params":{
					"tenant":%q,
					"message":{
						"messageId":"msg-1",
						"role":"ROLE_USER",
						"parts":[{"text":"hello"}]
					}
				}
			}`, tc.tenant)

			req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(body)) //nolint:noctx
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+testToken)

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

			if tc.wantErr {
				if rpcResp.Error == nil {
					t.Fatalf("expected JSON-RPC error response for tenant %q, got success", tc.tenant)
				}
				if rpcResp.Error.Code != codeInvalidParams {
					t.Errorf("expected error code %d, got code %d: %s",
						codeInvalidParams, rpcResp.Error.Code, rpcResp.Error.Message)
				}
				return
			}
			if rpcResp.Error != nil {
				t.Errorf("expected success for tenant %q, got JSON-RPC error: %s", tc.tenant, rpcResp.Error.Message)
			}
		})
	}
}

// TestTenantInterceptor_Before_BoundToSupervisorOwnTenant asserts that the
// interceptor's accepted tenant tracks the specific supervisor's own address
// (sup.Addr().Tenant()) rather than a fixed literal. Every other test in this
// file binds a server to tenant "acme", so a mutant hardcoding
// tenantInterceptor{tenant: "acme"} in New() would still pass the rest of the
// suite; binding this server to "beta" instead and checking that "beta" is
// accepted while the otherwise-universal "acme" is rejected closes that gap.
func TestTenantInterceptor_Before_BoundToSupervisorOwnTenant(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "beta")

	cases := []struct {
		name    string
		tenant  string
		wantErr bool
	}{
		{name: "own tenant (beta) accepted", tenant: "beta", wantErr: false},
		{name: "other server's tenant (acme) rejected", tenant: "acme", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{
				"jsonrpc":"2.0",
				"id":1,
				"method":"SendMessage",
				"params":{
					"tenant":%q,
					"message":{
						"messageId":"msg-1",
						"role":"ROLE_USER",
						"parts":[{"text":"hello"}]
					}
				}
			}`, tc.tenant)

			req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(body)) //nolint:noctx
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+testToken)

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

			if tc.wantErr {
				if rpcResp.Error == nil {
					t.Fatalf("expected JSON-RPC error response for tenant %q, got success", tc.tenant)
				}
				if rpcResp.Error.Code != codeInvalidParams {
					t.Errorf("expected error code %d, got code %d: %s",
						codeInvalidParams, rpcResp.Error.Code, rpcResp.Error.Message)
				}
				return
			}
			if rpcResp.Error != nil {
				t.Errorf("expected success for tenant %q, got JSON-RPC error: %s", tc.tenant, rpcResp.Error.Message)
			}
		})
	}
}

// codeUnauthenticated is a2a-go's JSON-RPC error code for a2a.ErrUnauthenticated
// (see internal/jsonrpc.codeToError). codeInvalidParams is the code for
// a2a.ErrInvalidParams, which is what tenantInterceptor returns for an empty
// tenant and for a mismatched tenant. Distinguishing these two codes is what
// lets a test prove which interceptor rejected a request.
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

// TestProviderFailureMarksFailed sends a message to a supervisor whose provider's RunTask
// fails, and asserts the returned task is FAILED.
// (satisfies "Provider failure marks task FAILED, not silently dropped")
//
// Retargeted (agent-delegation-over-a2a Phase 1): this test previously exercised the
// PolicyEngine == nil / executeDelegation branch via a provider's ResolveAgent failure.
// That branch is dead code — wire.go always sets PolicyEngine — and has been deleted along
// with executeDelegation. The behavior this test actually needs to prove ("a provider failure
// marks the task FAILED, not silently dropped") is exercised through RunTask instead, which is
// the only local-execution path port.Provider now offers.
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

	// Provider whose RunTask returns an error → supervisor marks FAILED.
	fp := &fake.Provider{ReturnRunErr: fmt.Errorf("provider down")}
	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     fp,
		Store:        store,
		PolicyEngine: policy.NewEngine(),
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}

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

// rpcOverHTTP issues a JSON-RPC call against srv as an authenticated HTTP
// caller (valid shared Bearer token) and decodes the result into out.
// Fails the test on a transport or JSON-RPC error.
func rpcOverHTTP(t *testing.T, srv *transa2a.Server, body string, out any) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.BaseURL()+"/invoke", strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /invoke: %v", err)
	}
	defer resp.Body.Close()

	var rpcResp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode JSON-RPC response: %v", err)
	}
	if rpcResp.Error != nil {
		t.Fatalf("JSON-RPC error for %s: %s", body, rpcResp.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(rpcResp.Result, out); err != nil {
			t.Fatalf("decode JSON-RPC result: %v", err)
		}
	}
}

// sendMessageOverHTTP creates a task over HTTP and returns its TaskID.
func sendMessageOverHTTP(t *testing.T, srv *transa2a.Server, msgID, text string) sdka2a.TaskID {
	t.Helper()
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"tenant":"acme","message":{"messageId":%q,"role":"ROLE_USER","parts":[{"text":%q}]}}}`,
		msgID, text)
	var result struct {
		Task *sdka2a.Task `json:"task"`
	}
	rpcOverHTTP(t, srv, body, &result)
	if result.Task == nil {
		t.Fatalf("expected a task result for message %q", msgID)
	}
	return result.Task.ID
}

// listTasksOverHTTP returns the TaskIDs visible to an authenticated HTTP caller.
func listTasksOverHTTP(t *testing.T, srv *transa2a.Server) []sdka2a.TaskID {
	t.Helper()
	var result sdka2a.ListTasksResponse
	rpcOverHTTP(t, srv, `{"jsonrpc":"2.0","id":2,"method":"ListTasks","params":{"tenant":"acme"}}`, &result)
	return taskIDs(result.Tasks)
}

// getTaskOverHTTP reads a single task over HTTP as an authenticated caller.
func getTaskOverHTTP(t *testing.T, srv *transa2a.Server, id sdka2a.TaskID) *sdka2a.Task {
	t.Helper()
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"GetTask","params":{"tenant":"acme","id":%q}}`, id)
	var task sdka2a.Task
	rpcOverHTTP(t, srv, body, &task)
	return &task
}

// TestTaskContinuityAcrossEntryPaths asserts that a stored task stays reachable
// from BOTH entry paths this shared-secret model produces: a trusted in-process
// handler call (nil ServiceParams) and an HTTP request carrying the valid
// shared Bearer token. Both are the same authorized principal, so neither may
// hide the other's tasks. The guarantee that does matter — an unauthenticated
// request reaches no task at all — is covered by
// TestUnauthenticated_RequestRejected.
func TestTaskContinuityAcrossEntryPaths(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	_, srv := newTestSupervisor(t, "ceo", "acme")

	// Task A: created via a direct handler call (nil ServiceParams).
	msgA := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("task A"))
	resultA, err := srv.Handler().SendMessage(context.Background(), &sdka2a.SendMessageRequest{
		Tenant:  "acme",
		Message: msgA,
	})
	if err != nil {
		t.Fatalf("SendMessage (in-process): %v", err)
	}
	taskA, ok := resultA.(*sdka2a.Task)
	if !ok {
		t.Fatalf("expected *a2a.Task result for task A, got %T", resultA)
	}

	// Task B: created over HTTP with the valid Bearer token.
	taskBID := sendMessageOverHTTP(t, srv, "msg-b", "task B")

	// The in-process path must see BOTH tasks.
	listInProcess, err := srv.Handler().ListTasks(context.Background(), &sdka2a.ListTasksRequest{Tenant: "acme"})
	if err != nil {
		t.Fatalf("ListTasks (in-process): %v", err)
	}
	for _, id := range []sdka2a.TaskID{taskA.ID, taskBID} {
		if !containsTaskID(listInProcess.Tasks, id) {
			t.Errorf("in-process ListTasks cannot see task %s: got %v", id, taskIDs(listInProcess.Tasks))
		}
	}

	// The HTTP path must see BOTH tasks.
	listHTTP := listTasksOverHTTP(t, srv)
	for _, id := range []sdka2a.TaskID{taskA.ID, taskBID} {
		if !slices.Contains(listHTTP, id) {
			t.Errorf("HTTP ListTasks cannot see task %s: got %v", id, listHTTP)
		}
	}

	// Cross-path non-List reads: GetTask must work in both directions.
	if got := getTaskOverHTTP(t, srv, taskA.ID); got.ID != taskA.ID {
		t.Errorf("GetTask over HTTP for in-process task %s returned %s", taskA.ID, got.ID)
	}
	gotB, err := srv.Handler().GetTask(context.Background(), &sdka2a.GetTaskRequest{Tenant: "acme", ID: taskBID})
	if err != nil {
		t.Fatalf("GetTask (in-process) for HTTP-created task %s: %v", taskBID, err)
	}
	if gotB.ID != taskBID {
		t.Errorf("GetTask (in-process) for HTTP task %s returned %s", taskBID, gotB.ID)
	}
}

// TestRecoveredTask_ReachableOverHTTP asserts that a task created over HTTP
// before a restart is still reachable over HTTP after crash recovery re-creates
// it in the a2asrv task store. Recovery attaches its own CallContext, so it must
// claim the same owner identity authInterceptor grants HTTP callers — otherwise
// a task parked before a restart (the INPUT_REQUIRED resume flow is the concrete
// casualty) becomes invisible to the wire client that has to reach it.
func TestRecoveredTask_ReachableOverHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	addr, err := address.New("ceo", "acme")
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}
	store, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	newServer := func() *transa2a.Server {
		t.Helper()
		sup, supErr := supervisor.New(supervisor.Config{
			Addr:         addr,
			Provider:     &fake.Provider{ReturnTaskID: "task-1"},
			Store:        store,
			PolicyEngine: policy.NewEngine(),
		})
		if supErr != nil {
			t.Fatalf("supervisor.New: %v", supErr)
		}
		srv, newErr := transa2a.New(sup, testToken)
		if newErr != nil {
			t.Fatalf("transport/a2a.New: %v", newErr)
		}
		t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
		return srv
	}

	// Before the restart: an authenticated HTTP caller creates a task.
	srvBefore := newServer()
	taskID := sendMessageOverHTTP(t, srvBefore, "msg-recover", "before restart")
	if err := srvBefore.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown pre-restart server: %v", err)
	}

	// Park it in INPUT_REQUIRED so RecoverOpenTasks re-registers it in the
	// process-local (therefore now empty) a2asrv task store on restart.
	if err := store.Save(addr, supervisor.TaskRecord{
		TaskID: string(taskID),
		State:  string(sdka2a.TaskStateInputRequired),
		Input:  "before restart",
		Owner:  string(addr),
	}); err != nil {
		t.Fatalf("park task as INPUT_REQUIRED: %v", err)
	}

	// Restart: transa2a.New runs recovery over the same supervisor store.
	srvAfter := newServer()

	if got := getTaskOverHTTP(t, srvAfter, taskID); got.ID != taskID {
		t.Errorf("GetTask over HTTP after recovery returned %s, want %s", got.ID, taskID)
	}
	if ids := listTasksOverHTTP(t, srvAfter); !slices.Contains(ids, taskID) {
		t.Errorf("recovered task %s is invisible to an authenticated HTTP caller: got %v", taskID, ids)
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
