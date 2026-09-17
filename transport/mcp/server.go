package mcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/salgozino/ai-solo-startup-framework/config"
)

// Server is the long-lived MCP HTTP server that exposes one tool per risk_policy action kind.
// It binds each adapter invocation to a per-invocation bearer token resolved to
// {tenant, agent, taskID}, and acknowledges tools/call without executing — recording an
// ActionIntent in the per-invocation Sink instead.
//
// Server is intentionally unreferenced from cmd/company until Phase 3; build green after Phase 2.
type Server struct {
	httpSrv  *http.Server
	listener net.Listener
	registry *Registry
	tenant   string
	serveErr atomic.Value // abnormal Serve death (not http.ErrServerClosed); see Err
}

// New creates an MCP Server configured for tenant, registering one tool per policy key.
// The registry is used to resolve bearer tokens and store per-invocation intents.
// Call Start to bind and begin serving.
func New(tenant string, policies map[string]config.Policy, registry *Registry) *Server {
	mcpSrv := gomcp.NewServer(
		&gomcp.Implementation{Name: "ai-solo-startup-framework", Version: "1.0"},
		nil,
	)

	registerTools(mcpSrv, tenant, policies, registry)

	handler := gomcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *gomcp.Server { return mcpSrv },
		nil,
	)

	// Wrap with bearer-token auth middleware.
	// TokenInfo.Expiration and UserID are set by the middleware; no hand-rolled expiry check.
	// The token verifier does NOT check tenant — that check is in the tool handler via Resolve.
	authedHandler := mcpauth.RequireBearerToken(
		registry.TokenVerifier(),
		&mcpauth.RequireBearerTokenOptions{
			AllowMissingExpiration: false,
		},
	)(handler)

	return &Server{
		httpSrv:  &http.Server{Handler: authedHandler},
		registry: registry,
		tenant:   tenant,
	}
}

// Start binds to addr and begins serving in a background goroutine.
// addr must be a TCP address; use "127.0.0.1:0" for ephemeral port assignment.
// If the address is already occupied, Start returns a non-nil error immediately and
// no invocations are started — this aborts materialize before any supervisor is ready
// (design Threat matrix: "Bind failure aborts materialize before any supervisor is ready").
func (s *Server) Start(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("mcp: server: listen %s: %w", addr, err)
	}
	s.listener = ln
	go func() {
		if serveErr := s.httpSrv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.serveErr.Store(serveErr)
			// Immediate operator-visible signal at the point of death. Err() (below)
			// additionally lets production callers (see cmd/company/wire.go's
			// MCPHealthCheck wiring) detect and act on this per task, not just in logs.
			log.Printf("mcp: server for tenant %q died abnormally: %v", s.tenant, serveErr)
		}
	}()
	return nil
}

// Err reports the Serve goroutine's abnormal-death error, or nil if healthy/stopped normally.
func (s *Server) Err() error {
	if v := s.serveErr.Load(); v != nil {
		return v.(error)
	}
	return nil
}

// Addr returns the loopback address the server is listening on (e.g. "127.0.0.1:54321").
// Panics if called before a successful Start.
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Shutdown gracefully stops the HTTP server using ctx.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpSrv.Shutdown(ctx)
}

// Registry returns the Registry associated with this Server so cmd/company can
// pass it to adapters for minting tokens.
func (s *Server) Registry() *Registry {
	return s.registry
}
