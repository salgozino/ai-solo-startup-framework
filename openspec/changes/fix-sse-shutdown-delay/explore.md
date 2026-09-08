## Exploration: fix-sse-shutdown-delay

### Current State

The backend serves a monitoring UI via `UIHandler` (ui/handler.go). The SSE
endpoint `/api/events` is handled by `handleEvents`, which runs this loop:

```go
for {
    select {
    case <-r.Context().Done():  // only fires when the connection is closed
        return
    case <-tick.C:
        // keepalive every 25s
    case msg := <-client.ch:
        // state-change event
    }
}
```

The shutdown sequence in `main.go` (lines 87–99):

```go
<-sig
ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
defer cancel()
_ = uiSrv.Shutdown(ctx)       // blocks up to 15s
for _, rt := range runtimes {
    _ = rt.srv.Shutdown(ctx)   // A2A servers
}
```

`http.Server.Shutdown()` does NOT close active HTTP/1.1 connections. It:
1. Closes the listener (no new connections).
2. Drains idle connections.
3. Waits for active connections to go idle **or** the context to expire.

SSE connections are permanently in `http.StateActive` (they are streaming).
They never become idle. The request context (`r.Context()`) is only canceled
when the underlying TCP connection closes — which `Shutdown()` does NOT do.
Result: `uiSrv.Shutdown(ctx)` waits the full 15-second timeout.

The A2A servers (`transport/a2a/server.go`) serve JSON-RPC (request/response),
so they are unlikely to hold long-lived connections that cause the same delay.

### Affected Areas

- `ui/handler.go` — `handleEvents` select loop; `UIHandler` struct; client management
- `cmd/company/main.go` — shutdown sequence (lines 87–99); `uiSrv` construction
- `ui/handler_test.go` — needs a test for the shutdown fast-path
- `ui/app.js` (indirect) — SSE error/reconnect at lines 130–135; no code change needed

### Approaches

1. **Explicit `UIHandler.Shutdown()` with a close channel**

   Add `shutdownCh chan struct{}` (closed via `sync.Once`) to `UIHandler`.
   Add a `Shutdown()` method. In `handleEvents`, add `case <-h.shutdownCh: return`.
   In `main.go`, call `ceoHandler.Shutdown()` before `uiSrv.Shutdown(ctx)`.

   - Pros:
     - Idiomatic Go; explicit ownership of lifecycle
     - Testable in isolation — just call `h.Shutdown()`
     - Only SSE handlers are affected; other endpoints continue normally
     - Zero changes outside `ui/handler.go` + `cmd/company/main.go`
   - Cons:
     - Adds a small amount of state to `UIHandler`
     - Must guard `Shutdown()` with `sync.Once` to be idempotent
   - Effort: **Low** (~15 lines of production code + 1 test)

2. **`http.Server.BaseContext` cancellation**

   Create a cancelable context (`shutdownCtx, shutdownCancel`) and assign it
   as the server's `BaseContext`. On signal, call `shutdownCancel()` before
   `uiSrv.Shutdown(ctx)`. All in-flight request contexts (including SSE) get
   canceled immediately, causing `r.Context().Done()` to fire.

   - Pros:
     - Zero changes to `UIHandler` — works with the existing select case
     - Very concise; entirely in `main.go`
     - Propagates naturally through the context tree
   - Cons:
     - Cancels ALL active request contexts, including concurrent POSTs
       (approve/reject/send) that happen to be in-flight at shutdown — those
       return errors rather than completing, which is arguably acceptable
     - `BaseContext` API (`func(net.Listener) context.Context`) is slightly
       surprising to readers unfamiliar with this pattern
     - Harder to unit-test in isolation (requires wiring a full server)
   - Effort: **Low** (~5 lines in main.go, no UIHandler changes)

3. **`http.Server.RegisterOnShutdown` + close channel**

   Register a callback via `uiSrv.RegisterOnShutdown(ceoHandler.Shutdown)`.
   The callback fires concurrently when `Shutdown()` begins; SSE handlers
   detect the closed channel and return.

   - Pros:
     - Decouples shutdown trigger from main.go's explicit call order
   - Cons:
     - `RegisterOnShutdown` callbacks run concurrently with shutdown — there is
       a small race window before the SSE goroutines actually return
     - Still requires modifying `UIHandler` (same changes as Approach 1)
     - Strictly more complex than Approach 1 for the same outcome
   - Effort: **Low** (same UIHandler changes + `RegisterOnShutdown` wiring)

### Recommendation

**Approach 1 — explicit `UIHandler.Shutdown()` with a close channel.**

It keeps the lifecycle of the SSE client store inside `UIHandler` (which already
owns the `clients` slice), makes the shutdown path trivially testable, and avoids
surprising side effects on non-SSE endpoints. The call order in `main.go` becomes:

```go
ceoHandler.Shutdown()          // signal SSE handlers to exit immediately
_ = uiSrv.Shutdown(ctx)        // drain remaining active connections (fast)
for _, rt := range runtimes {
    _ = rt.srv.Shutdown(ctx)   // A2A servers
}
```

Approach 2 is also valid and simpler if the team accepts that concurrent
approve/reject/send POSTs may fail at shutdown — acceptable for a dev/internal
tool. Mention it in the proposal as an alternative.

### Risks

- **Browser reconnect loop**: When the SSE connection is closed by the server,
  the browser EventSource fires `error` and attempts to reconnect after 3s
  (`setTimeout(connectSSE, 3000)` in app.js:134). Since the server is down,
  these reconnects fail silently. This is expected behavior and requires no
  code change.
- **`sync.Once` requirement on `Shutdown()`**: The method must be idempotent;
  calling `close()` twice on an unbuffered channel panics. A `sync.Once` guard
  or a buffered-close pattern is mandatory.
- **Missed broadcast after Shutdown()**: If `Broadcast()` is called after
  `Shutdown()`, it tries to send to channels whose receivers have exited. The
  existing `select { case c.ch <- msg: default: }` pattern already drops
  messages safely — no deadlock risk.
- **A2A server delay**: `transport/a2a/server.go` wraps `httpSrv.Shutdown(ctx)`.
  A2A uses JSON-RPC (short-lived requests), so unlikely to block. Monitor if
  a long-running `SendMessage` invocation is in flight during shutdown — the
  same `Shutdown()` also calls `sup.Shutdown()` which may have its own delay.

### Ready for Proposal

Yes — the bug is fully understood, the root cause is confirmed in code, and
Approach 1 has a clear, low-risk implementation path. The orchestrator should
tell the user: the fix adds `Shutdown()` to `UIHandler` and rewires the signal
handler in `main.go` to drain SSE connections before shutting down the HTTP
server, bringing shutdown time from ~15s to near-instant.
