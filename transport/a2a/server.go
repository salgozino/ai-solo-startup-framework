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
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	sdka2a "github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"

	"github.com/salgozino/ai-solo-startup-framework/core/address"
	"github.com/salgozino/ai-solo-startup-framework/core/supervisor"
)

// Server wraps a net/http server that speaks A2A JSON-RPC on a loopback port.
type Server struct {
	sup     *supervisor.Supervisor
	httpSrv *http.Server
	handler a2asrv.RequestHandler
	baseURL string
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

// bearerPrefix is the required "Authorization" header prefix for a valid token.
const bearerPrefix = "Bearer "

// authenticatedUserID is the task-store owner identity granted to every
// authorized caller. A single shared Bearer secret means there is exactly ONE
// authenticated principal: a valid-token HTTP request and a trusted in-process
// call (nil ServiceParams) are the same authorized party. Task ownership is
// keyed on this identity, so every path reaching the store — Before and crash
// recovery in New — must use it. Splitting it partitions stored tasks into
// mutually invisible sets and breaks continuity across restarts and entry
// paths, with no security gain: unauthenticated callers are rejected by
// authInterceptor before any handler or store access. This constant is the seam
// to replace if per-caller identities are ever introduced.
const authenticatedUserID = "authenticated"

// authInterceptor authenticates every inbound request against a shared
// Bearer token before any other interceptor or handler runs. A nil
// ServiceParams (set by [a2asrv.NewCallContext] when a caller invokes the
// handler directly, without going through the HTTP transport) identifies a
// trusted in-process caller and skips the token check.
type authInterceptor struct {
	a2asrv.PassthroughCallInterceptor
	token string
}

// Before implements a2asrv.CallInterceptor.
func (a authInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
	if callCtx.ServiceParams() == nil {
		// No HTTP context: this is a trusted in-process call (e.g. the UI
		// adapter or a direct handler invocation in tests).
		callCtx.User = a2asrv.NewAuthenticatedUser(authenticatedUserID, nil)
		return ctx, nil, nil
	}

	vals, ok := callCtx.ServiceParams().Get("authorization")
	if !ok || len(vals) == 0 {
		return ctx, nil, fmt.Errorf("%w: missing Authorization header", sdka2a.ErrUnauthenticated)
	}

	token, hasPrefix := strings.CutPrefix(vals[0], bearerPrefix)
	if !hasPrefix || subtle.ConstantTimeCompare([]byte(token), []byte(a.token)) != 1 {
		return ctx, nil, fmt.Errorf("%w: invalid bearer token", sdka2a.ErrUnauthenticated)
	}

	callCtx.User = a2asrv.NewAuthenticatedUser(authenticatedUserID, nil)
	return ctx, nil, nil
}

// New creates a Server for the given supervisor, authenticating all inbound
// requests against authToken. Returns an error if authToken is empty.
func New(sup *supervisor.Supervisor, authToken string) (*Server, error) {
	if authToken == "" {
		return nil, fmt.Errorf("a2a server: auth_token must not be empty")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("a2a server: listen: %w", err)
	}

	baseURL := fmt.Sprintf("http://%s", ln.Addr().String())
	card := buildAgentCard(sup.Addr(), baseURL)

	// Use an in-memory task store whose authenticator reads the caller
	// identity set by authInterceptor, so an unauthenticated context can
	// never reach a stored task. Every authorized caller shares
	// authenticatedUserID, so tasks stay reachable across entry paths.
	store := taskstore.NewInMemory(&taskstore.InMemoryStoreConfig{
		Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})

	handler := a2asrv.NewHandler(sup,
		a2asrv.WithCallInterceptors(authInterceptor{token: authToken}, tenantInterceptor{}),
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

	// registerFn is called for each INPUT_REQUIRED task found in the supervisor's
	// file store. It seeds the a2asrv in-memory task store so that a subsequent
	// SendMessage carrying the same TaskID is recognised as a resume (StoredTask != nil).
	registerFn := func(ctx context.Context, taskID, _ string) error {
		task := &sdka2a.Task{
			ID: sdka2a.TaskID(taskID),
			Status: sdka2a.TaskStatus{
				State: sdka2a.TaskStateInputRequired,
			},
		}
		_, err := store.Create(ctx, task)
		if errors.Is(err, taskstore.ErrTaskAlreadyExists) {
			return nil // idempotent
		}
		return err
	}

	// Recovery runs outside the HTTP interceptor chain (registerFn calls
	// store.Create directly), so NewTaskStoreAuthenticator has no CallContext
	// to read from unless we attach one here. Recovered tasks must carry
	// authenticatedUserID or an HTTP client with an in-flight task before a
	// restart could no longer read, cancel, or resume it afterwards.
	recoverCtx, recoverCallCtx := a2asrv.NewCallContext(context.Background(), nil)
	recoverCallCtx.User = a2asrv.NewAuthenticatedUser(authenticatedUserID, nil)

	// Transition the supervisor to IDLE — endpoint is now registered.
	if err := sup.RecoverOpenTasks(recoverCtx, handler, registerFn); err != nil {
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
		Name:                addr.Name(),
		Description:         fmt.Sprintf("Supervisor for agent %q", addr),
		SupportedInterfaces: []*sdka2a.AgentInterface{iface},
		DefaultInputModes:   []string{"text/plain"},
		DefaultOutputModes:  []string{"text/plain"},
		Capabilities: sdka2a.AgentCapabilities{
			Streaming: true,
		},
		SecuritySchemes: sdka2a.NamedSecuritySchemes{
			"bearer": sdka2a.HTTPAuthSecurityScheme{Scheme: "bearer"},
		},
		SecurityRequirements: sdka2a.SecurityRequirementsOptions{
			{sdka2a.SecuritySchemeName("bearer"): {}},
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
