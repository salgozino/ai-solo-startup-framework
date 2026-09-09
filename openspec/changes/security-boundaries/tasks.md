# Tasks: Security Boundaries — Bearer Auth for A2A Transport

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~125–175 |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | auto-chain |
| Chain strategy | stacked-to-main |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: stacked-to-main
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Full security-boundaries change | PR 1 | `go test ./transport/a2a/... ./core/supervisor/... -count=1` | N/A — all coverage via in-process HTTP test harness; no live server required | Revert `transport/a2a/server.go`, `config/schema.go`, `cmd/company/wire.go`, and both test files; no data migration |

## Phase 1: Foundation — Config & Fail-Fast Start

- [x] 1.1 [RED] Add `TestNew_EmptyToken_ReturnsError` in `transport/a2a/server_test.go`; assert `a2a.New(sup, "")` returns a non-nil error
- [x] 1.2 Add `AuthTokenEnv string \`yaml:"auth_token_env"\`` to raw config struct AND `CompanyConfig` in `config/schema.go`; validate via `requireEnvRef` pattern; fail-fast in `Load()` when field is missing
- [x] 1.3 [GREEN] Change signature to `New(sup *supervisor.Supervisor, authToken string) (*Server, error)` in `transport/a2a/server.go`; return descriptive error when `authToken == ""`
- [x] 1.4 Update `cmd/company/wire.go`: resolve `authToken := os.Getenv(cfg.AuthTokenEnv)`; pass `authToken` to `transa2a.New(sup, authToken)`; handle the returned error

## Phase 2: Core Implementation — authInterceptor

- [x] 2.1 [RED] Add table-driven `TestAuthInterceptor_Before` in `transport/a2a/server_test.go`: nil ServiceParams (trusted internal call), valid bearer, missing `Authorization` header, wrong token, malformed prefix (no "Bearer " prefix)
- [x] 2.2 [GREEN] Add `authInterceptor` struct embedding `a2asrv.PassthroughCallInterceptor` in `transport/a2a/server.go`
- [x] 2.3 [GREEN] Implement `authInterceptor.Before(ctx, callCtx, req)` in `transport/a2a/server.go`: nil svcParams → set `User="internal"`, skip; non-nil → `strings.CutPrefix` "Bearer " → match → `callCtx.User = NewAuthenticatedUser("caller", nil)`; else → return `ErrUnauthenticated`
- [x] 2.4 Prepend `authInterceptor{token: authToken}` to interceptor slice in `transport/a2a/server.go`; replace hardcoded `"supervisor"` authenticator lambda with `a2asrv.NewTaskStoreAuthenticator()`
- [x] 2.5 Add `sdka2a.HTTPAuthSecurityScheme{Scheme: "bearer"}` + `SecurityRequirements` to `buildAgentCard()` in `transport/a2a/server.go`

## Phase 3: Integration Testing

- [x] 3.1 [RED] Add `TestUnauthenticated_RequestRejected` in `transport/a2a/server_test.go`: HTTP POST without `Authorization` header → assert JSON-RPC unauthorized error response
- [x] 3.2 Add `const testToken = "test-bearer-token"`; update `newTestSupervisor` signature to accept token; add `Authorization: Bearer test-bearer-token` header to all HTTP-level test requests in `transport/a2a/server_test.go`
- [x] 3.3 Add `const testToken = "test-bearer-token"`; update `startSupervisor` in `core/supervisor/integration_test.go`; replace `workerClient.SendMessage` call with `workerSrv.Handler().SendMessage` for delegation assertion
- [x] 3.4 [GREEN] Run `go test ./transport/a2a/... ./core/supervisor/... -count=1`; all tests pass; verify no regressions in `go test ./... -count=1`
