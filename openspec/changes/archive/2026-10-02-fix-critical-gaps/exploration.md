# Exploration: fix-critical-gaps

Three spec violations found during the spec-vs-code audit. All are in the
`core/supervisor` + wiring layer. No UI, no config, no adapter changes required.

---

## Bug 1: SSE push is wired but never fires

### Current State

`UIHandler.Broadcast(data any)` exists and works: it serializes `data` as JSON
and sends an SSE `state` event to all connected `/api/events` clients.

`supervisor.Supervisor` has a private FSM (`core/supervisor/state.go`) whose
transitions are: `STARTING → IDLE`, `IDLE → WORKING`, `WORKING → IDLE`,
`IDLE → DRAINING → STOPPED`, `IDLE → RECOVERING`. Task-state changes are also
persisted to the file store inside `supervisor.go` at every `Store.Save` call.

**Neither the FSM nor the task-state path calls `Broadcast`.** The supervisor
has no reference to `UIHandler`, and `supervisor.Config` has no notification
callback. The frontend's `EventSource` connects, receives a keep-alive ping
every 25 s, and then nothing — so the UI falls back to manual refresh.

### Affected Areas

- `core/supervisor/supervisor.go` — must call a notify func after FSM transitions
  and after each `Store.Save` (task-state change)
- `core/supervisor/state.go` — FSM transitions happen here; notify must be called
  after the lock is released (outside FSM methods to avoid latency/deadlock risk)
- `cmd/company/main.go` — wires `UIHandler` after `materializeAgents`; must set
  the callback after both objects are created
- `core/supervisor/supervisor.go` / `Config` — add `OnStateChange func()` field
  (nil-safe; called with no args so it stays decoupled from ui package)

### Approaches

1. **Config callback (recommended)** — Add `OnStateChange func()` to `supervisor.Config`.
   The supervisor calls it (nil-check first) after FSM transitions and Store.Save calls.
   In `main.go`, after creating supervisor + UIHandler, call
   `runtimes[0].sup.SetOnStateChange(func() { ceoHandler.Broadcast(runtimes[0].sup.Status()) })`.
   - Pros: minimal change, no import cycle, nil-safe, consistent with existing Config pattern
   - Cons: requires a setter method OR a two-pass init in `main.go`
   - Effort: Low

2. **Observer channel** — Supervisor emits `Status` on a channel; UIHandler reads it.
   - Pros: more decoupled
   - Cons: goroutine lifecycle complexity, overkill for a single UI server
   - Effort: Medium

### Recommendation

Option 1. Add `OnStateChange func()` to `Config` (allowed to be nil at
`materializeAgents` time), and a `SetOnStateChange(fn func())` method.
`main.go` sets it after building both objects. Call the hook in `supervisor.go`
at:
  - End of `MarkReady()` (STARTING/RECOVERING → IDLE)
  - After `s.fsm.taskStarted()` in `Execute` (→ WORKING)
  - After `s.fsm.taskDone()` deferred in `Execute` (→ IDLE/STOPPED)
  - After every `s.cfg.Store.Save(...)` call (task state persisted — this is
    what the frontend needs to refresh the task list)
  - After `s.fsm.drain()` in `Shutdown` (→ DRAINING/STOPPED)

Broadcast payload: `sup.Status()` (carries `Addr` + `State`). The frontend
re-fetches `/api/tasks` on any SSE event, so the payload only needs to signal
"something changed".

### Risks

- **Lock ordering**: `OnStateChange` must be called after FSM lock is released
  (outside FSM methods) to avoid holding the lock while calling user code.
- **Multi-agent scope**: Only the CEO's supervisor is hooked in `main.go` (single
  `UIHandler`). Engineer-agent state changes do not push. Pre-existing scope gap;
  acceptable for v1.
- **No tests today**: `UIHandler.Broadcast` has 0 test coverage. A unit test for
  Broadcast sending to connected clients is needed.

---

## Bug 2: Gateway message body is hardcoded

### Current State

`executeAction` in `supervisor.go:396-407`:

```go
func (s *Supervisor) executeAction(ctx context.Context, actionKind string, token string) error {
    // ...
    return s.cfg.Gateway.Send(ctx, port.OutboundMessage{
        Channel: "telegram",
        Body:    actionKind,  // BUG: sends "telegram_send" as the message text
    })
}
```

Call sites:
- **Permit path** (`executeWithPolicy` line 333): passes `intent.Kind` but NOT `intent.Payload`
- **Resume path** (`executeResume` line 254): passes `rec.PendingIntentKind` — the actual
  message body was NEVER stored in `TaskRecord`

`ActionIntent` has `Payload map[string]any` intended to carry action-specific data
(e.g., `{"body": "Hello from CEO"}`), but the payload is never forwarded from
`executeWithPolicy` to `executeAction`, and `TaskRecord` has no field for it.

### Affected Areas

- `core/supervisor/supervisor.go` — `executeAction` signature + `executeWithPolicy`
  permit path + `executeResume` approval path
- `core/supervisor/store.go` — `TaskRecord` needs `PendingIntentBody string` to persist
  the message body across restarts (so the resume path can use it after a restart)

### Approaches

1. **Minimal body field (recommended)** — Add `PendingIntentBody string` to `TaskRecord`.
   Change `executeAction(ctx, actionKind, body, token string)`. In `executeWithPolicy`,
   extract body from `intent.Payload["body"]` (string cast, empty if missing). In the
   `Escalate` case, set `rec.PendingIntentBody`. In `executeResume`, pass
   `rec.PendingIntentBody` to `executeAction`.
   - Pros: minimal, backward-compatible JSON (omitempty), fixes both permit + resume paths
   - Cons: establishes an implicit `"body"` key convention in `Payload`
   - Effort: Low

2. **Forward full ActionIntent** — Pass the full `port.ActionIntent` to `executeAction`.
   Store a serialized `PendingIntentPayload json.RawMessage` in `TaskRecord`.
   - Pros: more general, future-proof
   - Cons: JSON in JSON in the store; more code surface
   - Effort: Medium

### Recommendation

Option 1. Define a helper `extractBody(intent port.ActionIntent) string` that reads
`intent.Payload["body"].(string)`. For `telegram_send`, adapters must populate
`Payload["body"]` with the actual message. Document this convention on `ActionIntent`.

### Risks

- **Adapters don't populate Payload**: Current adapters (claudecode, opencode) are
  text-in/text-out and never set `ActionIntents`. The policy test uses a fake provider
  that can set `Payload`. In production the gateway body will be empty until adapters
  are updated to parse their output into structured intents. This bug fix is necessary
  but not sufficient — adapters are a separate concern.
- **Resume path loses body on old data**: Existing `TaskRecord` JSON files on disk have
  no `PendingIntentBody` field. After the fix, records created before the upgrade will
  resume with an empty body. Acceptable (no worse than current behavior).
- **Channel is hardcoded**: `executeAction` hardcodes `Channel: "telegram"`. For a future
  `email_send` action this needs to be parameterized. Out of scope for this fix.

---

## Bug 3: Recovery of INPUT_REQUIRED tasks is incomplete

### Current State

`filterOpenTasks` in `supervisor.go:470-479`:

```go
func filterOpenTasks(records []TaskRecord) []TaskRecord {
    var open []TaskRecord
    for _, r := range records {
        state := a2a.TaskState(r.State)
        if !state.Terminal() && state != a2a.TaskStateInputRequired {
            open = append(open, r)
        }
    }
    return open
}
```

The `!= TaskStateInputRequired` exclusion was intentional: re-submitting an
INPUT_REQUIRED task via `handler.SendMessage` would trigger `Execute()` with
`isResume = false` (if a2asrv's in-memory store doesn't know the task), which
causes the provider to re-run — wrong behavior.

However, the spec requires that INPUT_REQUIRED tasks survive a restart. The gap:
the file store correctly persists them, but `a2asrv`'s in-memory `taskstore.InMemory`
is wiped on restart. When `PostVerdict` later calls `handler.SendMessage(msg with
TaskID)`, a2asrv does NOT find the task → creates a fresh entry → calls `Execute`
with `execCtx.StoredTask = nil` (new-task path) → calls provider again instead of
routing to `executeResume`. The approval silently re-starts the task.

The a2asrv SDK v2.5.0 exposes `taskstore.InMemory.Create(ctx, *a2a.Task) (TaskVersion, error)` —
the only API needed to seed a task into a2asrv's store without triggering execution.

### Affected Areas

- `core/supervisor/supervisor.go` — `RecoverOpenTasks` must handle INPUT_REQUIRED
  tasks via a separate "register" path (not `handler.SendMessage`)
- `transport/a2a/server.go` — must expose the `taskstore.Store` so the registration
  function can call `store.Create`
- `core/supervisor/supervisor_test.go` — test at line 176 validates the current
  (broken) behavior; will need updating for the new split behavior

### Approaches

1. **Inject register function (recommended)** — Change `RecoverOpenTasks` signature to
   add `registerFn func(ctx context.Context, taskID, input string) error`. Split
   `filterOpenTasks` into `filterWorkingTasks` (existing) + `filterInputRequiredTasks`
   (new). For WORKING: continue using `handler.SendMessage`. For INPUT_REQUIRED: call
   `registerFn`. In `server.go`, pass a closure that calls `store.Create` with a
   minimal `*a2a.Task{ID: taskID, Status: {State: INPUT_REQUIRED}}`.
   - Pros: correct, minimal, no new public API on Supervisor beyond signature change
   - Cons: breaks `RecoverOpenTasks` call-site (only one caller in `server.go`)
   - Effort: Low–Medium

2. **Expose TaskStore on Server** — Add `TaskStore() taskstore.Store` to `Server`.
   Let supervisor call it directly. Tighter coupling between packages.
   - Pros: flexible
   - Cons: supervisor package would need to import taskstore (new dep)
   - Effort: Medium

3. **Pre-populate via a new Server method** — Add `Server.SeedInputRequiredTask(ctx, id, input)`.
   Call it from a new `Supervisor.RecoverInputRequired(ctx, seedFn)` method.
   - Pros: clear separation of concerns
   - Cons: two new methods instead of one param change
   - Effort: Medium

### Recommendation

Option 1. Minimal signature change on `RecoverOpenTasks`. The injection function
stays in `transport/a2a/server.go` where the store reference lives. The supervisor
stays agnostic of the taskstore package.

`*a2a.Task` to seed:
```go
task := &sdka2a.Task{ID: sdka2a.TaskID(rec.TaskID)}
task.Status.State = sdka2a.TaskStateInputRequired
// History is intentionally empty: executeResume reads from the file store
_, err := store.Create(ctx, task)
// ErrTaskAlreadyExists on re-recovery: ignore silently
```

### Risks

- **store.Create user mismatch**: `taskstore.InMemory` stores the `user` set by
  the Authenticator at Create time. The authenticator always returns `"supervisor"`,
  and `PostVerdict` runs under `context.Background()` — same authenticator → same
  user → `Get` succeeds. This is safe.
- **Stale test**: `supervisor_test.go` line 176 explicitly asserts INPUT_REQUIRED
  is NOT in `filterOpenTasks`. The test is still correct (filterWorkingTasks excludes
  INPUT_REQUIRED too), but the test name/comment should be updated for clarity.
- **ErrTaskAlreadyExists on double-recovery**: If `RecoverOpenTasks` is called twice
  (e.g., bug, test teardown), the second Create returns `ErrTaskAlreadyExists`. Must
  ignore this error (treat as idempotent success).
- **resume path depends on file store**: After seeding, `executeResume` loads the
  record from the file store. If the file is corrupted or deleted between restart and
  approval, the resume fails. Pre-existing risk, not introduced by this fix.

---

## Ready for Proposal

**Yes.** All three bugs are well-understood with clear, minimal fix paths.

Recommended fix order:
1. Bug 2 (gateway body) — fully self-contained, two files, no signature changes
2. Bug 3 (INPUT_REQUIRED recovery) — one signature change + new helper functions
3. Bug 1 (SSE push) — hooks into the most places, benefits from 2 and 3 being done first

Tests to add:
- `core/supervisor/policy_test.go`: assert `gateway.LastCall().Body != "telegram_send"`
- `core/supervisor/supervisor_test.go`: test `filterInputRequiredTasks` returns correct records
- `ui/handler_test.go`: test `Broadcast` delivers to connected SSE client
- `cmd/company/e2e_test.go` or `core/supervisor/integration_test.go`: INPUT_REQUIRED
  task remains resumable after simulated restart (re-wiring a new handler with same store)
