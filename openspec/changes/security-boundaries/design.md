# Design: Security Boundaries — Bearer Auth for A2A Transport

## Technical Approach

Insert `authInterceptor` as the **first** interceptor in the A2A handler chain before `tenantInterceptor`. It reads the `Authorization` header from `ServiceParams`, validates against a config-supplied static token, and sets `callCtx.User` via `a2asrv.NewAuthenticatedUser`. Replace the hardcoded `"supervisor"` task-store authenticator with `a2asrv.NewTaskStoreAuthenticator()`, which reads `callCtx.User.Name`. Token flows: `company.yaml` → `CompanyConfig.AuthTokenEnv` → `wire.go` resolves env var → `transa2a.New(sup, token)`.

## Architecture Decisions

| Option | Tradeoff | Decision |
|--------|----------|----------|
| authInterceptor before tenantInterceptor | User set before tenant check; correct ordering for future per-tenant ACL | **Yes** |
| nil `ServiceParams` = trusted in-process caller | In-process calls (UI adapter, direct handler tests) have no HTTP context | **Yes** — skip auth, set `User="internal"` |
| `AuthTokenEnv` field (stores env-var name, not value) | Consistent with `TelegramGatewayConfig.TokenEnv`; rejects inline secrets at load time | **Yes** |
| `a2asrv.NewTaskStoreAuthenticator()` | Library function that correctly reads `callCtx.User.Name`; replaces ownership bypass | **Yes** — drop hardcoded lambda |
| Switch integration test's `a2aclient` call to direct handler | Avoids configuring client-side auth in test; a2aclient bearer not yet required | **Yes** |

## Data Flow

```
HTTP POST /invoke
  → jsonrpcHandler.ServeHTTP
       → NewCallContext(ctx, NewServiceParams(req.Header))  // non-nil ServiceParams
  → authInterceptor.Before
       → svcParams.Get("authorization") → vals[0]
       → strings.CutPrefix(vals[0], "Bearer ")
       → mismatch → ErrUnauthenticated (rejected)
       → match    → callCtx.User = NewAuthenticatedUser("caller", nil)
  → tenantInterceptor.Before
       → callCtx.Tenant() != "" check
  → defaultRequestHandler → taskstore
       → NewTaskStoreAuthenticator() reads callCtx.User.Name → "caller"

In-process (UI adapter / direct handler call):
  → attachMethodCallContext → NewCallContext(ctx, nil)  // nil ServiceParams
  → authInterceptor.Before
       → svcParams == nil → skip auth → User = "internal"
  → (rest of chain unchanged)
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `transport/a2a/server.go` | Modify | Add `authInterceptor` struct; change `New` to `New(sup, authToken string)`; prepend `authInterceptor` to interceptor slice; replace hardcoded lambda with `NewTaskStoreAuthenticator()`; add `HTTPAuthSecurityScheme` to `buildAgentCard()` |
| `config/schema.go` | Modify | Add `AuthTokenEnv string \`yaml:"auth_token_env"\`` to raw struct AND `CompanyConfig`; validate via `requireEnvRef`; fail-fast in `Load()` if field is missing |
| `cmd/company/wire.go` | Modify | Resolve `os.Getenv(cfg.AuthTokenEnv)` → `authToken`; pass to `transa2a.New(sup, authToken)` |
| `transport/a2a/server_test.go` | Modify | Add `const testToken = "test-bearer-token"`; update `newTestSupervisor` signature; add auth header in HTTP-level tests; add RED `TestUnauthenticated_RequestRejected` |
| `core/supervisor/integration_test.go` | Modify | Add same `testToken` const; update `startSupervisor`; replace `workerClient.SendMessage` with `workerSrv.Handler().SendMessage` for the delegation assertion |

## Interfaces / Contracts

```go
// transport/a2a/server.go

type authInterceptor struct {
    a2asrv.PassthroughCallInterceptor
    token string
}

// Before: nil ServiceParams → trusted internal call (skip auth, set User="internal").
// Non-nil ServiceParams → validate "Authorization: Bearer <token>"; reject with ErrUnauthenticated on mismatch.
func (a authInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error)

// New: authToken must not be empty; fail-fast if empty.
func New(sup *supervisor.Supervisor, authToken string) (*Server, error)

// config/schema.go addition:
type CompanyConfig struct {
    // ... existing fields ...
    AuthTokenEnv string `yaml:"auth_token_env"` // env-var name, resolved by wire.go
}

// buildAgentCard() addition:
SecuritySchemes: sdka2a.NamedSecuritySchemes{
    "bearer": sdka2a.HTTPAuthSecurityScheme{Scheme: "bearer"},
},
SecurityRequirements: sdka2a.SecurityRequirementsOptions{
    {sdka2a.SecuritySchemeName("bearer"): {}},
},
```

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | `authInterceptor.Before` logic | Table-driven: nil params (internal pass), valid bearer, missing header, wrong token, malformed prefix |
| Unit | `New()` with empty token | Returns error immediately |
| Integration (RED) | HTTP POST without `Authorization` → JSON-RPC error | `TestUnauthenticated_RequestRejected` |
| Integration | HTTP POST with valid bearer → request proceeds | Updated `TestEmptyTenantRejected` includes bearer header |
| Integration | Direct handler call (`srv.Handler()`) → trusted path | Existing `TestProviderFailureMarksFailed` continues to work unchanged |
| Integration | `ListTasks` returns only caller's tasks | Single-token scenario; ownership enforced via `NewTaskStoreAuthenticator()` |

## Threat Matrix

N/A — no routing, shell, subprocess, VCS/PR automation, executable-file classification, or process-integration boundary.

## Migration / Rollout

`company.yaml` MUST add `auth_token_env: <ENV_VAR_NAME>` and the corresponding env var must be set before startup. Missing or empty value causes `transa2a.New()` to fail fast with a descriptive error. No data migration required. Document the new required field in `company.yaml` comments and README.

The Telegram gateway and any future A2A agent-to-agent clients must include the Bearer header on outgoing calls once those methods are implemented (currently `errNotImplemented`).

## Open Questions

- [ ] Should `testToken` be a shared constant in a dedicated `internal/testutil` package, or duplicated in each test file? (Duplication is simpler and avoids a new package; cross-package constant risks coupling test internals.)
