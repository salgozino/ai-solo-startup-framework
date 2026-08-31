// Package a2a implements the A2A JSON-RPC transport for the supervisor framework.
// Each supervisor gets its own loopback HTTP server serving:
//   - POST /invoke   — A2A JSON-RPC handler (SendMessage, GetTask, ListTasks, …)
//   - GET  /.well-known/agent-card.json — public Agent Card
//
// The CallInterceptor embedded here rejects any request whose tenant field is
// empty before task processing begins (satisfies spec: "empty tenant rejected").
package a2a

import (
	"context"
	"fmt"
	"net"
	"net/http"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
)

// Server wraps a net/http server that speaks A2A JSON-RPC on a loopback port.
type Server struct {
	sup      *supervisor.Supervisor
	httpSrv  *http.Server
	handler  a2asrv.RequestHandler
	baseURL  string
}

// tenantInterceptor rejects requests whose tenant field is empty.
// An empty and absent tenant are indistinguishable in the A2A wire format
// (both serialize as omitempty), so we must reject empty at the edge.
type tenantInterceptor struct {
	a2asrv.PassthroughCallInterceptor
}

// Before implements a2asrv.CallInterceptor.
func (tenantInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
	if callCtx.Tenant() == "" {
		return ctx, nil, fmt.Errorf("%w: tenant must not be empty", sdka2a.ErrInvalidParams)
	}
	return ctx, nil, nil
}

// New creates a Server for the given supervisor.
// It binds a random loopback port (127.0.0.1:0) and constructs the Agent Card.
func New(sup *supervisor.Supervisor) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("a2a server: listen: %w", err)
	}

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	card := buildAgentCard(sup.Addr(), baseURL)

	// Use an in-memory task store with a fixed user authenticator so that
	// ListTasks works without requiring real per-request authentication in v1.
	// ponytail: one machine, one tenant at a time — no auth complexity for v1.
	store := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: func(_ context.Context) (string, error) {
			return "supervisor", nil
		},
	})

	handler := a2asrv.NewHandler(sup,
		a2asrv.WithCallInterceptors(tenantInterceptor{}),
		a2asrv.WithTaskStore(store),
	)

	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(handler))
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))

	httpSrv := &http.Server{Handler: mux}

	s := &Server{
		sup:     sup,
		httpSrv: httpSrv,
		handler: handler,
		baseURL: baseURL,
	}

	// Start serving in the background.
	go func() {
		_ = httpSrv.Serve(ln)
	}()

	// Transition the supervisor to IDLE — endpoint is now registered.
	if err := sup.RecoverOpenTasks(context.Background(), handler); err != nil {
		return nil, fmt.Errorf("a2a server: recover: %w", err)
	}
	sup.MarkReady()

	return s, nil
}

// BaseURL returns the base HTTP URL for this server (e.g. "http://127.0.0.1:54321").
func (s *Server) BaseURL() string {
	return s.baseURL
}

// Handler returns the a2asrv.RequestHandler for direct use in integration tests.
func (s *Server) Handler() a2asrv.RequestHandler {
	return s.handler
}

// Shutdown stops the HTTP server gracefully.
func (s *Server) Shutdown(ctx context.Context) error {
	s.sup.Shutdown()
	return s.httpSrv.Shutdown(ctx)
}

// buildAgentCard constructs the public Agent Card for the given supervisor address.
func buildAgentCard(addr address.A2AAddress, baseURL string) *sdka2a.AgentCard {
	invokeURL := baseURL + "/invoke"
	iface := sdka2a.NewAgentInterface(invokeURL, sdka2a.TransportProtocolJSONRPC)
	iface.Tenant = addr.Tenant()

	return &sdka2a.AgentCard{
		Name:        addr.Name(),
		Description: fmt.Sprintf("Supervisor for agent %q", addr),
		SupportedInterfaces: []*sdka2a.AgentInterface{iface},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Capabilities: sdka2a.AgentCapabilities{
			Streaming: true,
		},
		Skills: []sdka2a.AgentSkill{
			{
				ID:          "task",
				Name:        "Task execution",
				Description: "Execute a task via the provider",
				Tags:        []string{"task"},
			},
		},
		Version: "1.0",
	}
}
