package a2a

import (
	"context"
	"errors"
	"net/http"
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

// blockingProvider's RunTask blocks on ctx until canceled, simulating a peer
// that never finishes on its own. Used to prove the client's deadline surfaces
// as a distinguishable timeout rather than a fabricated success.
type blockingProvider struct {
	*fake.Provider
}

func (p *blockingProvider) RunTask(ctx context.Context, _ string, _ string) (port.ProviderResult, error) {
	<-ctx.Done()
	return port.ProviderResult{}, ctx.Err()
}

var _ port.Provider = (*blockingProvider)(nil)

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
	if result.State != string(sdka2a.TaskStateCompleted) {
		t.Errorf("State = %q, want %q", result.State, sdka2a.TaskStateCompleted)
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

// TestClient_BlocksUntilPeerTerminal verifies Delegate blocks until the peer's
// task reaches a terminal state and returns that state and the peer's task ID.
//
// Output is deliberately NOT asserted here: the terminal COMPLETED event still
// carries a nil Status.Message in this slice — attaching the peer's output as a
// text part is design D9 / tasks.md Phase 5 of this change, not implemented
// here. Asserting a non-empty Output would fake a result this slice cannot yet
// produce.
func TestClient_BlocksUntilPeerTerminal(t *testing.T) {
	prov := &fake.Provider{ReturnRunResult: port.ProviderResult{Output: "peer done"}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", clientTestToken, nil)

	result, err := c.Delegate(context.Background(), "engineer", "do the work")
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if result.State != string(sdka2a.TaskStateCompleted) {
		t.Errorf("State = %q, want %q", result.State, sdka2a.TaskStateCompleted)
	}
	if result.PeerTaskID == "" {
		t.Error("expected a non-empty PeerTaskID")
	}
}

// TestClient_DeadlineReturnsDistinguishableTimeout verifies that when the
// caller's context deadline is reached before the peer's task terminates,
// Delegate returns an error distinguishable via errors.Is(err,
// context.DeadlineExceeded), never a fabricated success.
// Satisfies: a2a-client "SendMessage Blocks Until the Peer Task Is Terminal,
// Bounded by a Deadline" (deadline scenario).
func TestClient_DeadlineReturnsDistinguishableTimeout(t *testing.T) {
	prov := &blockingProvider{Provider: &fake.Provider{}}
	_, dir := newTestPeer(t, "engineer", "acme", prov, nil)

	c := NewClient(dir, "acme", clientTestToken, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := c.Delegate(ctx, "engineer", "do the work")
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected errors.Is(err, context.DeadlineExceeded), got %v", err)
	}
}
