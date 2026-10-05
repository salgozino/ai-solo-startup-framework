# Proposal: Fix SSE Shutdown Delay

## Intent

The HTTP server takes ~15 seconds to shut down because `http.Server.Shutdown()` waits for active SSE connections that never go idle. `handleEvents` in `ui/handler.go` blocks on `r.Context().Done()`, but Go's `Shutdown()` does not cancel active request contexts — it only stops accepting new connections and waits for existing ones to finish. SSE connections are permanently alive (keepalive every 25s), so the full timeout elapses on every graceful stop.

## Scope

### In Scope
- Add `Shutdown()` method to `UIHandler` with `shutdownCh chan struct{}` + `sync.Once`
- Update `handleEvents` select loop to exit on `<-h.shutdownCh`
- Update shutdown sequence in `main.go` to call `ceoHandler.Shutdown()` before `uiSrv.Shutdown(ctx)`
- Add test in `ui/handler_test.go` proving SSE goroutine exits within 100ms of `Shutdown()`

### Out of Scope
- Changes to `transport/a2a/server.go` (JSON-RPC requests are short-lived, unaffected)
- Frontend reconnect logic in `ui/app.js` (already handles server close gracefully)
- BaseContext-level cancellation (Approach 2 — cancels ALL requests, not scoped to SSE)

## Capabilities

### New Capabilities
- `sse-graceful-shutdown`: `UIHandler` can be explicitly shut down, closing all active SSE streams before the HTTP server drains

### Modified Capabilities
None

## Approach

Add explicit shutdown control to `UIHandler`:

1. `UIHandler` gains `shutdownCh chan struct{}` (closed on `Shutdown()`) and `sync.Once` for idempotency.
2. `handleEvents` select adds `case <-h.shutdownCh: return` — goroutine exits immediately.
3. `main.go` shutdown sequence: `ceoHandler.Shutdown()` → `uiSrv.Shutdown(ctx)`. HTTP server sees no active SSE connections; drains in milliseconds.

~15 LOC total. No new dependencies.

## Affected Areas

| Area | Impact | Description |
|------|--------|-------------|
| `ui/handler.go` | Modified | Add `shutdownCh`, `once`, `Shutdown()` method; update `handleEvents` select |
| `cmd/company/main.go` | Modified | Call `ceoHandler.Shutdown()` before `uiSrv.Shutdown(ctx)` |
| `ui/handler_test.go` | New | Test that SSE goroutine exits ≤100ms after `Shutdown()` |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| `Broadcast()` called after `Shutdown()` | Low | Existing select/default drop in event loop handles this safely |
| Double `Shutdown()` call panics on closed channel | Low | `sync.Once` wraps the `close(h.shutdownCh)` call |
| Browser clients see abrupt disconnect | Low | `ui/app.js` reconnect loop already handles server-initiated close |

## Rollback Plan

Revert `ui/handler.go` and `cmd/company/main.go` to their pre-change state via `git revert` or branch reset. No schema migrations, no data changes, no external state to undo.

## Dependencies

None — stdlib only (`sync.Once`, channel close).

## Success Criteria

- [ ] Server shuts down in ≤1 second with an active SSE client connected
- [ ] `TestUIHandlerShutdown` passes: goroutine exits within 100ms of `Shutdown()`
- [ ] No regression in existing `ui/handler_test.go` tests
- [ ] `Shutdown()` is safe to call multiple times (idempotent)
