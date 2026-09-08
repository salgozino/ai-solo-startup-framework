# Design: Fix SSE Shutdown Delay

## Technical Approach

Add explicit shutdown control to `UIHandler` via a `shutdownCh chan struct{}` closed by `sync.Once`. The `handleEvents` select loop gains a `case <-h.shutdownCh: return` arm. In `main.go`, call `ceoHandler.Shutdown()` before `uiSrv.Shutdown(ctx)` so all SSE goroutines exit before the HTTP server attempts to drain. Net result: shutdown goes from ~15s to <1s with zero browser-side changes.

## Architecture Decisions

| Decision | Options | Chosen | Rationale |
|----------|---------|--------|-----------|
| Signal mechanism | channel close, context field, `http.Server.BaseContext` cancel | `chan struct{}` + `sync.Once` | Minimal — `sync` already imported; no new field type; matches A2A server's own `Shutdown()` pattern; idempotency is free |
| Shutdown ordering | sequential (handler first), concurrent | Sequential: handler → HTTP → A2A | SSE goroutines must exit before HTTP drain; sequential ordering is deterministic and simple |
| Channel init | eager in `NewUIHandler`, lazy (nil check) | Eager | Avoids nil-guard in hot select path; `NewUIHandler` is the single construction site |

## Data Flow

```
SIGINT/SIGTERM
    │
    ▼
main.go: ceoHandler.Shutdown()
    │   └─ closes shutdownCh (sync.Once)
    │
    ├── each handleEvents goroutine
    │       select case <-h.shutdownCh → return
    │       defer removes client from h.clients
    │
    ▼
main.go: uiSrv.Shutdown(ctx)   ← 0 active SSE connections → drains instantly
    │
    ▼
main.go: rt.srv.Shutdown(ctx)  ← A2A servers (unchanged)
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `ui/handler.go` | Modify | Add `shutdownCh chan struct{}` + `once sync.Once` to `UIHandler`; init in `NewUIHandler`; add `Shutdown()` method; add `case <-h.shutdownCh: return` in `handleEvents` select |
| `cmd/company/main.go` | Modify | Insert `ceoHandler.Shutdown()` call before `uiSrv.Shutdown(ctx)` |
| `ui/handler_test.go` | Modify | Add `TestUIHandlerShutdown` (SSE goroutine exits ≤100ms) and `TestShutdownIdempotent` (no panic on double call) |

## Interfaces / Contracts

```go
// Shutdown closes all active SSE connections and is safe to call multiple times.
// It must be called before http.Server.Shutdown() to avoid the 15s drain delay.
func (h *UIHandler) Shutdown()
```

`UIHandler` struct additions:

```go
type UIHandler struct {
    sup     Supervisor
    mu      sync.Mutex
    clients []*sseClient
    shutdownCh chan struct{}  // closed on Shutdown()
    once       sync.Once     // guards close(shutdownCh)
}
```

`handleEvents` select arm addition (after existing `case <-tick.C`):

```go
case <-h.shutdownCh:
    return
```

## Testing Strategy

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit | SSE goroutine exits within 100ms of `Shutdown()` | `TestUIHandlerShutdown`: open SSE via `httptest`, call `h.Shutdown()`, assert `resp.Body.Read` returns EOF within 100ms |
| Unit | `Shutdown()` idempotent (no panic on double call) | `TestShutdownIdempotent`: call `Shutdown()` twice on a fresh handler |
| Regression | All existing `ui/handler_test.go` tests pass unchanged | `go test ./ui/...` |

## Threat Matrix

N/A — no routing, shell, subprocess, VCS/PR automation, executable-file classification, or process-integration boundary.

## Migration / Rollout

No migration required. The change is fully backward-compatible: `Broadcast()` after `Shutdown()` is safe (the existing `select/default` drop in `Broadcast` handles closed clients). No browser changes. No schema changes.

## Open Questions

None — design is fully resolved.
