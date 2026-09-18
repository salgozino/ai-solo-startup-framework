package mcp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/salgozino/ai-solo-startup-framework/core/port"
)

// invocation holds the per-adapter-invocation state: identity, intent sink, and dedupe map.
type invocation struct {
	tenant string
	agent  string
	taskID string
	exp    time.Time
	sink   *Sink

	// dedupe maps dedupeKey → receipt ID to suppress repeat tool calls.
	// Protected by mu.
	mu     sync.Mutex
	dedupe map[string]string
}

// Handle is the adapter-side handle for a live invocation.
// The adapter calls Drain after the subprocess exits to retrieve recorded intents.
type Handle struct {
	inv   *invocation
	reg   *Registry
	token string

	mu      sync.Mutex
	drained bool
}

// Registry maps opaque per-invocation bearer tokens to their active invocations.
// All methods are safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]*invocation
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]*invocation)}
}

// Mint creates a new invocation entry bound to {tenant, agent, taskID} with
// expiry exp, and returns an opaque bearer token and a Handle.
// The token is 32 cryptographically-random bytes encoded as base64url.
func (r *Registry) Mint(tenant, agent, taskID string, exp time.Time) (string, *Handle) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand is not expected to fail; panic to surface misconfigured environments.
		panic(fmt.Sprintf("mcp: registry: crypto/rand.Read: %v", err))
	}
	token := base64.RawURLEncoding.EncodeToString(b)

	inv := &invocation{
		tenant: tenant,
		agent:  agent,
		taskID: taskID,
		exp:    exp,
		sink:   NewSink(0),
		dedupe: make(map[string]string),
	}

	r.mu.Lock()
	r.entries[token] = inv
	r.mu.Unlock()

	return token, &Handle{inv: inv, reg: r, token: token}
}

// Resolve looks up the token in the registry, checks it is not expired, and
// verifies that the registered tenant matches claimedTenant. Returns an error
// for unknown, expired, or foreign-tenant tokens.
func (r *Registry) Resolve(token, claimedTenant string) (*invocation, error) {
	r.mu.RLock()
	inv, ok := r.entries[token]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mcp: unknown or drained token")
	}
	if time.Now().After(inv.exp) {
		return nil, fmt.Errorf("mcp: token expired")
	}
	if inv.tenant != claimedTenant {
		return nil, fmt.Errorf("mcp: tenant mismatch: token belongs to %q, not %q", inv.tenant, claimedTenant)
	}
	return inv, nil
}

// TokenVerifier returns an auth.TokenVerifier that validates tokens in this registry.
// It checks existence and expiry only; tenant validation is deferred to Resolve in tool handlers.
// The returned TokenInfo carries UserID = token (the opaque bearer string), so handlers can
// pass it to Resolve for the tenant check.
func (r *Registry) TokenVerifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, req *http.Request) (*auth.TokenInfo, error) {
		r.mu.RLock()
		inv, ok := r.entries[token]
		r.mu.RUnlock()

		if !ok {
			return nil, fmt.Errorf("%w: unknown token", auth.ErrInvalidToken)
		}
		if time.Now().After(inv.exp) {
			return nil, fmt.Errorf("%w: expired token", auth.ErrInvalidToken)
		}
		return &auth.TokenInfo{UserID: token, Expiration: inv.exp}, nil
	}
}

// Drain removes the invocation entry from the registry and returns the sink contents.
// Idempotent: a second call returns nil without panicking.
func (h *Handle) Drain() []port.ActionIntent {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.drained {
		return nil
	}
	h.drained = true

	h.reg.mu.Lock()
	delete(h.reg.entries, h.token)
	h.reg.mu.Unlock()

	return h.inv.sink.Read()
}
