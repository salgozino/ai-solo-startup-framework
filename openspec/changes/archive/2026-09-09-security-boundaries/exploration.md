## Exploration: Security Boundaries — Tenant Auth (Issues #4 + #9)

### Current State

The system today has two distinct gaps:

**Gap 1 — Tenant is a label, not a boundary (Issue #9)**

`A2AAddress` encodes tenant as `{agent-name}/{tenant}` (e.g. `ceo/acme`). The `tenantInterceptor`
in `transport/a2a/server.go` checks only that `callCtx.Tenant() != ""`. ANY non-empty string
passes. There is no validation that the caller is authorized to claim that tenant.

The store layer (`core/supervisor/store.go`) uses the address as a filename key
(`agentname__tenant.json`). Isolation is purely by filename — no auth guard.

**Gap 2 — Fixed `"supervisor"` authenticator (Issue #4)**

The `InMemoryStoreConfig.Authenticator` in `New()` (line 62–65 of `server.go`) always returns
`"supervisor"` regardless of who is calling. The `taskstore.InMemory` implementation:
- `List()` filters by `userName` AND rejects empty usernames → but since everyone is
  `"supervisor"`, all callers get all tasks
- `Get()` / `Update()` check `stored.user != userName` → passes for all callers since everyone is `"supervisor"`

This is an **ownership bypass**: the task store thinks it is enforcing ownership per user, but
all users are the same user.

**What flows today (request path):**
```
HTTP POST /invoke
  → jsonrpcHandler.ServeHTTP (a2asrv/jsonrpc.go:50)
       → NewCallContext(ctx, NewServiceParams(req.Header))  ← headers in ServiceParams
  → InterceptedHandler.SendMessage / ListTasks / etc.
       → tenantInterceptor.Before()  ← only checks tenant != ""
  → defaultRequestHandler (a2asrv/handler.go)
       → taskstore.InMemory.{Create,Update,Get,List}()
            → Authenticator() → always returns "supervisor"
```

**Key library discovery:**
`a2asrv/jsonrpc.go:50` — HTTP request headers are already passed into `ServiceParams` via
`NewCallContext(ctx, NewServiceParams(req.Header))`. The `Authorization` header is accessible
inside any `CallInterceptor.Before` via `callCtx.ServiceParams().Get("authorization")`.

The library also ships `a2asrv.NewTaskStoreAuthenticator()` (middleware.go:106) which reads
`callCtx.User.Name` from context. This is the intended hook: auth interceptor sets the User,
task store reads it.

**Current A2A client status:**
Both adapters' A2A client methods (`SendMessage`, `ResolveAgent`, `SendTask`) return
`errNotImplemented`. No agent currently calls another agent over A2A. The only active callers
of the A2A transport are tests and the telegram gateway (tests only — not production).

---

### Affected Areas

- `transport/a2a/server.go` — add `authInterceptor`, replace fixed authenticator, accept token in `New()`
- `config/schema.go` — add `AuthToken` field to `CompanyConfig` (env-var reference pattern)
- `cmd/company/wire.go` — pass token from config to `a2a.New()`; add `WithAuthToken` option or pass directly
- `core/supervisor/supervisor.go` — `Config` probably does NOT need a token field; auth lives at transport layer
- `transport/a2a/server_test.go` — all requests need to include the token
- `core/supervisor/integration_test.go` — direct handler calls go through `InterceptedHandler`, so they need token too
- `gateways/telegram/contract_test.go` — if gateway calls supervisor A2A, needs token (currently tests use direct handler)
- `openspec/config.yaml` — update testing capabilities once Go stack is locked

---

### Approaches

#### 1. **Per-tenant Bearer token in `CallInterceptor`** (Recommended)

Add a second `CallInterceptor` (`authInterceptor`) that:
1. Reads `Authorization: Bearer <token>` from `callCtx.ServiceParams().Get("authorization")`
2. Validates it against a per-Server expected token (set at construction time from `company.yaml`)
3. On success: sets `callCtx.User = a2asrv.NewAuthenticatedUser(callCtx.Tenant(), nil)`
4. On failure: returns `sdka2a.ErrUnauthenticated`

Replace the fixed `Authenticator` with `a2asrv.NewTaskStoreAuthenticator()` so the task store
scopes operations to the authenticated user (= tenant).

**Config change:** `company.yaml` gets an `auth_token` field with the same env-var reference
pattern already used by `telegram.token_env`:
```yaml
tenant: acme
auth_token: COMPANY_AUTH_TOKEN   # env var name; inline literal also accepted
```

Token validation in interceptor:
```go
type authInterceptor struct {
    a2asrv.PassthroughCallInterceptor
    token string
}

func (a authInterceptor) Before(ctx context.Context, callCtx *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
    vals, _ := callCtx.ServiceParams().Get("authorization")
    if len(vals) == 0 || !strings.EqualFold(strings.TrimPrefix(vals[0], "Bearer "), a.token) {
        return ctx, nil, sdka2a.ErrUnauthenticated
    }
    callCtx.User = a2asrv.NewAuthenticatedUser(callCtx.Tenant(), nil)
    return ctx, nil, nil
}
```

- **Pros:** Zero new dependencies; uses library-provided hooks exactly as designed; fixes both issues atomically; token can be rotated via env var; consistent with existing config pattern
- **Cons:** Token is a shared secret (no per-caller identity); no replay protection (acceptable for loopback)
- **Effort:** Low (~150 lines total including tests)

**Interceptor order:** `authInterceptor` runs BEFORE `tenantInterceptor`. The auth check sets
`callCtx.User`; the tenant check (already present) ensures the tenant field is non-empty. Both
remain independent concerns.

---

#### 2. JWT with tenant claims

Token is a signed JWT carrying `sub` = tenant. Validated with a shared HMAC key from config.

- **Pros:** No shared secret stored in plain config; tenant is cryptographically bound in token; tokens are revocable per-caller
- **Cons:** Requires `golang.org/x/crypto` or `github.com/golang-jwt/jwt`; key rotation logic; more complex test setup; overkill for single-tenant loopback
- **Effort:** Medium (~300 lines + new dependency)

---

#### 3. mTLS per tenant

Each tenant has its own client certificate; server verifies client cert tenant matches request tenant.

- **Pros:** Strong mutual auth; tenant is cryptographically provable
- **Cons:** Certificate infrastructure (CA, cert generation, rotation); incompatible with current loopback HTTP; significant complexity
- **Effort:** High

---

### Recommendation

**Option 1 — Bearer token interceptor** is the right choice for this codebase today.

Reasons:
1. The library provides the exact hook (`NewTaskStoreAuthenticator()` + `ServiceParams`) — this
   is the designed extension point, not a workaround
2. Zero new dependencies (stdlib only: `strings`, already imported)
3. Token-in-env-var matches the established pattern (`TELEGRAM_OWNER_ID`, `TELEGRAM_TOKEN`)
4. The A2A client methods are all `errNotImplemented` — no existing production caller to break
5. Test blast radius is small: update `server_test.go` and `integration_test.go` to send the header
6. The ponytail comment ("one machine, one tenant at a time") explicitly accepts this simplicity
7. Upgradeable: if/when multi-tenant SaaS is needed, the `authInterceptor` is the right seam to swap for JWT

**Interceptor chain after the change:**
```
authInterceptor.Before → tenantInterceptor.Before → handler → tenantInterceptor.After → authInterceptor.After
```
Order matters: auth must precede tenant validation so `callCtx.User` is set before the store is reached.

---

### Store Layer Assessment

The file-based `Store` does NOT need changes for v1. Auth at the transport layer means only
authenticated callers reach the store. The "one file per address" isolation is sufficient.

For a future multi-tenant deployment where tenants are separate organizations, the store would
need a secondary tenant check (e.g. verify `addr.Tenant()` matches the authenticated user before
returning data). This is a future concern, not a v1 blocker.

---

### Risks

1. **Token must be required for the interceptor to work** — if `auth_token` is absent from
   `company.yaml`, the server should fail-fast at `New()` rather than silently boot with no auth.
   A `""` token would accept any `"Bearer "` prefix.
2. **Test helpers** need a shared test token constant to avoid drift between test files.
3. **Future A2A clients** (when `SendMessage` is implemented) must include the bearer token in
   outgoing requests — this is a documentation/convention risk if not addressed in the proposal.
4. **Agent Card `SecurityScheme`** should declare the auth scheme so external callers know the
   expected header. The `buildAgentCard` function should be updated to include this.
5. **`ListTasks` without auth** currently works by design (the ponytail comment). Removing this
   is a user-visible breaking change for any existing automation scripts. The proposal must note this.

---

### Ready for Proposal

**Yes** — the approach is clear, the affected surface is bounded, and the library provides all
needed primitives. The proposal should cover:
- `CompanyConfig.AuthToken` field (env-var reference, required)
- `authInterceptor` implementation and interceptor chain order
- `buildAgentCard()` security scheme annotation
- Test helper pattern for the shared test token
- Migration note: any existing client must add `Authorization: Bearer <token>` after the upgrade
