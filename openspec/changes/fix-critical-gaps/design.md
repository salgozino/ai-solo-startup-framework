# Design: Fix Critical Gaps

## Technical Approach

Three self-contained bug fixes in `core/supervisor/` and `transport/a2a/server.go`. No new packages,
no new dependencies, no interface additions beyond what is described here. Fix order: Bug 2 → Bug 3 → Bug 1
(ascending blast radius, each a separate commit).

## Architecture Decisions

| Decision | Options | Tradeoff | Choice |
|----------|---------|----------|--------|
| Bug 2 body storage | Full `ActionIntent` JSON vs `PendingIntentBody string` | Full: general but JSON-in-JSON in file store; string: minimal, covers v1 only | **`PendingIntentBody string`** — `omitempty` preserves backward compat |
| Bug 3 recovery hook | Inject `registerFn func(...)` vs expose `TaskStore` on `Server` vs new `Server.SeedInputRequiredTask` | Fn: one param change, supervisor stays store-agnostic; TaskStore: new import dep; new method: two new symbols | **`registerFn` injection** — one caller, minimal surface |
| Bug 1 hook type | `OnStateChange func()` in `Config` vs observer channel vs event bus | Func: nil-safe, no goroutine lifecycle, matches existing `Config` pattern; channel: goroutine management overhead for a single sink | **`OnStateChange func()` + `SetOnStateChange`** — decoupled, zero new deps |
| Bug 1 call site | Inside FSM methods vs in `supervisor.go` after FSM calls | Inside FSM: would call user code while holding `fsm.mu` (lock-order hazard); outside: lock already released | **Call `notify()` in `supervisor.go` only**, never inside `state.go` methods |

## Data Flow

### Bug 2 — Body propagation

```
executeWithPolicy:
  for intent := range result.ActionIntents:
    body = extractBody(intent)          // reads intent.Payload["body"]
    Escalate → rec.PendingIntentBody = body; Store.Save
    Permit   → executeAction(ctx, kind, body, token)

executeResume (approved):
  rec = Store.Load(...)
  executeAction(ctx, rec.PendingIntentKind, rec.PendingIntentBody, token)
```

### Bug 3 — Recovery split

```
RecoverOpenTasks(ctx, handler, registerFn):
  filterWorkingTasks      → handler.SendMessage  (existing path)
  filterInputRequiredTasks → registerFn          (new path)

server.go New():
  store := taskstore.NewInMemory(...)
  registerFn = func(ctx, taskID, input string) error:
    task := &sdka2a.Task{ID: TaskID(taskID)}
    task.Status.State = TaskStateInputRequired
    _, err = store.Create(ctx, task)
    if ErrTaskAlreadyExists → return nil  // idempotent
    return err
  sup.RecoverOpenTasks(ctx, handler, registerFn)
```

### Bug 1 — SSE wiring

```
Supervisor:
  MarkReady()     → fsm.ready()        → notify()
  Execute()       → fsm.taskStarted()  → notify()
  Execute(deferred) → fsm.taskDone()   → notify()
  Shutdown()      → fsm.drain()        → notify()
  every Store.Save(...)                → notify()

notify() → cfg.OnStateChange() (nil-safe, outside FSM lock)

main.go (after ceoHandler + supervisor both constructed):
  runtimes[0].sup.SetOnStateChange(func() {
      ceoHandler.Broadcast(runtimes[0].sup.Status())
  })
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `core/supervisor/store.go` | Modify | Add `PendingIntentBody string \`json:"pending_intent_body,omitempty"\`` to `TaskRecord` |
| `core/supervisor/supervisor.go` | Modify | Bug 2: add `extractBody`; change `executeAction` signature; set/pass body. Bug 3: rename `filterOpenTasks` → `filterWorkingTasks`; add `filterInputRequiredTasks`; add `registerFn` param to `RecoverOpenTasks`. Bug 1: add `OnStateChange func()` to `Config`; add `SetOnStateChange`; add `notify()`; call `notify()` at all state-change sites |
| `transport/a2a/server.go` | Modify | Pass `registerFn` closure (over `store.Create`) to `RecoverOpenTasks` |
| `cmd/company/main.go` | Modify | Wire `runtimes[0].sup.SetOnStateChange(...)` after `ceoHandler` is created |
| `core/supervisor/policy_test.go` | Create | Assert `gateway.LastCall().Body != "telegram_send"` when action is permitted |
| `core/supervisor/supervisor_test.go` | Modify | Rename filter test; add `filterInputRequiredTasks` table test |
| `ui/handler_test.go` | Modify | Add `Broadcast` SSE delivery test: connect client, call Broadcast, assert received |

## Interfaces / Contracts

```go
// core/supervisor/store.go — TaskRecord addition (zero-value safe, omitempty)
PendingIntentBody string `json:"pending_intent_body,omitempty"`

// core/supervisor/supervisor.go — Config addition
OnStateChange func() // nil-safe; called after each supervisor/task state change

// core/supervisor/supervisor.go — new/changed signatures
func (s *Supervisor) SetOnStateChange(fn func())
func (s *Supervisor) notify()  // calls cfg.OnStateChange() if non-nil

func (s *Supervisor) executeAction(ctx context.Context, actionKind, body, token string) error
func extractBody(intent port.ActionIntent) string  // reads Payload["body"].(string), empty if absent

func filterWorkingTasks(records []TaskRecord) []TaskRecord         // excludes INPUT_REQUIRED
func filterInputRequiredTasks(records []TaskRecord) []TaskRecord   // only INPUT_REQUIRED

func (s *Supervisor) RecoverOpenTasks(
    ctx context.Context,
    handler a2asrv.RequestHandler,
    registerFn func(ctx context.Context, taskID, input string) error,
) error
```

## Testing Strategy

| Layer | What | Approach |
|-------|------|----------|
| Unit | Gateway body is non-empty and ≠ action kind | `policy_test.go`: fake gateway asserts `LastCall().Body != "telegram_send"` |
| Unit | `filterWorkingTasks` excludes INPUT_REQUIRED; `filterInputRequiredTasks` includes it | `supervisor_test.go`: table test with mixed states |
| Unit | `Broadcast` delivers SSE event to connected client | `handler_test.go`: register client, Broadcast, assert channel receives "event: state" |
| Integration | INPUT_REQUIRED task survives restart and resumes (not restarts) | `integration_test.go`: park task, re-wire handler with same store, approve → executeResume |

## Threat Matrix

N/A — no routing, shell command, subprocess, VCS/PR automation, executable-file classification, or
process-integration boundary is introduced by this change. Existing threat-matrix rows from the
foundation design carry forward unchanged.

## Migration / Rollout

No migration required. `PendingIntentBody` uses `omitempty`; existing records without the field
unmarshal cleanly (empty body → same as current behavior, no regression). Each bug is a separate
commit; any fix can be reverted independently without affecting the others.

## Open Questions

None — all three fixes are fully specified by the exploration and proposal.
