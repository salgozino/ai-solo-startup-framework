# Proposal: Security Boundaries — Bearer Auth for A2A Transport

## Intent

Two security gaps share a single fix path in the A2A transport layer:

- **Issue #4**: `InMemoryStoreConfig.Authenticator` always returns `"supervisor"` for every
  caller. Task ownership enforcement is a no-op — all callers see all tasks.
- **Issue #9**: `tenantInterceptor` validates only that tenant is non-empty. Any caller can
  claim any tenant. Isolation is filename-only (`agentname__tenant.json`).

A `CallInterceptor` that validates a Bearer token and sets caller identity on the call context
makes both the existing ownership logic and tenant scoping actually enforce boundaries.

## Scope

### In Scope
- `authInterceptor` in `transport/a2a/server.go`: validate `Authorization: Bearer <token>`, set `callCtx.User`
- `AuthToken` field in `CompanyConfig` (`config/schema.go`) with env-var reference
- Wire token from config in `cmd/company/wire.go`; fail-fast at `New()` if token is empty
- Update `buildAgentCard()` to declare `httpBearer` security scheme
- Update `server_test.go` and `core/supervisor/integration_test.go` with auth header

### Out of Scope
- Per-tenant token rotation or multi-tenant key management
- A2A client-side implementation (all methods are `errNotImplemented`)
- RBAC beyond caller identity binding
- Token revocation or refresh

## Capabilities

### New Capabilities
- `a2a-bearer-auth`: Caller authentication via `Authorization: Bearer` header; blocks
  unauthenticated requests; binds caller identity to task ownership and tenant scoping.

### Modified Capabilities
- `a2a-transport`: Task ownership filtering becomes effective; `ListTasks` now requires auth
  (previously open — breaking behavioral change for any unauthenticated caller).

## Approach

Insert `authInterceptor` as the first interceptor before `tenantInterceptor`. Reads
`callCtx.ServiceParams().Get("authorization")`, validates against config token, sets
`callCtx.User.Name`. The existing `NewTaskStoreAuthenticator()` (already in the library) reads
that name — no task store changes required.

**Interceptor chain:**
`authInterceptor.Before → tenantInterceptor.Before → handler → tenantInterceptor.After → authInterceptor.After`

**Affected agent roles**: supervisor (all tenant instances), telegram gateway (must carry token).

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `transport/a2a/server.go` | Modified | Add `authInterceptor`, rewire interceptor slice, accept token in `New()` |
| `config/schema.go` | Modified | Add `AuthToken string` to `CompanyConfig` |
| `cmd/company/wire.go` | Modified | Pass `cfg.AuthToken` to `a2a.New()` |
| `transport/a2a/server_test.go` | Modified | Add `Authorization` header; shared `testToken` constant |
| `core/supervisor/integration_test.go` | Modified | Same — shared test token |
| `buildAgentCard()` in `server.go` | Modified | Declare `httpBearer` security scheme |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Empty token lets server accept unauthenticated requests | Med | Fail-fast at `New()` with descriptive error |
| Test drift from mismatched token constants | Med | Single `const testToken` in `server_test.go`, imported by integration tests |
| `ListTasks` requiring auth is a breaking change | Low | No external callers today; document as intentional |
| Future A2A agent-to-agent calls omit Bearer header | Low | Document in `a2a-bearer-auth` spec as a MUST requirement |

## Rollback Plan

Change is isolated to `transport/a2a/server.go` and `config/schema.go`. To revert:
1. Remove `authInterceptor` from the interceptor slice in `a2a.New()`
2. Restore the hardcoded `"supervisor"` authenticator
3. Remove `AuthToken` from `CompanyConfig`
4. Remove auth header additions from test helpers

No database migrations. `git revert` of the change commits is sufficient.

## Dependencies

- `a2asrv.NewTaskStoreAuthenticator()` — already in the library; no new imports needed

## Success Criteria

- [ ] Unauthenticated requests return JSON-RPC error (unauthorized)
- [ ] `ListTasks` returns only tasks owned by the authenticated caller
- [ ] Two distinct Bearer tokens cannot see each other's tasks across tenants
- [ ] Agent card declares `httpBearer` security scheme
- [ ] All tests pass with auth header added; no regressions
- [ ] Server startup fails with descriptive error when `auth_token` is not configured
