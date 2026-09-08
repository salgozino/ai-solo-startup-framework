# Tasks: Fix SSE Shutdown Delay

## Review Workload Forecast

| Field | Value |
|-------|-------|
| Estimated changed lines | ~90–120 |
| 400-line budget risk | Low |
| Chained PRs recommended | No |
| Suggested split | Single PR |
| Delivery strategy | single-pr |
| Chain strategy | size-exception |

Decision needed before apply: No
Chained PRs recommended: No
Chain strategy: size-exception
400-line budget risk: Low

### Suggested Work Units

| Unit | Goal | Likely PR | Focused test command | Runtime harness | Rollback boundary |
|------|------|-----------|----------------------|-----------------|-------------------|
| 1 | Full shutdown fix (handler struct + `Shutdown()` + select arm + wiring + tests) | PR 1 | `go test ./ui/... -run TestUIHandlerShutdown\|TestShutdownIdempotent` | N/A — shutdown timing verified via `httptest.NewRecorder`; no live server required | Revert `ui/handler.go`, `cmd/company/main.go`, `ui/handler_test.go`; prior behavior fully restored |

## Phase 1: Foundation (Struct + TDD Red Tests)

- [x] 1.1 [RED] In `ui/handler_test.go`, add `TestUIHandlerShutdown`: open SSE via `httptest.NewRecorder`, call `h.Shutdown()`, assert `resp.Body.Read` returns EOF within 100ms — must fail (no `Shutdown()` yet)
- [x] 1.2 [RED] In `ui/handler_test.go`, add `TestShutdownIdempotent`: construct fresh `UIHandler`, call `Shutdown()` twice, assert no panic — must fail (no `Shutdown()` yet)
- [x] 1.3 In `ui/handler.go`, add `shutdownCh chan struct{}` and `once sync.Once` fields to the `UIHandler` struct
- [x] 1.4 In `ui/handler.go`, initialize `shutdownCh: make(chan struct{})` inside `NewUIHandler`

## Phase 2: Core Implementation

- [x] 2.1 In `ui/handler.go`, add `Shutdown()` method: `h.once.Do(func() { close(h.shutdownCh) })`
- [x] 2.2 In `ui/handler.go`, add `case <-h.shutdownCh: return` arm to the `handleEvents` select loop
- [x] 2.3 In `cmd/company/main.go`, insert `ceoHandler.Shutdown()` immediately before `uiSrv.Shutdown(ctx)` in the shutdown sequence

## Phase 3: Verification

- [x] 3.1 Run `go test ./ui/... -run TestUIHandlerShutdown` — assert SSE goroutine exits ≤100ms [GREEN]
- [x] 3.2 Run `go test ./ui/... -run TestShutdownIdempotent` — assert no panic on double `Shutdown()` call [GREEN]
- [x] 3.3 Run `go test ./...` — full regression; all pre-existing tests in `ui/handler_test.go` pass unchanged
