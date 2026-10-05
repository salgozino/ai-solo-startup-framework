package a2a

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/salgozino/ai-solo-startup-framework/config"
	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/policy"
	"github.com/salgozino/ai-solo-startup-framework/core/port"
	"github.com/salgozino/ai-solo-startup-framework/core/port/fake"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
)

// clientTestToken is the shared bearer secret used by this file's real servers.
// Named distinctly from server_test.go's testToken: that file lives in the
// external a2a_test package, so there is no symbol collision, but a shared name
// would be confusing to a reader jumping between the two files.
const clientTestToken = "client-test-bearer-token"

// newTestPeer starts a real supervisor + A2A server on a random loopback port,
// backed by prov, and returns it alongside a single-role PeerDirectory with role
// already bound to the server's live base URL. Cleanup is registered automatically.
func newTestPeer(t *testing.T, role, tenant string, prov port.Provider, policyCfg map[string]config.Policy) (*Server, *PeerDirectory) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: skipping in -short mode")
	}

	addr, err := address.New(role, tenant)
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}
	store, err := supervisor.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("supervisor.NewStore: %v", err)
	}
	sup, err := supervisor.New(supervisor.Config{
		Addr:         addr,
		Provider:     prov,
		Store:        store,
		PolicyEngine: policy.NewEngine(),
		Gateway:      &fake.Gateway{},
		Role:         role,
		PolicyConfig: policyCfg,
	})
	if err != nil {
		t.Fatalf("supervisor.New: %v", err)
	}
	srv, err := New(sup, clientTestToken)
	if err != nil {
		t.Fatalf("transport/a2a.New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	dir, err := NewPeerDirectory([]string{role})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}
	if err := dir.Bind(role, srv.BaseURL()); err != nil {
		t.Fatalf("dir.Bind: %v", err)
	}

	return srv, dir
}

// stubPeerTaskID is the task ID stubPeer reports for every request.
const stubPeerTaskID = "stub-task-1"

// stubPeer starts an httptest.Server speaking the A2A JSON-RPC wire shape that
// reports its task as parked in state, and returns a PeerDirectory bound to it.
// newTestPeer cannot stand in: its real supervisor always drives a task to a
// terminal or INPUT_REQUIRED state, so only a stub can emit the violation.
func stubPeer(t *testing.T, role, tenant string, state sdka2a.TaskState) *PeerDirectory {
	t.Helper()

	addr, err := address.New(role, tenant)
	if err != nil {
		t.Fatalf("address.New: %v", err)
	}
	task := &sdka2a.Task{ID: stubPeerTaskID, Status: sdka2a.TaskStatus{State: state}}

	var baseURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(buildAgentCard(addr, baseURL))
	})
	mux.HandleFunc("/invoke", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("stub peer: decode request: %v", err)
			return
		}
		// SendMessage results are wrapped in the a2a.StreamResponse event
		// envelope; GetTask results are a bare task.
		var result any = task
		if req.Method == "SendMessage" {
			result = sdka2a.StreamResponse{Event: task}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "1", "result": result})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	baseURL = srv.URL

	dir, err := NewPeerDirectory([]string{role})
	if err != nil {
		t.Fatalf("NewPeerDirectory: %v", err)
	}
	if err := dir.Bind(role, srv.URL); err != nil {
		t.Fatalf("dir.Bind: %v", err)
	}
	return dir
}

// TestClient_DelegateAcceptsRunningPeerAndRejectsOtherStates proves Delegate
// enforces the port.Delegator contract after park/resume (Slice 6). Overturned:
// SUBMITTED and WORKING used to be rejected as protocol violations because
// Delegate blocked until the peer stopped; the replacement is that they are the
// normal "still running" answer, returned as a result for the caller to park on.
// A state that is neither running, terminal nor INPUT_REQUIRED is still a
// protocol violation, discriminable with errors.Is.
//
// PeerTaskState is a polling READ and accepts every state, as before.
func TestClient_DelegateAcceptsRunningPeerAndRejectsOtherStates(t *testing.T) {
	for _, state := range []sdka2a.TaskState{sdka2a.TaskStateSubmitted, sdka2a.TaskStateWorking} {
		t.Run(string(state), func(t *testing.T) {
			dir := stubPeer(t, "engineer", "acme", state)
			c := NewClient(dir, "acme", clientTestToken, nil)

			got, err := c.Delegate(context.Background(), "engineer", "do the work")
			if err != nil {
				t.Fatalf("Delegate must accept a running peer, got error: %v", err)
			}
			if got.State != string(state) || got.PeerTaskID != stubPeerTaskID {
				t.Errorf("Delegate = %+v, want state %q and peer task %q", got, state, stubPeerTaskID)
			}
		})
	}

	t.Run(string(sdka2a.TaskStateAuthRequired), func(t *testing.T) {
		dir := stubPeer(t, "engineer", "acme", sdka2a.TaskStateAuthRequired)
		c := NewClient(dir, "acme", clientTestToken, nil)

		if _, err := c.Delegate(context.Background(), "engineer", "do the work"); !errors.Is(err, ErrPeerNonTerminalState) {
			t.Errorf("Delegate error = %v, want errors.Is(err, ErrPeerNonTerminalState)", err)
		}
		got, err := c.PeerTaskState(context.Background(), "engineer", stubPeerTaskID)
		if err != nil {
			t.Fatalf("PeerTaskState must accept any state, got error: %v", err)
		}
		if got.State != string(sdka2a.TaskStateAuthRequired) {
			t.Errorf("PeerTaskState State = %q", got.State)
		}
	})
}

// recordingTransport wraps an http.RoundTripper and records every request's
// path and headers, in order, so tests can prove request ORDERING (card before
// /invoke) and HEADER CONTENT (a real Authorization header, not just a peer
// that happened to accept the call for an unrelated reason).
type recordingTransport struct {
	base http.RoundTripper

	mu      sync.Mutex
	paths   []string
	headers []http.Header
}

func newRecordingTransport(base http.RoundTripper) *recordingTransport {
	return &recordingTransport{base: base}
}

func (r *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.paths = append(r.paths, req.URL.Path)
	r.headers = append(r.headers, req.Header.Clone())
	r.mu.Unlock()
	return r.base.RoundTrip(req)
}

func (r *recordingTransport) snapshot() ([]string, []http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...), append([]http.Header(nil), r.headers...)
}

// authHeaderFor returns the Authorization header recorded for the first request
// whose path ends with suffix, or "" if none was recorded.
func authHeaderFor(paths []string, headers []http.Header, suffix string) string {
	for i, p := range paths {
		if strings.HasSuffix(p, suffix) {
			return headers[i].Get("Authorization")
		}
	}
	return ""
}

// gatedProvider's RunTask blocks until release is closed, simulating a peer that
// is still working. It ignores ctx on purpose: the peer's execution must outlive
// the delegation request that created it.
type gatedProvider struct {
	*fake.Provider
	release chan struct{}
}

func (p *gatedProvider) RunTask(ctx context.Context, taskID, input string) (port.ProviderResult, error) {
	<-p.release
	return p.Provider.RunTask(ctx, taskID, input)
}

var _ port.Provider = (*gatedProvider)(nil)

// settle polls PeerTaskState until the peer task leaves SUBMITTED/WORKING.
// Delegate returns as soon as the peer task exists (park/resume, Slice 6), so a
// test that wants the peer's final state must wait for it the way the
// supervisor's watcher does.
func settle(t *testing.T, c *Client, role, peerTaskID string) port.DelegationResult {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := c.PeerTaskState(context.Background(), role, peerTaskID)
		if err != nil {
			t.Fatalf("PeerTaskState: %v", err)
		}
		if st := sdka2a.TaskState(got.State); st != sdka2a.TaskStateSubmitted && st != sdka2a.TaskStateWorking {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("peer task %q still %s after 5s", peerTaskID, got.State)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestClient_CardResolvedBeforeSend verifies the A2A client resolves the peer's
// Agent Card before issuing any /invoke request.
// Satisfies: a2a-client "The Client Resolves the Peer's Agent Card Before Sending".
func TestClient_CardResolvedBeforeSend(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	rec := newRecordingTransport(http.DefaultTransport)
	c := NewClient(dir, "acme", clientTestToken, &http.Client{Transport: rec})

	if _, err := c.Delegate(context.Background(), "engineer", "do the work"); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	paths, _ := rec.snapshot()
	cardIdx, invokeIdx := -1, -1
	for i, p := range paths {
		if cardIdx == -1 && strings.Contains(p, "/.well-known/agent-card.json") {
			cardIdx = i
		}
		if invokeIdx == -1 && strings.HasSuffix(p, "/invoke") {
			invokeIdx = i
		}
	}
	if cardIdx == -1 {
		t.Fatalf("no agent-card request recorded: %v", paths)
	}
	if invokeIdx == -1 {
		t.Fatalf("no /invoke request recorded: %v", paths)
	}
	if cardIdx >= invokeIdx {
		t.Errorf("expected the agent-card request (index %d) before /invoke (index %d): %v", cardIdx, invokeIdx, paths)
	}
}

// TestClient_BearerPresentAndAccepted verifies every outbound delegation request
// carries the shared bearer token as a real Authorization header, and that the
// peer accepts it.
// Satisfies: a2a-client "The Client Authenticates Every Outbound Request With the
// Shared Bearer Token".
func TestClient_BearerPresentAndAccepted(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	rec := newRecordingTransport(http.DefaultTransport)
	c := NewClient(dir, "acme", clientTestToken, &http.Client{Transport: rec})

	result, err := c.Delegate(context.Background(), "engineer", "do the work")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if got := settle(t, c, "engineer", result.PeerTaskID); got.State != string(sdka2a.TaskStateCompleted) {
		t.Errorf("State = %q, want %q", got.State, sdka2a.TaskStateCompleted)
	}

	paths, headers := rec.snapshot()
	got := authHeaderFor(paths, headers, "/invoke")
	want := "Bearer " + clientTestToken
	if got != want {
		t.Errorf("Authorization header on /invoke = %q, want %q", got, want)
	}
}

// TestClient_SessionlessRequestIsRejectedByPeer proves the exact risk design D8
// names: a2aclient.AuthInterceptor.Before attaches NO header unless a SessionID
// is on the context — omitting AttachSessionID sends an unauthenticated request
// with no error from the client library itself. This test skips AttachSessionID
// deliberately, using the package-internal resolvePeer helper, and asserts the
// PEER is the one that catches it.
// Satisfies: a2a-client "A request without a valid token is rejected by the
// peer, proving the client cannot bypass auth".
func TestClient_SessionlessRequestIsRejectedByPeer(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", clientTestToken, nil)

	peer, err := c.resolvePeer(context.Background(), "engineer")
	if err != nil {
		t.Fatalf("resolvePeer: %v", err)
	}

	// Deliberately no a2aclient.AttachSessionID here.
	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart("do the work"))
	_, err = peer.SendMessage(context.Background(), &sdka2a.SendMessageRequest{Tenant: "acme", Message: msg})
	if err == nil {
		t.Fatal("expected the peer to reject a session-less request, got nil error")
	}
	if !errors.Is(err, sdka2a.ErrUnauthenticated) {
		t.Errorf("expected errors.Is(err, sdka2a.ErrUnauthenticated), got %v", err)
	}
}

// TestClient_WrongTokenIsRejectedByPeer verifies a Client configured with the
// wrong bearer token is rejected by the peer's authInterceptor, not silently
// accepted.
func TestClient_WrongTokenIsRejectedByPeer(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", "wrong-token", nil)

	_, err := c.Delegate(context.Background(), "engineer", "do the work")
	if err == nil {
		t.Fatal("expected an error delegating with the wrong bearer token, got nil")
	}
	if !errors.Is(err, sdka2a.ErrUnauthenticated) {
		t.Errorf("expected errors.Is(err, sdka2a.ErrUnauthenticated), got %v", err)
	}
}

// TestClient_WrongTenantIsRejectedByPeer verifies a Client configured with a
// tenant that does not match the peer's bound tenant is rejected by the peer's
// tenantInterceptor.
// Satisfies: a2a-client "The Client Sets the Request Tenant So the Peer's
// Tenant Check Accepts It" (negative case).
func TestClient_WrongTenantIsRejectedByPeer(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "evil-corp", clientTestToken, nil)

	_, err := c.Delegate(context.Background(), "engineer", "do the work")
	if err == nil {
		t.Fatal("expected an error delegating with a mismatched tenant, got nil")
	}
	if !errors.Is(err, sdka2a.ErrInvalidParams) {
		t.Errorf("expected errors.Is(err, sdka2a.ErrInvalidParams), got %v", err)
	}
}

// TestClient_PeerOutputCrossesTheWire verifies the peer's output reaches the
// delegator. Overturned by Slice 6: this was TestClient_BlocksUntilPeerTerminal
// (Delegate blocked until the peer was terminal). Replacement: Delegate returns
// once the peer task exists and the terminal state and output are read through
// PeerTaskState, which is what the supervisor's watcher does.
//
// The peer's output must cross the wire: the supervisor's terminal COMPLETED
// event carries it as a text part in Status.Message (design D9, Phase 5).
func TestClient_PeerOutputCrossesTheWire(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "peer done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", clientTestToken, nil)

	delegated, err := c.Delegate(context.Background(), "engineer", "do the work")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if delegated.PeerTaskID == "" {
		t.Error("expected a non-empty PeerTaskID")
	}
	result := settle(t, c, "engineer", delegated.PeerTaskID)
	if result.State != string(sdka2a.TaskStateCompleted) {
		t.Errorf("State = %q, want %q", result.State, sdka2a.TaskStateCompleted)
	}
	if result.Output != "peer done" {
		t.Errorf("Output = %q, want %q (peer output must cross the wire)", result.Output, "peer done")
	}
}

// TestClient_DelegateDoesNotWaitForPeer replaces
// TestClient_DeadlineReturnsDistinguishableTimeout (Slice 6): Delegate no longer
// blocks on the peer, so a peer that is still working must not hold the call —
// it returns promptly with a running state and a peer task ID to poll.
func TestClient_DelegateDoesNotWaitForPeer(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	prov := &gatedProvider{Provider: &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "late"}}, release: release}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", clientTestToken, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	delegated, err := c.Delegate(ctx, "engineer", "do the work")
	if err != nil {
		t.Fatalf("Delegate must return while the peer is still working, got: %v", err)
	}
	if st := sdka2a.TaskState(delegated.State); st != sdka2a.TaskStateSubmitted && st != sdka2a.TaskStateWorking {
		t.Errorf("Delegate State = %q, want a running state", delegated.State)
	}
	if delegated.PeerTaskID == "" {
		t.Error("expected a non-empty PeerTaskID to poll")
	}
	// The peer keeps running after the delegation request returned.
	got, err := c.PeerTaskState(context.Background(), "engineer", delegated.PeerTaskID)
	if err != nil {
		t.Fatalf("PeerTaskState: %v", err)
	}
	if got.State != string(sdka2a.TaskStateWorking) {
		t.Errorf("peer state = %q, want WORKING while the provider is gated", got.State)
	}
}

// TestClient_PeerTaskStateReportsCurrentStatus verifies PeerTaskState reads
// back a parked (non-terminal) peer task's current state and ID — the read
// path a future watcher (deferred to agent-delegation-chained-approval) will
// poll repeatedly.
func TestClient_PeerTaskStateReportsCurrentStatus(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{
		ActionIntents: []port.ActionIntent{{Kind: "telegram_send"}},
	}}
	policyCfg := map[string]config.Policy{
		"telegram_send": {Risk: "risky", AllowedRoles: []string{"engineer"}},
	}
	_, dir := newTestPeer(t, "engineer", "acme", prov, policyCfg)

	c := NewClient(dir, "acme", clientTestToken, nil)

	delegated, err := c.Delegate(context.Background(), "engineer", "escalate please")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if delegated.PeerTaskID == "" {
		t.Fatal("expected a non-empty PeerTaskID for the parked peer task")
	}
	if got := settle(t, c, "engineer", delegated.PeerTaskID); got.State != string(sdka2a.TaskStateInputRequired) {
		t.Fatalf("settled State = %q, want %q (peer should have escalated, not completed)", got.State, sdka2a.TaskStateInputRequired)
	}

	got, err := c.PeerTaskState(context.Background(), "engineer", delegated.PeerTaskID)
	if err != nil {
		t.Fatalf("PeerTaskState: %v", err)
	}
	if got.State != string(sdka2a.TaskStateInputRequired) {
		t.Errorf("PeerTaskState State = %q, want %q", got.State, sdka2a.TaskStateInputRequired)
	}
	if got.PeerTaskID != delegated.PeerTaskID {
		t.Errorf("PeerTaskState PeerTaskID = %q, want %q", got.PeerTaskID, delegated.PeerTaskID)
	}
}

// TestClient_PeerTaskStateReportsTerminalOutput proves PeerTaskState's
// independent read path reports a COMPLETED peer task's state, ID, and the
// output the peer's supervisor attached to its terminal COMPLETED event
// (design D9, Phase 5) — read via GetTask, with no SendMessage involved.
func TestClient_PeerTaskStateReportsTerminalOutput(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "peer output"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", clientTestToken, nil)

	delegated, err := c.Delegate(context.Background(), "engineer", "do the work")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if got := settle(t, c, "engineer", delegated.PeerTaskID); got.State != string(sdka2a.TaskStateCompleted) {
		t.Fatalf("settled State = %q, want %q", got.State, sdka2a.TaskStateCompleted)
	}

	got, err := c.PeerTaskState(context.Background(), "engineer", delegated.PeerTaskID)
	if err != nil {
		t.Fatalf("PeerTaskState: %v", err)
	}
	if got.State != string(sdka2a.TaskStateCompleted) {
		t.Errorf("PeerTaskState State = %q, want %q", got.State, sdka2a.TaskStateCompleted)
	}
	if got.PeerTaskID != delegated.PeerTaskID {
		t.Errorf("PeerTaskState PeerTaskID = %q, want %q", got.PeerTaskID, delegated.PeerTaskID)
	}
	if got.Output != "peer output" {
		t.Errorf("PeerTaskState Output = %q, want %q (peer output must cross the wire)", got.Output, "peer output")
	}
}
