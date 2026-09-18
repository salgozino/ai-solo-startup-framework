package a2a

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// bearerScheme is the credentials-store key the shared bearer token is seeded
// under. It MUST be the literal string "bearer" to match the security scheme
// name buildAgentCard publishes (server.go's SecuritySchemes map key) —
// a2aclient.AuthInterceptor looks credentials up by this exact scheme name, and
// a mismatch here silently sends every outbound call unauthenticated (design D8).
const bearerScheme = sdka2a.SecuritySchemeName("bearer")

// clientSessionID is the fixed SessionID every outbound Client call is
// attached under via a2aclient.AttachSessionID. One process-wide delegation
// Client authenticates with one shared bearer credential, so a single constant
// session is sufficient — AttachSessionID exists only so
// a2aclient.AuthInterceptor can look the credential up, not to scope a
// per-caller identity.
const clientSessionID = a2aclient.SessionID("delegation-client")

// ErrPeerNonTerminalState reports that a peer answered a Delegate call with a
// state that is neither terminal nor INPUT_REQUIRED (e.g. SUBMITTED, WORKING)
// — a protocol violation under port.Delegator. Wrapped with %w so callers
// discriminate with errors.Is, never by matching message text (issue #78).
var ErrPeerNonTerminalState = errors.New("peer returned a non-terminal task state")

// Client implements port.Delegator over the real A2A wire: it resolves a
// peer's Agent Card, authenticates with the shared bearer token, propagates
// the tenant, and blocks until the peer's task reaches a terminal (or
// INPUT_REQUIRED) state. See design D8.
type Client struct {
	dir      *PeerDirectory
	tenant   string
	creds    *a2aclient.InMemoryCredentialsStore
	resolver *agentcard.Resolver
	// httpClient, when non-nil, is used for both Agent Card resolution and the
	// outbound A2A JSON-RPC transport. Nil uses the a2a-go package defaults.
	// Exists so tests can observe outbound requests via a recording
	// http.RoundTripper.
	httpClient *http.Client
}

var _ port.Delegator = (*Client)(nil)

// NewClient constructs a Client that delegates to peers registered in dir, on
// behalf of tenant, authenticating every outbound call with the shared
// authToken. httpClient may be nil to use the a2a-go package defaults.
func NewClient(dir *PeerDirectory, tenant, authToken string, httpClient *http.Client) *Client {
	creds := a2aclient.NewInMemoryCredentialsStore()
	creds.Set(clientSessionID, bearerScheme, a2aclient.AuthCredential(authToken))
	return &Client{
		dir:        dir,
		tenant:     tenant,
		creds:      creds,
		resolver:   agentcard.NewResolver(httpClient),
		httpClient: httpClient,
	}
}

// resolvePeer resolves role's Agent Card (spec: card BEFORE any send) and
// constructs a fresh a2aclient.Client for it, wired with the shared
// credentials store.
//
// The returned client's calls MUST be made against a context carrying
// a2aclient.AttachSessionID(ctx, clientSessionID) — see Delegate and
// PeerTaskState. Omitting that attaches NO Authorization header at all
// (a2aclient.AuthInterceptor.Before returns early with no error), sending the
// request unauthenticated with no client-side error. This is deliberately
// exposed as its own method (rather than inlined) so
// TestClient_SessionlessRequestIsRejectedByPeer can prove the peer, not the
// client library, is what actually stops that mistake.
func (c *Client) resolvePeer(ctx context.Context, role string) (*a2aclient.Client, error) {
	base, err := c.dir.BaseURL(role)
	if err != nil {
		return nil, fmt.Errorf("transport/a2a: client: role %q: %w", role, err)
	}

	card, err := c.resolver.Resolve(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("transport/a2a: client: resolve agent card for role %q: %w", role, err)
	}

	opts := []a2aclient.FactoryOption{
		a2aclient.WithCallInterceptors(&a2aclient.AuthInterceptor{Service: c.creds}),
	}
	if c.httpClient != nil {
		opts = append(opts, a2aclient.WithJSONRPCTransport(c.httpClient))
	}

	peer, err := a2aclient.NewFromCard(ctx, card, opts...)
	if err != nil {
		return nil, fmt.Errorf("transport/a2a: client: connect to role %q: %w", role, err)
	}
	return peer, nil
}

// Delegate implements port.Delegator.
func (c *Client) Delegate(ctx context.Context, role, body string) (port.DelegationResult, error) {
	peer, err := c.resolvePeer(ctx, role)
	if err != nil {
		return port.DelegationResult{}, err
	}

	// MANDATORY on every call: a2aclient.AuthInterceptor.Before attaches no
	// header unless a SessionID is on the context (design D8).
	ctx = a2aclient.AttachSessionID(ctx, clientSessionID)

	msg := sdka2a.NewMessage(sdka2a.MessageRoleUser, sdka2a.NewTextPart(body))
	res, err := peer.SendMessage(ctx, &sdka2a.SendMessageRequest{Tenant: c.tenant, Message: msg})
	if err != nil {
		return port.DelegationResult{}, fmt.Errorf("transport/a2a: client: delegate to role %q: %w", role, err)
	}

	task, ok := res.(*sdka2a.Task)
	if !ok {
		return port.DelegationResult{}, fmt.Errorf("transport/a2a: client: delegate to role %q: peer returned %T, want *a2a.Task", role, res)
	}

	// port.Delegator: Delegate blocks until the peer STOPS advancing, so any
	// state that is neither terminal nor INPUT_REQUIRED means the peer answered
	// early — a protocol violation, never a result. TaskState.Terminal() is the
	// SDK's own terminal set (COMPLETED, CANCELED, FAILED, REJECTED; a2a-go
	// v2.5.0 a2a/core.go); never re-enumerate it here.
	if state := task.Status.State; !state.Terminal() && state != sdka2a.TaskStateInputRequired {
		return port.DelegationResult{}, fmt.Errorf("transport/a2a: client: delegate to role %q: %w: %s", role, ErrPeerNonTerminalState, state)
	}

	return resultFromTask(task), nil
}

// PeerTaskState implements port.Delegator.
//
// It deliberately does NOT apply Delegate's terminality check: this is a
// polling READ of a task's current state, so SUBMITTED/WORKING is the expected
// answer here and rejecting it would break the very wait it exists to support.
// The asymmetry with Delegate is the contract, not an inconsistency to "fix".
func (c *Client) PeerTaskState(ctx context.Context, role, peerTaskID string) (port.DelegationResult, error) {
	peer, err := c.resolvePeer(ctx, role)
	if err != nil {
		return port.DelegationResult{}, err
	}

	ctx = a2aclient.AttachSessionID(ctx, clientSessionID)

	task, err := peer.GetTask(ctx, &sdka2a.GetTaskRequest{Tenant: c.tenant, ID: sdka2a.TaskID(peerTaskID)})
	if err != nil {
		return port.DelegationResult{}, fmt.Errorf("transport/a2a: client: peer task state for role %q task %q: %w", role, peerTaskID, err)
	}

	return resultFromTask(task), nil
}

// resultFromTask converts an a2a.Task into a port.DelegationResult, reading
// the terminal output text from Status.Message (design D9: the supervisor's
// terminal COMPLETED event carries the task's output as a text part there —
// as of this change's Phase 4, that wiring is not yet in place, so Output is
// empty even for a COMPLETED task; see tasks.md Phase 5).
func resultFromTask(task *sdka2a.Task) port.DelegationResult {
	result := port.DelegationResult{
		PeerTaskID: string(task.ID),
		State:      string(task.Status.State),
	}
	if task.Status.State == sdka2a.TaskStateCompleted && task.Status.Message != nil {
		for _, part := range task.Status.Message.Parts {
			result.Output += part.Text()
		}
	}
	return result
}
