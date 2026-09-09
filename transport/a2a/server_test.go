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
